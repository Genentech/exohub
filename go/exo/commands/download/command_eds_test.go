//go:build artifactdb

package download

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Genentech/exohub/go/exo/internal/eds"
)

func TestPrintSuccess_WithDestinationAndScope(t *testing.T) {
	out := captureStdout(t, func() {
		printSuccess(eds.Outcome{
			Success:     true,
			Destination: "/data/eds/users/alice/workspace/winery/v0.0.4",
			Scope:       "user",
		})
	})
	if !strings.Contains(out, "/data/eds/users/alice/workspace/winery/v0.0.4") {
		t.Errorf("output missing destination, got: %q", out)
	}
	if !strings.Contains(out, "scope: user") {
		t.Errorf("output missing scope, got: %q", out)
	}
}

func TestPrintSuccess_EmptyDestination(t *testing.T) {
	out := captureStdout(t, func() {
		printSuccess(eds.Outcome{Success: true})
	})
	// Must not print a label with nothing after it.
	if strings.Contains(out, "Downloaded to \n") || strings.HasSuffix(strings.TrimSpace(out), "to") {
		t.Errorf("printed a dangling 'Downloaded to' label, got: %q", out)
	}
	if !strings.Contains(out, "no destination path") {
		t.Errorf("expected an explicit no-destination message, got: %q", out)
	}
}

func TestEdsLiveRenderer_Update_NilIsNoop(t *testing.T) {
	renderer := newEDSLiveRenderer(false, time.Now(), time.Now)
	out := captureStdout(t, func() {
		renderer.Update(nil)
	})
	if out != "" {
		t.Errorf("Update(nil) should print nothing, got: %q", out)
	}
}

func TestEdsLiveRenderer_NonTTY_SuppressesDuplicateLines(t *testing.T) {
	renderer := newEDSLiveRenderer(false, time.Now(), time.Now)
	p := &eds.Progress{Step: "syncing", TS: "t1", FilesSynced: 3, BytesSynced: 1024}

	out := captureStdout(t, func() {
		renderer.Update(p)
		renderer.Update(p)
		renderer.Update(p)
	})

	lines := strings.Count(out, "\n")
	if lines != 1 {
		t.Errorf("expected exactly 1 line for unchanged progress in non-TTY mode, got %d lines: %q", lines, out)
	}
}

func TestEdsLiveRenderer_NonTTY_PrintsWhenValuesChange(t *testing.T) {
	renderer := newEDSLiveRenderer(false, time.Now(), time.Now)

	out := captureStdout(t, func() {
		renderer.Update(&eds.Progress{Step: "cloning", TS: "t1", FilesSynced: 1, BytesSynced: 100})
		renderer.Update(&eds.Progress{Step: "syncing", TS: "t2", FilesSynced: 2, BytesSynced: 200})
	})

	lines := strings.Count(out, "\n")
	if lines != 2 {
		t.Errorf("expected 2 lines when progress changes, got %d lines: %q", lines, out)
	}
}

func TestEdsLiveRenderer_NonTTY_NoCarriageReturn(t *testing.T) {
	renderer := newEDSLiveRenderer(false, time.Now(), time.Now)

	out := captureStdout(t, func() {
		renderer.Update(&eds.Progress{Step: "syncing", TS: "t1", FilesSynced: 1, BytesSynced: 100, BytesTotal: 200, AnnexFilesTotal: 2})
	})

	if strings.Contains(out, "\r") {
		t.Errorf("non-TTY output must not contain \\r, got: %q", out)
	}
}

func TestRunEDSDownload_FinalLineFromCompletedResult(t *testing.T) {
	result := &eds.Result{
		Status:          eds.ResultOK,
		Verified:        true,
		FilesSynced:     1,
		BytesSynced:     60715200,
		AnnexFilesTotal: 1,
		BytesTotal:      60715200,
		Scope:           "user",
		RepoDir:         "/data/eds/users/alice/workspace/cvrm_exohub/main",
	}

	out := captureStdout(t, func() {
		fmt.Println(formatEDSFinalLine(result))
		printSuccess(eds.Classify(&eds.Operation{Status: eds.StatusCompleted, Result: result}))
	})

	if !strings.Contains(out, "100%") {
		t.Errorf("expected 100%% in final output, got: %q", out)
	}
	if !strings.Contains(out, "1/1 files, verified") {
		t.Errorf("expected '1/1 files, verified' in final output, got: %q", out)
	}
	if strings.Count(out, "%") != 1 {
		t.Errorf("expected exactly one percentage, got: %q", out)
	}
}
