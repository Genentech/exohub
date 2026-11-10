package init

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// defaultMigrationJobs bounds how many objects --migrate-content copies in
// parallel when --jobs isn't given.
const defaultMigrationJobs = 8

// flagMigrateJobs bounds --migrate-content's concurrency (exo-cli#168).
var flagMigrateJobs int

// credHelperCreds is exo-credential-helper's JSON output shape (matching
// git-annex-remote-s5cmd's fetchGrantsCredentials/fetchBaseCredentials).
type credHelperCreds struct {
	AccessKeyID     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
	Expiration      string `json:"Expiration"`
}

// credentialFetcher obtains AWS credentials scoped to s3url the same way
// git-annex-remote-s5cmd does, via exo-credential-helper: the exohub
// profile, then the identity-provider fallback, or S3 Access Grants for
// grants remotes. Overridable in tests to stub the helper.
var credentialFetcher = fetchCredentialsViaHelper

func credentialHelperBin() string {
	if v := strings.TrimSpace(os.Getenv("EXO_CREDENTIAL_HELPER_BIN")); v != "" {
		return v
	}
	return "exo-credential-helper"
}

// fetchCredentialsViaHelper shells out to exo-credential-helper for s3url,
// mirroring git-annex-remote-s5cmd's fetchGrantsCredentials (grants=true,
// scoped STS credentials per location) and fetchBaseCredentials (grants=false,
// the caller's own identity). exo never relies on the default AWS chain
// (env vars / ~/.aws / instance role) for remote data (exo-cli#168).
func fetchCredentialsViaHelper(s3url string, grants bool, group, permission string) (aws.Credentials, error) {
	args := []string{"--s3url", s3url}
	if grants {
		args = append(args, "--permission", permission)
	} else {
		args = append(args, "--grants=false")
		if group != "" {
			args = append(args, "--group", group)
		}
	}
	cmd := exec.Command(credentialHelperBin(), args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return aws.Credentials{}, fmt.Errorf("exo-credential-helper --s3url %s: %s", s3url, msg)
	}

	var creds credHelperCreds
	if err := json.Unmarshal(outBuf.Bytes(), &creds); err != nil {
		return aws.Credentials{}, fmt.Errorf("parsing exo-credential-helper output for %s: %w", s3url, err)
	}
	if creds.AccessKeyID == "" {
		return aws.Credentials{}, fmt.Errorf("exo-credential-helper returned empty credentials for %s", s3url)
	}
	return aws.Credentials{
		AccessKeyID:     creds.AccessKeyID,
		SecretAccessKey: creds.SecretAccessKey,
		SessionToken:    creds.SessionToken,
	}, nil
}

// remoteGroupValue resolves the remote-scoped credentials group (internal
// builds only; nil hook on OSS) the same way groupConfigArg does.
func remoteGroupValue(remote RemoteConfig) string {
	if remoteCredentialGroup == nil {
		return ""
	}
	return remoteCredentialGroup(remote)
}

// sameIdentity reports whether a and b are the same credentials, meaning a
// single client built from either can read the source and write the
// destination -- the condition under which a server-side S3 CopyObject is
// possible instead of a stream fallback.
func sameIdentity(a, b aws.Credentials) bool {
	return a.AccessKeyID != "" && a.AccessKeyID == b.AccessKeyID && a.SecretAccessKey == b.SecretAccessKey
}

// migrationRegion resolves the AWS region to use for remote's S3 clients.
func migrationRegion(remote RemoteConfig) string {
	if remote.Datacenter != "" {
		return remote.Datacenter
	}
	if v := strings.TrimSpace(os.Getenv("AWS_REGION")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("AWS_DEFAULT_REGION")); v != "" {
		return v
	}
	return "us-east-1"
}

// s3TestBaseEndpoint overrides the S3 endpoint (path-style) for tests, so
// migrateRemoteContent's full orchestration can be exercised against a fake
// S3 server without touching real AWS. Unset in production.
var s3TestBaseEndpoint string

func s3ClientForCreds(creds aws.Credentials, region string) *s3.Client {
	opts := s3.Options{
		Region:      region,
		Credentials: credentials.NewStaticCredentialsProvider(creds.AccessKeyID, creds.SecretAccessKey, creds.SessionToken),
	}
	if s3TestBaseEndpoint != "" {
		opts.BaseEndpoint = aws.String(s3TestBaseEndpoint)
		opts.UsePathStyle = true
	}
	return s3.New(opts)
}

// migrationMode describes how objects are copied.
type migrationMode string

const (
	modeServerSide migrationMode = "server-side"
	modeStream     migrationMode = "stream"
)

