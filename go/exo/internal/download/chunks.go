package download

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"

	"golang.org/x/sync/errgroup"
)

const chunkConcurrency = 8

// downloadAndReassembleChunks downloads chunks in parallel and concatenates them.
func downloadAndReassembleChunks(chunks []ChunkInfo, outputPath string, totalSize int64, filename string, progress ProgressFunc) error {
	// Sort chunks by number to ensure correct reassembly order
	sort.Slice(chunks, func(i, j int) bool {
		return chunks[i].ChunkNumber < chunks[j].ChunkNumber
	})

	// Use the output directory for temporary chunk storage so that we avoid
	// filling up /tmp (which can be small on some systems) and stay on the
	// same filesystem as the final file (enabling efficient renames).
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}
	tmpDir, err := os.MkdirTemp(filepath.Dir(outputPath), ".exo-download-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Track total downloaded bytes across all chunks for progress
	var totalDownloaded atomic.Int64

	// Download chunks in parallel
	g, ctx := errgroup.WithContext(context.Background())
	g.SetLimit(chunkConcurrency)

	chunkPaths := make([]string, len(chunks))
	for i, chunk := range chunks {
		chunkPath := filepath.Join(tmpDir, fmt.Sprintf("chunk-%04d", chunk.ChunkNumber))
		chunkPaths[i] = chunkPath

		g.Go(func() error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return downloadChunkToFile(chunk.URL, chunkPath, func(bytesWritten int64) {
				current := totalDownloaded.Add(bytesWritten)
				if progress != nil {
					progress(filename, current, totalSize)
				}
			})
		})
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("downloading chunks: %w", err)
	}

	// Reassemble chunks into final file
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}

	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer out.Close()

	for _, cp := range chunkPaths {
		f, err := os.Open(cp)
		if err != nil {
			return fmt.Errorf("opening chunk %s: %w", cp, err)
		}
		if _, err := io.Copy(out, f); err != nil {
			f.Close()
			return fmt.Errorf("writing chunk %s: %w", cp, err)
		}
		f.Close()
	}

	return nil
}

// chunkProgressFunc reports incremental bytes written for a single chunk.
type chunkProgressFunc func(bytesWritten int64)

// downloadChunkToFile downloads a single chunk from a presigned URL to a file.
func downloadChunkToFile(presignedURL, outputPath string, progress chunkProgressFunc) error {
	resp, err := http.Get(presignedURL)
	if err != nil {
		return fmt.Errorf("downloading chunk: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d downloading chunk: %s", resp.StatusCode, truncate(body, 200))
	}

	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer f.Close()

	if progress == nil {
		_, err = io.Copy(f, resp.Body)
		return err
	}

	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := f.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
			progress(int64(n))
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return readErr
		}
	}

	return nil
}
