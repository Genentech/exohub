package storage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/Genentech/exohub/go/adb-standalone/internal/config"
)

// S3Adapter implements Adapter for AWS S3 and S3-compatible stores (e.g. MinIO).
type S3Adapter struct {
	client  *s3.Client
	presign *s3.PresignClient
}

// NewS3Adapter constructs an S3Adapter from the given S3 config.
// When cfg.Endpoint is non-empty it is used as a custom endpoint (MinIO / other
// S3-compatible stores). Static credentials are used when AccessKeyID is set.
func NewS3Adapter(ctx context.Context, cfg *config.S3Config) (*S3Adapter, error) {
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(regionOrDefault(cfg.Region)),
	}

	if cfg.AccessKeyID != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	s3opts := []func(*s3.Options){}
	if cfg.Endpoint != "" {
		ep := cfg.Endpoint
		s3opts = append(s3opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(ep)
		})
	}
	if cfg.UsePathStyle {
		s3opts = append(s3opts, func(o *s3.Options) {
			o.UsePathStyle = true
		})
	}

	client := s3.NewFromConfig(awsCfg, s3opts...)
	return &S3Adapter{
		client:  client,
		presign: s3.NewPresignClient(client),
	}, nil
}

// PresignGetURL generates a presigned GET URL for the object at bucket/key.
func (a *S3Adapter) PresignGetURL(ctx context.Context, bucket, key string, expiry time.Duration) (string, error) {
	req, err := a.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", fmt.Errorf("presign get s3://%s/%s: %w", bucket, key, err)
	}
	return req.URL, nil
}

// Parts returns the individual parts of a multipart-uploaded S3 object as
// presigned GET URLs. For single-part objects, a single Part covering the whole
// object is returned. For multipart objects the part ranges are obtained via
// GetObjectAttributes; if that fails (older MinIO), falls back to a single presigned GET.
func (a *S3Adapter) Parts(ctx context.Context, bucket, key string, expiry time.Duration) ([]Part, error) {
	head, err := a.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("head s3://%s/%s: %w", bucket, key, err)
	}

	totalSize := aws.ToInt64(head.ContentLength)
	etag := aws.ToString(head.ETag)
	partCount := etagPartCount(etag)

	if partCount <= 1 {
		u, err := a.PresignGetURL(ctx, bucket, key, expiry)
		if err != nil {
			return nil, err
		}
		return []Part{{
			PartNumber: 1,
			Size:       totalSize,
			URL:        u,
			S3Path:     bucket + "/" + key,
			ChunkKey:   strings.Trim(etag, `"`),
		}}, nil
	}

	// Multipart: use GetObjectAttributes to get per-part sizes.
	attrs, err := a.client.GetObjectAttributes(ctx, &s3.GetObjectAttributesInput{
		Bucket:           aws.String(bucket),
		Key:              aws.String(key),
		ObjectAttributes: []s3types.ObjectAttributes{s3types.ObjectAttributesObjectParts},
	})
	if err == nil && attrs.ObjectParts != nil {
		return a.presignParts(ctx, bucket, key, expiry, etag, attrs.ObjectParts.Parts)
	}

	// Fallback to single presigned GET when GetObjectAttributes is unavailable.
	u, err := a.PresignGetURL(ctx, bucket, key, expiry)
	if err != nil {
		return nil, err
	}
	return []Part{{
		PartNumber: 1,
		Size:       totalSize,
		URL:        u,
		S3Path:     bucket + "/" + key,
		ChunkKey:   strings.Trim(etag, `"`),
	}}, nil
}

// presignParts generates presigned range-GET URLs for each S3 object part.
func (a *S3Adapter) presignParts(
	ctx context.Context,
	bucket, key string,
	expiry time.Duration,
	etag string,
	parts []s3types.ObjectPart,
) ([]Part, error) {
	result := make([]Part, 0, len(parts))
	var offset int64
	for _, p := range parts {
		partSize := aws.ToInt64(p.Size)
		rangeHeader := fmt.Sprintf("bytes=%d-%d", offset, offset+partSize-1)

		req, err := a.presign.PresignGetObject(ctx, &s3.GetObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
			Range:  aws.String(rangeHeader),
		}, s3.WithPresignExpires(expiry))
		if err != nil {
			return nil, fmt.Errorf("presign part %d of s3://%s/%s: %w", aws.ToInt32(p.PartNumber), bucket, key, err)
		}

		partNum := int(aws.ToInt32(p.PartNumber))
		result = append(result, Part{
			PartNumber: partNum,
			Size:       partSize,
			URL:        req.URL,
			S3Path:     fmt.Sprintf("%s/%s#%d", bucket, key, partNum),
			ChunkKey:   strings.Trim(aws.ToString(p.ChecksumSHA256), `"`),
		})
		offset += partSize
	}
	_ = etag // used for context only
	return result, nil
}

// etagPartCount extracts the number of parts from an S3 multipart ETag (format: "<hash>-<n>").
// Returns 1 for single-part ETags.
func etagPartCount(etag string) int {
	etag = strings.Trim(etag, `"`)
	idx := strings.LastIndex(etag, "-")
	if idx < 0 {
		return 1
	}
	var n int
	if _, err := fmt.Sscanf(etag[idx+1:], "%d", &n); err != nil || n < 1 {
		return 1
	}
	return n
}

// IDToKey derives the S3 object key from an ADB document id.
// ADB id format: "project:path@version" → key: "project/path"
func IDToKey(id string) string {
	// Split on ':' to get project and "path@version"
	colon := strings.Index(id, ":")
	if colon < 0 {
		return id
	}
	project := id[:colon]
	rest := id[colon+1:]
	// Strip @version suffix.
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		rest = rest[:at]
	}
	return project + "/" + rest
}

// EnsureBucket creates the bucket if it does not already exist. Idempotent.
func EnsureBucket(ctx context.Context, client *s3.Client, bucket, region string) error {
	_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(bucket),
	})
	if err == nil {
		return nil // already exists
	}
	_, err = client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		return fmt.Errorf("create bucket %q: %w", bucket, err)
	}
	return nil
}

// Client returns the underlying S3 client, used for bucket management operations.
func (a *S3Adapter) Client() *s3.Client {
	return a.client
}

func regionOrDefault(r string) string {
	if r == "" {
		return "us-east-1"
	}
	return r
}