// migrationAction is one object --migrate-content either copies or skips
// (because it's already present at the destination) while moving a remote's
// content from its old S3 prefix to its new one.
type migrationAction struct {
	Key  string `json:"key"`
	Size int64  `json:"size"`
	ETag string `json:"etag,omitempty"`
}

// migrationPlan is the set of object copies needed to move a remote's
// content from oldS3URL to newS3URL, as computed by planContentMigration.
type migrationPlan struct {
	Remote    string            `json:"remote"`
	Bucket    string            `json:"bucket"`
	NewBucket string            `json:"new_bucket,omitempty"`
	OldPrefix string            `json:"old_prefix"`
	NewPrefix string            `json:"new_prefix"`
	Mode      migrationMode     `json:"mode"`
	ToCopy    []migrationAction `json:"to_copy,omitempty"`
	Skipped   []migrationAction `json:"skipped,omitempty"`
	Excluded  []string          `json:"excluded_foreign,omitempty"`
	Lost      []string          `json:"lost,omitempty"`
}

func (p *migrationPlan) destBucket() string {
	if p.NewBucket != "" {
		return p.NewBucket
	}
	return p.Bucket
}

func (p *migrationPlan) totalBytes() int64 {
	var total int64
	for _, a := range p.ToCopy {
		total += a.Size
	}
	return total
}

// migrationReport is the outcome of actually running and verifying a
// migrationPlan.
type migrationReport struct {
	Copied         []string          `json:"copied,omitempty"`
	AlreadyPresent []string          `json:"already_present,omitempty"`
	Failed         map[string]string `json:"failed,omitempty"`
	MissingAfter   []string          `json:"missing_after_copy,omitempty"`
}

func (r migrationReport) ok() bool {
	return len(r.Failed) == 0 && len(r.MissingAfter) == 0
}

// listObjectKeys returns every object under prefix in bucket, with size and
// ETag as reported by S3.
func listObjectKeys(ctx context.Context, client *s3.Client, bucket, prefix string) ([]migrationAction, error) {
	var out []migrationAction
	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, obj := range page.Contents {
			out = append(out, migrationAction{
				Key:  aws.ToString(obj.Key),
				Size: aws.ToInt64(obj.Size),
				ETag: strings.Trim(aws.ToString(obj.ETag), `"`),
			})
		}
	}
	return out, nil
}

// headObject returns the size/ETag of bucket/key, and whether it exists.
func headObject(ctx context.Context, client *s3.Client, bucket, key string) (size int64, etag string, exists bool, err error) {
	out, headErr := client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if headErr == nil {
		return aws.ToInt64(out.ContentLength), strings.Trim(aws.ToString(out.ETag), `"`), true, nil
	}
	var notFound *types.NotFound
	if errors.As(headErr, &notFound) {
		return 0, "", false, nil
	}
	var apiErr interface{ ErrorCode() string }
	if errors.As(headErr, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NotFound", "NoSuchKey", "404":
			return 0, "", false, nil
		}
	}
	return 0, "", false, headErr
}

// isMultipartETag reports whether etag looks like a multipart-upload ETag
// (not a plain MD5 of the object body), which can't be compared directly
// across a separate PUT/COPY.
func isMultipartETag(etag string) bool {
	return strings.Contains(etag, "-")
}

// locationLogKeys returns the git-annex keys the location log says are
// present on remoteName, scoped to the current work tree (the same scope
// invalidateMovedRemotePresence's fsck uses) -- used to cross-check the old
// prefix's listing and flag expected-but-absent objects as lost, rather
// than silently not copying them.
func locationLogKeys(remoteName string) []string {
	output, err := command("git", "annex", "find", "--in", remoteName, "--format=${key}\n").Output()
	if err != nil {
		return nil
	}
	var keys []string
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			keys = append(keys, line)
		}
	}
	return keys
}

// keyPresentAmong reports whether git-annex key's object (flat, or any
// chunk of it per git-annex's chunk log naming "<key>-S<size>-C<n>") is
// present among listed objects under prefix.
func keyPresentAmong(listed map[string]bool, prefix, key string) bool {
	base := prefix + key
	if listed[base] {
		return true
	}
	chunkPrefix := base + "-S"
	for k := range listed {
		if strings.HasPrefix(k, chunkPrefix) {
			return true
		}
	}
	return false
}

// otherRemotePrefix is another remote's (bucket, prefix), used to exclude
// objects that belong to a sibling remote sharing the same bucket.
type otherRemotePrefix struct {
	Name   string
	Bucket string
	Prefix string
}

