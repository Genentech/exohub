// Package embed provides document embedding for optional semantic search.
//
// When semantic.model_path is unset the Embedder is nil; semantic endpoints
// return 404, matching the reference ArtifactDB service behaviour when nuros is disabled.
//
// Embedding model: Qwen3-Embedding (ONNX export), loaded from a filesystem path.
//
// Pooling: last-token pooling (nuros 0.5.1 convention) — the hidden state of the
// final non-padding token is used as the sentence embedding, not mean-pooling.
//
// Runtime choice: github.com/yalue/onnxruntime_go (pure-Go CGo wrapper around the
// ONNX Runtime C library). Chosen over a Python sidecar because it runs in-process
// with no subprocess management, no IPC latency, and produces a single self-contained
// binary. Qwen3-Embedding ONNX exports are compatible.
// The concrete OnnxEmbedder type is provided in embed_onnx.go; the interface
// defined here is what the rest of the application depends on so tests and the
// FTS-only build path can substitute a stub without pulling in CGo.
package embed

// Embedder converts text into a fixed-dimension float32 vector.
type Embedder interface {
	Embed(text string) ([]float32, error)
	Close() error
}
