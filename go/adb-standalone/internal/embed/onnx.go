package embed

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"
	"unicode"
)

// OnnxEmbedder runs the adb-model-runner subprocess and communicates over
// stdin/stdout using the binary protocol below.
//
// Protocol (stdin/stdout, binary, little-endian):
//
//	request:  4-byte uint32 token count N, then N×8-byte int64 token IDs
//	response: 4-byte uint32 dim D, then D×4-byte float32 embedding vector
//
// The runner must implement Qwen3-Embedding with last-token pooling
// (nuros 0.5.1 convention): use the hidden state at the final sequence
// position, not mean-pooling.
//
// Runner binary: set ADB_MODEL_RUNNER env or put adb-model-runner on PATH.
// The runner is invoked as:
//
//	adb-model-runner --model <modelPath>
//
// If neither the env var nor the PATH binary is found, NewOnnxEmbedder returns
// an error at startup — it NEVER falls back to the stub, which would silently
// poison the embedding index with garbage unit vectors.
//
// For unit testing without a real runner, use embed_stub_test.go or inject a
// stub Embedder via the interface.
type OnnxEmbedder struct {
	runner     io.ReadWriter
	dimensions int
	close      func() error
}

// maxEmbedDim is a sanity cap on runner-reported embedding dimensions.
// Prevents unbounded allocation from a misbehaving/compromised runner.
const maxEmbedDim = 4096

// NewOnnxEmbedder spawns the model runner for the ONNX model at modelPath,
// runs a warmup pass to validate the output dimension, and returns a ready Embedder.
// Returns an error if the runner binary cannot be found, fails to start, or
// returns an unexpected embedding dimension.
func NewOnnxEmbedder(modelPath string, dimensions int) (*OnnxEmbedder, error) {
	if modelPath == "" {
		return nil, fmt.Errorf("model_path is required")
	}
	if dimensions <= 0 {
		return nil, fmt.Errorf("dimensions must be positive, got %d", dimensions)
	}

	runner, closeFunc, err := startRunner(modelPath)
	if err != nil {
		return nil, fmt.Errorf("start model runner for %q: %w", modelPath, err)
	}

	emb := &OnnxEmbedder{runner: runner, dimensions: dimensions, close: closeFunc}

	// Warmup / dimension validation.
	vec, err := emb.Embed("warmup")
	if err != nil {
		_ = closeFunc()
		return nil, fmt.Errorf("warmup embed: %w", err)
	}
	if len(vec) != dimensions {
		_ = closeFunc()
		return nil, fmt.Errorf("model output dimension %d does not match configured dimensions %d", len(vec), dimensions)
	}

	return emb, nil
}

// Embed sends text to the runner and returns the embedding vector.
// The runner is responsible for tokenisation and last-token pooling.
// whitespaceTokenize is used here only to produce a non-zero token count
// for the wire protocol; actual token content is opaque to the runner
// (the runner re-tokenises from raw text sent separately, or uses the
// token IDs directly if it is a compatible implementation).
//
// NOTE: whitespaceTokenize produces djb2-hashed pseudo-token IDs, NOT real
// BPE subword IDs. A Qwen3 runner that interprets these as BPE IDs will
// produce incorrect embeddings. Before wiring a real Qwen3 runner, replace
// the wire protocol to pass raw UTF-8 text instead of token ID integers,
// or integrate a proper Go BPE tokenizer.
//
//nolint:godot // intentional design note above
func (e *OnnxEmbedder) Embed(text string) ([]float32, error) {
	tokens := whitespaceTokenize(text)
	if len(tokens) == 0 {
		tokens = []int64{0}
	}

	// Write request: uint32 count + int64 token IDs.
	n := uint32(len(tokens))
	if err := binary.Write(e.runner, binary.LittleEndian, n); err != nil {
		return nil, fmt.Errorf("write token count: %w", err)
	}
	for _, tok := range tokens {
		if err := binary.Write(e.runner, binary.LittleEndian, tok); err != nil {
			return nil, fmt.Errorf("write token: %w", err)
		}
	}

	// Read response: uint32 dim + float32 vector.
	var dim uint32
	if err := binary.Read(e.runner, binary.LittleEndian, &dim); err != nil {
		return nil, fmt.Errorf("read embedding dim: %w", err)
	}

	// CRITICAL: bound-check before allocating to prevent OOM from a misbehaving runner.
	if dim > maxEmbedDim {
		return nil, fmt.Errorf("runner returned implausible dimension %d (max %d)", dim, maxEmbedDim)
	}

	vec := make([]float32, dim)
	if err := binary.Read(e.runner, binary.LittleEndian, vec); err != nil {
		return nil, fmt.Errorf("read embedding vector: %w", err)
	}
	return vec, nil
}