func otherRemotePrefixes(remotesConfig *RemotesConfig, excludeName, bucket string) []otherRemotePrefix {
	if remotesConfig == nil {
		return nil
	}
	var out []otherRemotePrefix
	for _, r := range remotesConfig.Remotes {
		if r.Name == excludeName || r.S3URL == "" {
			continue
		}
		b, p, err := parseS3URL(r.S3URL)
		if err != nil || b != bucket {
			continue
		}
		out = append(out, otherRemotePrefix{Name: r.Name, Bucket: b, Prefix: p})
	}
	return out
}

// belongsToOtherRemote reports whether key falls under a sibling remote's
// (more specific, nested) prefix rather than ownPrefix.
func belongsToOtherRemote(key string, others []otherRemotePrefix, ownPrefix string) (string, bool) {
	for _, o := range others {
		if o.Prefix == "" || o.Prefix == ownPrefix || len(o.Prefix) <= len(ownPrefix) {
			continue
		}
		if strings.HasPrefix(key, o.Prefix) {
			return o.Name, true
		}
	}
	return "", false
}

// planContentMigration lists every object under oldS3URL's prefix (except
// ones belonging to a sibling remote sharing the bucket), builds the plan
// to copy each one to the matching key under newS3URL's prefix, skips
// objects already present at the destination with the same size (and ETag,
// when comparable), and flags any of remote's location-log keys that have
// no matching object on the old prefix as lost instead of silently not
// copying them.
func planContentMigration(ctx context.Context, srcClient, destClient *s3.Client, mode migrationMode, remote RemoteConfig, remotesConfig *RemotesConfig, oldS3URL, newS3URL string) (*migrationPlan, error) {
	oldBucket, oldPrefix, err := parseS3URL(oldS3URL)
	if err != nil {
		return nil, fmt.Errorf("old s3url: %w", err)
	}
	newBucket, newPrefix, err := parseS3URL(newS3URL)
	if err != nil {
		return nil, fmt.Errorf("new s3url: %w", err)
	}
	if oldBucket == newBucket && (oldPrefix == newPrefix || strings.HasPrefix(newPrefix, oldPrefix) || strings.HasPrefix(oldPrefix, newPrefix)) {
		return nil, fmt.Errorf("old and new s3url prefixes overlap (s3://%s/%s and s3://%s/%s); refusing to migrate blindly", oldBucket, oldPrefix, newBucket, newPrefix)
	}

	listed, err := listObjectKeys(ctx, srcClient, oldBucket, oldPrefix)
	if err != nil {
		return nil, fmt.Errorf("listing objects under s3://%s/%s: %w", oldBucket, oldPrefix, err)
	}

	listedByKey := make(map[string]bool, len(listed))
	for _, o := range listed {
		listedByKey[o.Key] = true
	}

	plan := &migrationPlan{Remote: remote.Name, Bucket: oldBucket, OldPrefix: oldPrefix, NewPrefix: newPrefix, Mode: mode}
	if newBucket != oldBucket {
		plan.NewBucket = newBucket
	}
	destBucket := plan.destBucket()

	others := otherRemotePrefixes(remotesConfig, remote.Name, oldBucket)
	for _, o := range listed {
		if owner, excluded := belongsToOtherRemote(o.Key, others, oldPrefix); excluded {
			plan.Excluded = append(plan.Excluded, fmt.Sprintf("%s (belongs to remote %q)", o.Key, owner))
			continue
		}

		rel := strings.TrimPrefix(o.Key, oldPrefix)
		destKey := newPrefix + rel
		destSize, destETag, exists, err := headObject(ctx, destClient, destBucket, destKey)
		if err != nil {
			return nil, fmt.Errorf("checking destination s3://%s/%s: %w", destBucket, destKey, err)
		}
		action := migrationAction{Key: rel, Size: o.Size, ETag: o.ETag}
		if exists && destSize == o.Size && (isMultipartETag(o.ETag) || isMultipartETag(destETag) || destETag == o.ETag) {
			plan.Skipped = append(plan.Skipped, action)
			continue
		}
		plan.ToCopy = append(plan.ToCopy, action)
	}

	for _, key := range locationLogKeys(remote.Name) {
		if !keyPresentAmong(listedByKey, oldPrefix, key) {
			plan.Lost = append(plan.Lost, key)
		}
	}

	sort.Slice(plan.ToCopy, func(i, j int) bool { return plan.ToCopy[i].Key < plan.ToCopy[j].Key })
	sort.Slice(plan.Skipped, func(i, j int) bool { return plan.Skipped[i].Key < plan.Skipped[j].Key })
	sort.Strings(plan.Excluded)
	sort.Strings(plan.Lost)
	return plan, nil
}

