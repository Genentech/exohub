package eds

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSubmit_Success_ScopeOmitted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok123" {
			t.Errorf("Authorization = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		if _, ok := body["scope"]; ok {
			t.Errorf("scope should be omitted, got body: %v", body)
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(SubmitResponse{
			OperationID:  "op-1",
			OperationURL: "https://eds.example.com/api/operations/op-1?t=abc",
		})
	}))
	defer srv.Close()

	sub, err := Submit(srv.Client(), srv.URL, "tok123", "cvrm_exohub", "v0.0.4", "")
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if sub.OperationID != "op-1" {
		t.Errorf("OperationID = %q", sub.OperationID)
	}
}

func TestSubmit_Success_ScopeIncluded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["scope"] != "global" {
			t.Errorf("scope = %v, want global", body["scope"])
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(SubmitResponse{OperationID: "op-1", OperationURL: "https://x/op-1"})
	}))
	defer srv.Close()

	_, err := Submit(srv.Client(), srv.URL, "tok123", "cvrm_exohub", "v0.0.4", "global")
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
}

func TestSubmit_Forbidden_PassesThroughDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"detail": "not in EXOHUB_USERS group"})
	}))
	defer srv.Close()

	_, err := Submit(srv.Client(), srv.URL, "tok123", "cvrm_exohub", "v0.0.4", "")
	if err == nil {
		t.Fatal("Submit() expected error on 403")
	}
	if !strings.Contains(err.Error(), "not in EXOHUB_USERS group") {
		t.Errorf("error should surface detail, got: %v", err)
	}
}
