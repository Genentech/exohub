package embed

import (
	"fmt"
	"io"
)

// NewOnnxEmbedderWithStub creates an OnnxEmbedder backed by the inline stub
// runner (fixed unit vectors of the given dim). For use in tests only — not
// callable from production code because this file lives in the embed package
// and is not exported via a build tag.
//
// This avoids the need for a real adb-model-runner binary in CI.
func NewOnnxEmbedderWithStub(dimensions int) (*OnnxEmbedder, error) {
	if dimensions <= 0 {
		return nil, fmt.Errorf("dimensions must be positive, got %d", dimensions)
	}

	// Wire an in-process pipe pair to the inline stub runner.
	pr, pw := io.Pipe()
	rr, rw := io.Pipe()

	go inlineStubRunner(pr, rw, 256) // stub always produces 256 dims

	rw2 := &pipeReadWriter{r: rr, w: pw}
	closeFunc := func() error {
		_ = pw.Close()
		_ = rr.Close()
		return nil
	}

	emb := &OnnxEmbedder{runner: rw2, dimensions: dimensions, close: closeFunc}

	// Warmup to validate dimension.
	vec, err := emb.Embed("warmup")
	if err != nil {
		_ = closeFunc()
		return nil, fmt.Errorf("warmup: %w", err)
	}
	if len(vec) != dimensions {
		_ = closeFunc()
		return nil, fmt.Errorf("stub dimension %d does not match requested dimensions %d", len(vec), dimensions)
	}

	return emb, nil
}


