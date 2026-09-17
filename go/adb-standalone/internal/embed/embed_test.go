package embed_test

import (
	"testing"

	"github.com/Genentech/exohub/go/adb-standalone/internal/embed"
)

// stubEmbedder is a test double that returns a fixed-length vector.
type stubEmbedder struct {
	dims int
}

func (s *stubEmbedder) Embed(_ string) ([]float32, error) {
	vec := make([]float32, s.dims)
	for i := range vec {
		vec[i] = 0.1
	}
	return vec, nil
}

func (s *stubEmbedder) Close() error { return nil }

// Compile-time check that stubEmbedder satisfies the interface.
var _ embed.Embedder = (*stubEmbedder)(nil)

func TestStubEmbedder_ReturnsCorrectDimension(t *testing.T) {
	e := &stubEmbedder{dims: 256}
	vec, err := e.Embed("test text")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 256 {
		t.Errorf("expected 256 dimensions, got %d", len(vec))
	}
}

func TestStubEmbedder_DifferentTextsReturnSameDim(t *testing.T) {
	e := &stubEmbedder{dims: 128}
	texts := []string{"hello world", "BRCA1 gene expression", ""}
	for _, text := range texts {
		vec, err := e.Embed(text)
		if err != nil {
			t.Fatalf("Embed(%q): %v", text, err)
		}
		if len(vec) != 128 {
			t.Errorf("Embed(%q): expected 128 dims, got %d", text, len(vec))
		}
	}
}

func TestStubEmbedder_Close(t *testing.T) {
	e := &stubEmbedder{dims: 64}
	if err := e.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestNewOnnxEmbedder_EmptyModelPath(t *testing.T) {
	_, err := embed.NewOnnxEmbedder("", 256)
	if err == nil {
		t.Error("expected error for empty model_path")
	}
}

func TestNewOnnxEmbedder_InvalidDimensions(t *testing.T) {
	_, err := embed.NewOnnxEmbedder("/some/model.onnx", 0)
	if err == nil {
		t.Error("expected error for dimensions=0")
	}
	_, err = embed.NewOnnxEmbedder("/some/model.onnx", -1)
	if err == nil {
		t.Error("expected error for dimensions=-1")
	}
}

func TestNewOnnxEmbedder_MissingRunnerBinary(t *testing.T) {
	// With no ADB_MODEL_RUNNER set and adb-model-runner not on PATH,
	// NewOnnxEmbedder must return an error — never silently fall back to a stub.
	t.Setenv("ADB_MODEL_RUNNER", "")
	t.Setenv("PATH", "") // ensure PATH lookup fails
	_, err := embed.NewOnnxEmbedder("/some/model.onnx", 256)
	if err == nil {
		t.Error("expected error when runner binary is not found")
	}
}

func TestNewOnnxEmbedder_WithStubRunner(t *testing.T) {
	// Use the test helper that wires the inline stub runner directly.
	e, err := embed.NewOnnxEmbedderWithStub(256)
	if err != nil {
		t.Fatalf("NewOnnxEmbedderWithStub: %v", err)
	}
	defer e.Close()

	vec, err := e.Embed("rnaseq datasets")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 256 {
		t.Errorf("expected 256 dims, got %d", len(vec))
	}
}

func TestNewOnnxEmbedder_DimensionMismatch(t *testing.T) {
	// The stub returns 256 dims; requesting 512 must fail at warmup validation.
	_, err := embed.NewOnnxEmbedderWithStub(512)
	if err == nil {
		t.Error("expected dimension mismatch error when requesting 512 from a 256-dim stub")
	}
}