// copyObjectServerSide copies bucket/oldKey to destBucket/newKey with a
// single client able to read both (CopyObject), used only when the source
// and destination share one credential identity.
func copyObjectServerSide(ctx context.Context, client *s3.Client, bucket, oldKey, destBucket, newKey string) error {
	_, err := client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(destBucket),
		Key:        aws.String(newKey),
		CopySource: aws.String(copySourcePath(bucket, oldKey)),
	})
	return err
}

// copySourcePath builds the CopySource value S3 expects: "bucket/key", with
// each path segment of key percent-encoded (but '/' left as a separator).
func copySourcePath(bucket, key string) string {
	parts := strings.Split(key, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return bucket + "/" + strings.Join(parts, "/")
}

// copyObjectStream downloads bucket/oldKey to a bounded temp file with
// srcClient's (source) credentials, then uploads it to destBucket/newKey
// with destClient's (destination) credentials, deleting the temp file
// afterwards -- used when a single credential set can't read the source
// and write the destination (different buckets, or grants remotes whose
// STS credentials are scoped per location).
func copyObjectStream(ctx context.Context, srcClient, destClient *s3.Client, bucket, oldKey, destBucket, newKey string) error {
	get, err := srcClient.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(oldKey)})
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer get.Body.Close()

	tmp, err := os.CreateTemp("", "exo-migrate-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, get.Body); err != nil {
		tmp.Close()
		return fmt.Errorf("downloading to temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	f, err := os.Open(tmpPath)
	if err != nil {
		return fmt.Errorf("reopening temp file: %w", err)
	}
	defer f.Close()

	_, err = destClient.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(destBucket),
		Key:           aws.String(newKey),
		Body:          f,
		ContentLength: get.ContentLength,
	})
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}
	return nil
}

// runContentMigration copies every plan.ToCopy action with bounded
// concurrency (jobs), then verifies every planned object (copied or
// already-skipped) is actually present at the destination with the
// expected size (and ETag, when comparable) before reporting success --
// nothing confirms objects arrived until this check runs.
func runContentMigration(ctx context.Context, srcClient, destClient *s3.Client, plan *migrationPlan, jobs int) migrationReport {
	if jobs <= 0 {
		jobs = defaultMigrationJobs
	}
	report := migrationReport{Failed: map[string]string{}}
	var mu sync.Mutex
	sem := make(chan struct{}, jobs)
	var wg sync.WaitGroup

	for _, action := range plan.ToCopy {
		wg.Add(1)
		sem <- struct{}{}
		go func(a migrationAction) {
			defer wg.Done()
			defer func() { <-sem }()
			oldKey := plan.OldPrefix + a.Key
			newKey := plan.NewPrefix + a.Key
			var err error
			if plan.Mode == modeServerSide {
				err = copyObjectServerSide(ctx, destClient, plan.Bucket, oldKey, plan.destBucket(), newKey)
			} else {
				err = copyObjectStream(ctx, srcClient, destClient, plan.Bucket, oldKey, plan.destBucket(), newKey)
			}
			mu.Lock()
			if err != nil {
				report.Failed[a.Key] = err.Error()
			} else {
				report.Copied = append(report.Copied, a.Key)
			}
			mu.Unlock()
		}(action)
	}
	wg.Wait()

	for _, a := range plan.Skipped {
		report.AlreadyPresent = append(report.AlreadyPresent, a.Key)
	}

	// Verify: nothing confirms a copy actually landed until we check. Also
	// re-verify already-skipped objects, since a prior partial migration's
	// "skip" was computed from a HeadObject that could itself be stale.
	destBucket := plan.destBucket()
	for _, a := range append(append([]migrationAction{}, plan.ToCopy...), plan.Skipped...) {
		if _, failed := report.Failed[a.Key]; failed {
			continue
		}
		newKey := plan.NewPrefix + a.Key
		size, etag, exists, err := headObject(ctx, destClient, destBucket, newKey)
		if err != nil || !exists || size != a.Size || (!isMultipartETag(a.ETag) && !isMultipartETag(etag) && etag != a.ETag) {
			report.MissingAfter = append(report.MissingAfter, a.Key)
		}
	}

	sort.Strings(report.Copied)
	sort.Strings(report.AlreadyPresent)
	sort.Strings(report.MissingAfter)
	return report
}

