// Package storage defines the pluggable storage adapter interface used by the
// adb-standalone file-serving endpoints.
package storage

import (
	"context"
	"time"
)

// Part describes one chunk of a multipart-uploaded S3 object.
type Part struct {
	PartNumber int
	Size       int64
	URL        string // presigned GET URL for this part range
	S3Path     string // bucket/key for this part
	ChunkKey   string // S3 part ETag or upload key
}

// Adapter is the interface that storage backends must implement.
type Adapter interface {
	// PresignGetURL generates a presigned GET URL for the object at bucket/key.
	PresignGetURL(ctx context.Context, bucket, key string, expiry time.Duration) (string, error)

	// Parts returns the individual parts of a multipart-uploaded object as
	// presigned GET range URLs. If the object was not uploaded via multipart,
	// a single Part covering the whole object is returned.
	Parts(ctx context.Context, bucket, key string, expiry time.Duration) ([]Part, error)
}