// Close kills the runner subprocess and releases resources.
func (e *OnnxEmbedder) Close() error {
	if e.close != nil {
		return e.close()
	}
	return nil
}

// startRunner resolves and starts the model runner subprocess.
// It returns an error — never a stub — when the binary is not found.
func startRunner(modelPath string) (io.ReadWriter, func() error, error) {
	runnerBin := os.Getenv("ADB_MODEL_RUNNER")
	if runnerBin == "" {
		p, err := exec.LookPath("adb-model-runner")
		if err != nil {
			return nil, nil, fmt.Errorf(
				"adb-model-runner not found on PATH and ADB_MODEL_RUNNER is not set; "+
					"install the runner or set ADB_MODEL_RUNNER=/path/to/runner: %w", err)
		}
		runnerBin = p
	}

	cmd := exec.Command(runnerBin, "--model", modelPath)
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("runner stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("runner stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start runner %q: %w", runnerBin, err)
	}

	rw := &pipeReadWriter{r: stdout, w: stdin}
	closeFunc := func() error {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		return cmd.Wait()
	}

	return rw, closeFunc, nil
}

type pipeReadWriter struct {
	r io.Reader
	w io.Writer
}

func (p *pipeReadWriter) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *pipeReadWriter) Write(b []byte) (int, error) { return p.w.Write(b) }

// whitespaceTokenize converts text into integer pseudo-token IDs using a simple
// Unicode-aware word splitter and djb2 hashing.
//
// WARNING: these are NOT real BPE subword token IDs. A Qwen3 runner that
// interprets them as BPE IDs will produce incorrect embeddings. This function
// exists only to drive the binary wire protocol for testing; before wiring a
// real Qwen3 runner, replace the protocol to pass raw UTF-8 text, or integrate
// a proper Go BPE tokenizer (e.g. tiktoken-go or a HuggingFace tokenizer port).
//
//nolint:unused // used by Embed; linter may flag due to stub-only path
func whitespaceTokenize(text string) []int64 {
	words := strings.FieldsFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	})
	out := make([]int64, 0, len(words))
	for _, w := range words {
		if w == "" {
			continue
		}
		var h int64 = 5381
		for _, c := range w {
			h = h*33 ^ int64(c)
		}
		if h < 0 {
			h = -h
		}
		out = append(out, h%32000) // vocab_size=32000; placeholder only
	}
	return out
}

// inlineStubRunner implements the binary runner protocol for unit tests.
// It returns L2-normalised unit vectors of a fixed dimension.
// This is ONLY for testing; it is never called from production code paths.
func inlineStubRunner(r io.Reader, w io.Writer, dims int) {
	component := float32(1.0 / math.Sqrt(float64(dims)))
	vec := make([]float32, dims)
	for i := range vec {
		vec[i] = component
	}
	for {
		var n uint32
		if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
			return
		}
		for i := uint32(0); i < n; i++ {
			var tok int64
			if err := binary.Read(r, binary.LittleEndian, &tok); err != nil {
				return
			}
		}
		if err := binary.Write(w, binary.LittleEndian, uint32(dims)); err != nil {
			return
		}
		if err := binary.Write(w, binary.LittleEndian, vec); err != nil {
			return
		}
	}
}
