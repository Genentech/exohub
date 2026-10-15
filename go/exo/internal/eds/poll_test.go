package eds

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPollOperation_CompletedOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("operation_url poll must not require Bearer, got Authorization: %q", got)
		}
		_ = json.NewEncoder(w).Encode(Operation{
			Status: StatusCompleted,
			Result: &Result{Status: ResultOK, RepoDir: "/data/eds/cvrm_exohub"},
		})
	}))
	defer srv.Close()

	op, err := PollOperation(context.Background(), srv.Client(), srv.URL, "", "tok", time.Millisecond, time.Second, nil, nil)
	if err != nil {
		t.Fatalf("PollOperation() error = %v", err)
	}
	outcome := Classify(op)
	if !outcome.Success || outcome.Destination != "/data/eds/cvrm_exohub" {
		t.Errorf("outcome = %+v", outcome)
	}
}

func TestPollOperation_Degraded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Operation{
			Status: StatusCompleted,
			Result: &Result{Status: ResultDegraded, MissingFiles: []string{"a.txt", "b.txt"}},
		})
	}))
	defer srv.Close()

	op, err := PollOperation(context.Background(), srv.Client(), srv.URL, "", "tok", time.Millisecond, time.Second, nil, nil)
	if err != nil {
		t.Fatalf("PollOperation() error = %v", err)
	}
	outcome := Classify(op)
	if outcome.Success {
		t.Error("degraded completion must not be Success")
	}
	if len(outcome.MissingFiles) != 2 {
		t.Errorf("MissingFiles = %v", outcome.MissingFiles)
	}
}

func TestPollOperation_Failed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Operation{Status: StatusFailed, Reason: "disk full"})
	}))
	defer srv.Close()

	op, err := PollOperation(context.Background(), srv.Client(), srv.URL, "", "tok", time.Millisecond, time.Second, nil, nil)
	if err != nil {
		t.Fatalf("PollOperation() error = %v", err)
	}
	outcome := Classify(op)
	if outcome.Success || outcome.Message != "disk full" {
		t.Errorf("outcome = %+v", outcome)
	}
}

func TestPollOperation_CanceledStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Operation{Status: StatusCanceled, Message: "user aborted"})
	}))
	defer srv.Close()

	op, err := PollOperation(context.Background(), srv.Client(), srv.URL, "", "tok", time.Millisecond, time.Second, nil, nil)
	if err != nil {
		t.Fatalf("PollOperation() error = %v", err)
	}
	outcome := Classify(op)
	if outcome.Success || outcome.Message != "user aborted" {
		t.Errorf("outcome = %+v", outcome)
	}
}

func TestPollOperation_FallsBackOn404(t *testing.T) {
	var fellBack bool
	opURL := "http://127.0.0.1:1/does-not-exist" // unreachable, forces fallback

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("fallback poll should use Bearer, got: %q", got)
		}
		_ = json.NewEncoder(w).Encode(Operation{
			Status: StatusCompleted,
			Result: &Result{Status: ResultOK, RepoDir: "/data/x"},
		})
	}))
	defer fallback.Close()

	op, err := PollOperation(
		context.Background(), fallback.Client(), opURL, fallback.URL, "tok",
		time.Millisecond, 2*time.Second, nil,
		func(url string) { fellBack = true },
	)
	if err != nil {
		t.Fatalf("PollOperation() error = %v", err)
	}
	if !fellBack {
		t.Error("expected onFallback to be called")
	}
	if op.Status != StatusCompleted {
		t.Errorf("op.Status = %q", op.Status)
	}
}

func TestPollOperation_MaxWaitExceeded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Operation{Status: "running"})
	}))
	defer srv.Close()

	_, err := PollOperation(context.Background(), srv.Client(), srv.URL, "", "tok", 10*time.Millisecond, 30*time.Millisecond, nil, nil)
	if err != ErrMaxWaitExceeded {
		t.Errorf("PollOperation() error = %v, want ErrMaxWaitExceeded", err)
	}
}

func TestPollOperation_ContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Operation{Status: "running"})
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := PollOperation(ctx, srv.Client(), srv.URL, "", "tok", time.Millisecond, time.Second, nil, nil)
	if err != ErrCanceled {
		t.Errorf("PollOperation() error = %v, want ErrCanceled", err)
	}
}

func TestPollOperation_RejectsNonPositiveInterval(t *testing.T) {
	_, err := PollOperation(context.Background(), http.DefaultClient, "http://example.com", "", "tok", 0, time.Second, nil, nil)
	if err == nil {
		t.Fatal("PollOperation() expected error for zero interval")
	}

	_, err = PollOperation(context.Background(), http.DefaultClient, "http://example.com", "", "tok", -time.Second, time.Second, nil, nil)
	if err == nil {
		t.Fatal("PollOperation() expected error for negative interval")
	}
}

func TestPollOperation_404OperationsFallback(t *testing.T) {
	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer notFound.Close()

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Operation{Status: StatusCompleted, Result: &Result{Status: ResultOK}})
	}))
	defer fallback.Close()

	op, err := PollOperation(context.Background(), notFound.Client(), notFound.URL, fallback.URL, "tok", time.Millisecond, time.Second, nil, nil)
	if err != nil {
		t.Fatalf("PollOperation() error = %v", err)
	}
	if op.Status != StatusCompleted {
		t.Errorf("op.Status = %q", op.Status)
	}
}