// printMigrationPlan prints plan as a --dry-run preview, either as JSON
// (matching the --json conventions of exo sync --all/--check) or as text.
func printMigrationPlan(w io.Writer, plan *migrationPlan, jsonOutput bool) {
	if jsonOutput {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(plan)
		return
	}
	fmt.Fprintf(w, "Would copy %d object(s) (%d bytes), skip %d already at the destination, mode=%s (s3://%s/%s -> s3://%s/%s)\n",
		len(plan.ToCopy), plan.totalBytes(), len(plan.Skipped), plan.Mode, plan.Bucket, plan.OldPrefix, plan.destBucket(), plan.NewPrefix)
	if len(plan.Excluded) > 0 {
		fmt.Fprintf(w, "Excluding %d object(s) that belong to another remote's prefix\n", len(plan.Excluded))
	}
	if len(plan.Lost) > 0 {
		fmt.Fprintf(w, "WARNING: %d key(s) believed present on '%s' have no matching object on the old prefix (lost): %s\n",
			len(plan.Lost), plan.Remote, strings.Join(plan.Lost, ", "))
	}
	for _, a := range plan.ToCopy {
		fmt.Fprintf(w, "  copy %s (%d bytes)\n", a.Key, a.Size)
	}
	for _, a := range plan.Skipped {
		fmt.Fprintf(w, "  skip %s (already at destination)\n", a.Key)
	}
}

// printMigrationReport prints the outcome of runContentMigration.
func printMigrationReport(w io.Writer, report migrationReport, jsonOutput bool) {
	if jsonOutput {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
		return
	}
	fmt.Fprintf(w, "Content migration: %d copied, %d already present, %d failed, %d missing after copy\n",
		len(report.Copied), len(report.AlreadyPresent), len(report.Failed), len(report.MissingAfter))
	for key, errMsg := range report.Failed {
		fmt.Fprintf(w, "  FAILED %s: %s\n", key, errMsg)
	}
	for _, key := range report.MissingAfter {
		fmt.Fprintf(w, "  MISSING %s: not found at the destination after copy\n", key)
	}
}

// migrateRemoteContent copies remote's content from oldS3URL to newS3URL,
// verifying every planned object is actually present at the destination
// before returning success. Credentials for each side are obtained from
// exo-credential-helper (the exohub profile, the identity-provider
// fallback, or S3 Access Grants) the same way git-annex-remote-s5cmd does
// -- never the default AWS
// chain. A server-side S3 CopyObject is used only when a single credential
// set can read the source and write the destination (same bucket and
// identity); otherwise objects are streamed through a bounded temp file.
// It never deletes anything at the old prefix -- cleanup stays a separate,
// explicit step. On dryRun, it only prints the plan and changes nothing.
// On any failure or any object missing after copy, it returns an error so
// the caller leaves the remote's s3url unchanged.
func migrateRemoteContent(remote RemoteConfig, oldS3URL, newS3URL string, dryRun, jsonOutput bool) error {
	ctx := context.Background()
	group := remoteGroupValue(remote)

	oldCreds, err := credentialFetcher(oldS3URL, remote.Grants, group, "READ")
	if err != nil {
		return fmt.Errorf("content migration for '%s': credentials for old location: %w", remote.Name, err)
	}
	newCreds, err := credentialFetcher(newS3URL, remote.Grants, group, "READWRITE")
	if err != nil {
		return fmt.Errorf("content migration for '%s': credentials for new location: %w", remote.Name, err)
	}

	region := migrationRegion(remote)
	srcClient := s3ClientForCreds(oldCreds, region)
	destClient := s3ClientForCreds(newCreds, region)

	mode := modeStream
	if sameIdentity(oldCreds, newCreds) {
		mode = modeServerSide
	}

	remotesConfig, _ := LoadRemotesConfig()

	if !jsonOutput {
		fmt.Printf("Migrating content for '%s': %s -> %s (mode=%s)\n", remote.Name, oldS3URL, newS3URL, mode)
	}
	plan, err := planContentMigration(ctx, srcClient, destClient, mode, remote, remotesConfig, oldS3URL, newS3URL)
	if err != nil {
		return fmt.Errorf("content migration for '%s': %w", remote.Name, err)
	}

	if dryRun {
		printMigrationPlan(os.Stdout, plan, jsonOutput)
		return nil
	}

	report := runContentMigration(ctx, srcClient, destClient, plan, flagMigrateJobs)
	printMigrationReport(os.Stdout, report, jsonOutput)
	if !report.ok() {
		return fmt.Errorf("content migration for '%s': %d failed, %d missing after copy -- s3url left unchanged; see 'exo sync --check --verify' once resolved",
			remote.Name, len(report.Failed), len(report.MissingAfter))
	}
	return nil
}
