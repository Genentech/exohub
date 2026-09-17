package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/Genentech/exohub/go/adb-standalone/internal/config"
	"github.com/Genentech/exohub/go/adb-standalone/internal/storage"
)

// filesChunksResponse matches the shape expected by exo download's ChunksResponse.
type filesChunksResponse struct {
	Chunks   []chunkEntry `json:"chunks"`
	Filename string       `json:"filename"`
	Size     int64        `json:"size"`
}

type chunkEntry struct {
	ChunkKey    string `json:"chunk_key,omitempty"`
	ChunkNumber int    `json:"chunk_number"`
	ChunkSize   int64  `json:"chunk_size"`
	S3Path      string `json:"s3_path,omitempty"`
	URL         string `json:"url,omitempty"`
}

// metaRedirectResponse is returned instead of a 307 header when meta_redirect=true.
type metaRedirectResponse struct {
	URL string `json:"url"`
}

// FilesServe handles both GET /files/{fileID} and GET /files/{fileID}?chunks=true.
// It dispatches based on the presence of the "chunks" query parameter.
func FilesServe(db *surrealdb.DB, adapter storage.Adapter, cfg *config.S3Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rawID := chi.URLParam(r, "fileID")
		if rawID == "" {
			writeError(w, http.StatusBadRequest, "missing file id")
			return
		}

		fileID, err := url.PathUnescape(rawID)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid file id encoding: %v", err))
			return
		}

		ctx := r.Context()
		rid := models.NewRecordID("document", fileID)
		res, err := surrealdb.Select[map[string]any](ctx, db, rid)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("db error: %v", err))
			return
		}
		if res == nil {
			writeError(w, http.StatusNotFound, fmt.Sprintf("not found: %s", fileID))
			return
		}
		doc := *res

		bucket, key, fileSize := extractS3Location(doc, fileID)
		if bucket == "" {
			writeError(w, http.StatusInternalServerError, "document has no S3 location")
			return
		}

		expiry := presignExpiry(cfg)

		if r.URL.Query().Get("chunks") == "true" {
			serveChunks(w, r, adapter, cfg, bucket, key, fileID, fileSize, expiry)
			return
		}

		serveRedirect(w, r, adapter, cfg, bucket, key, expiry)
	}
}

// serveRedirect generates a presigned GET URL and either issues a 307 redirect
// or returns a JSON body (when meta_redirect is enabled).
func serveRedirect(w http.ResponseWriter, r *http.Request, adapter storage.Adapter, cfg *config.S3Config, bucket, key string, expiry time.Duration) {
	presigned, err := adapter.PresignGetURL(r.Context(), bucket, key, expiry)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("presign error: %v", err))
		return
	}

	if cfg.MetaRedirect {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(metaRedirectResponse{URL: presigned})
		return
	}

	http.Redirect(w, r, presigned, http.StatusTemporaryRedirect)
}

// serveChunks lists the multipart parts of the S3 object and returns presigned URLs.
func serveChunks(w http.ResponseWriter, r *http.Request, adapter storage.Adapter, cfg *config.S3Config, bucket, key, fileID string, fileSize int64, expiry time.Duration) {
	parts, err := adapter.Parts(r.Context(), bucket, key, expiry)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("list parts error: %v", err))
		return
	}

	chunks := make([]chunkEntry, len(parts))
	var totalSize int64
	for i, p := range parts {
		chunks[i] = chunkEntry{
			ChunkKey:    p.ChunkKey,
			ChunkNumber: p.PartNumber,
			ChunkSize:   p.Size,
			S3Path:      p.S3Path,
			URL:         p.URL,
		}
		totalSize += p.Size
	}
	if fileSize > 0 {
		totalSize = fileSize
	}

	resp := filesChunksResponse{
		Chunks:   chunks,
		Filename: filepath.Base(fileID),
		Size:     totalSize,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// extractS3Location pulls bucket, key and file size from a document's _extra.location.
// The S3 key is derived from the ADB id when not stored explicitly in the location.
func extractS3Location(doc map[string]any, fileID string) (bucket, key string, fileSize int64) {
	extra, _ := doc["_extra"].(map[string]any)
	if extra == nil {
		return "", "", 0
	}

	loc, _ := extra["location"].(map[string]any)
	if loc == nil {
		return "", "", 0
	}

	s3loc, _ := loc["s3"].(map[string]any)
	if s3loc == nil {
		return "", "", 0
	}

	bucket, _ = s3loc["bucket"].(string)
	key, _ = s3loc["key"].(string)
	if key == "" {
		// Derive key from id: "project:path@version" → "project/path"
		key = storage.IDToKey(fileID)
	}

	// Extract file size from _extra.file_size if available.
	if sz, ok := extra["file_size"].(float64); ok {
		fileSize = int64(sz)
	}

	return bucket, key, fileSize
}

// presignExpiry returns the presigned URL expiry duration from config.
func presignExpiry(cfg *config.S3Config) time.Duration {
	secs := cfg.PresignedURLExpiration
	if secs <= 0 {
		secs = 3600
	}
	return time.Duration(secs) * time.Second
}
