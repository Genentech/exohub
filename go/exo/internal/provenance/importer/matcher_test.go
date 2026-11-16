package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	lib "github.com/Genentech/exohub/go/exo/internal/provenance"
)

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestMatcher_MatchedByChecksum(t *testing.T) {
	root, run := importerTestRepo(t)
	content := "a,b\n1,2\n"
	writeTestFile(t, root, "data/counts.csv", content)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "add")

	reader := lib.NewWorktreeReader(root)
	matcher := NewMatcher(reader)

	f := File{Path: "data/counts.csv", Size: int64(len(content)), Checksum: Checksum{Algorithm: "sha256", Value: sha256Hex(content)}}
	matched := matcher.Match(f)

	wantID := lib.GitBlobEntityID(gitBlobID(t, run, "data/counts.csv"))
	if matched.ID != wantID {
		t.Errorf("ID = %q, want %q", matched.ID, wantID)
	}
	if matched.External {
		t.Error("External = true, want false")
	}
	if matched.Verified != "checksum" {
		t.Errorf("Verified = %q, want checksum", matched.Verified)
	}
	if matched.Unmatched != "" {
		t.Errorf("Unmatched = %q, want empty", matched.Unmatched)
	}
}

func TestMatcher_MatchedBySizeWhenAlgorithmUnsupported(t *testing.T) {
	root, run := importerTestRepo(t)
	content := "fake-parquet-content"
	writeTestFile(t, root, "results/de.parquet", content)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "add")

	reader := lib.NewWorktreeReader(root)
	matcher := NewMatcher(reader)

	f := File{Path: "results/de.parquet", Size: int64(len(content)), Checksum: Checksum{Algorithm: "sha512", Value: "irrelevant"}}
	matched := matcher.Match(f)

	if matched.External {
		t.Error("External = true, want false")
	}
	if matched.Verified != "size" {
		t.Errorf("Verified = %q, want size", matched.Verified)
	}
}

func TestMatcher_ChangedFileIsNotMatched(t *testing.T) {
	root, run := importerTestRepo(t)
	original := "version one"
	writeTestFile(t, root, "results/de.csv", original)
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "v1")

	writeTestFile(t, root, "results/de.csv", "version two, changed after the run")
	run("git", "add", ".")
	run("git", "commit", "-q", "-m", "v2")

	reader := lib.NewWorktreeReader(root)
	matcher := NewMatcher(reader)

	f := File{Path: "results/de.csv", Size: int64(len(original)), Checksum: Checksum{Algorithm: "sha256", Value: sha256Hex(original)}}
	matched := matcher.Match(f)

	if !matched.External {
		t.Error("External = false, want true")
	}
	if matched.Unmatched != "content differs from the run" {
		t.Errorf("Unmatched = %q, want %q", matched.Unmatched, "content differs from the run")
	}
	if matched.ID != "exo-path:results/de.csv" {
		t.Errorf("ID = %q, want exo-path:results/de.csv", matched.ID)
	}
}

func TestMatcher_UntrackedFile(t *testing.T) {
	root, _ := importerTestRepo(t)
	writeTestFile(t, root, "results/new.csv", "untracked")

	reader := lib.NewWorktreeReader(root)
	matcher := NewMatcher(reader)

	matched := matcher.Match(File{Path: "results/new.csv"})
	if !matched.External {
		t.Error("External = false, want true")
	}
	if matched.Unmatched == "" {
		t.Error("Unmatched is empty, want an untracked hint")
	}
}

func TestMatcher_ExternalInput(t *testing.T) {
	root, _ := importerTestRepo(t)
	reader := lib.NewWorktreeReader(root)
	matcher := NewMatcher(reader)

	matched := matcher.Match(File{URL: "s3://refs/genome.fa"})
	if !matched.External {
		t.Error("External = false, want true")
	}
	if matched.ID != "s3://refs/genome.fa" {
		t.Errorf("ID = %q, want the URL", matched.ID)
	}
	if matched.Unmatched != "" {
		t.Errorf("Unmatched = %q, want empty", matched.Unmatched)
	}
}

func TestParseAnnexKey(t *testing.T) {
	sha256Key := "SHA256E-s2048--" + sha256Hex("content") + ".csv"
	info, ok := parseAnnexKey(sha256Key)
	if !ok {
		t.Fatalf("parseAnnexKey(%q) ok = false", sha256Key)
	}
	if !info.hasSize || info.size != 2048 {
		t.Errorf("size = %v (hasSize=%v), want 2048", info.size, info.hasSize)
	}
	if info.algo != "sha256" || info.hex != sha256Hex("content") {
		t.Errorf("algo/hex = %q/%q, want sha256/%q", info.algo, info.hex, sha256Hex("content"))
	}

	md5Key := "MD5-s10--0123456789abcdef0123456789abcdef"
	info, ok = parseAnnexKey(md5Key)
	if !ok {
		t.Fatalf("parseAnnexKey(%q) ok = false", md5Key)
	}
	if info.algo != "md5" || info.hex != "0123456789abcdef0123456789abcdef" {
		t.Errorf("algo/hex = %q/%q, want md5/0123456789abcdef0123456789abcdef", info.algo, info.hex)
	}

	wormKey := "WORM-s5--some-name"
	info, ok = parseAnnexKey(wormKey)
	if !ok {
		t.Fatalf("parseAnnexKey(%q) ok = false", wormKey)
	}
	if info.algo != "" {
		t.Errorf("algo = %q, want empty for a non-hashing backend", info.algo)
	}
	if !info.hasSize || info.size != 5 {
		t.Errorf("size = %v (hasSize=%v), want 5", info.size, info.hasSize)
	}
}
