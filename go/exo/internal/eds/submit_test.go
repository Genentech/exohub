package eds

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func ptrBool(b bool) *bool { return &b }

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

	sub, err := Submit(srv.Client(), srv.URL, "tok123", SubmitRequest{ProjectID: "cvrm_exohub", Version: "v0.0.4"})
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

	_, err := Submit(srv.Client(), srv.URL, "tok123", SubmitRequest{ProjectID: "cvrm_exohub", Version: "v0.0.4", Scope: "global"})
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

	_, err := Submit(srv.Client(), srv.URL, "tok123", SubmitRequest{ProjectID: "cvrm_exohub", Version: "v0.0.4"})
	if err == nil {
		t.Fatal("Submit() expected error on 403")
	}
	if !strings.Contains(err.Error(), "not in EXOHUB_USERS group") {
		t.Errorf("error should surface detail, got: %v", err)
	}
}

func TestSubmit_OnlySetFieldsAreSent(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(SubmitResponse{OperationID: "op-1", OperationURL: "https://x/op-1"})
	}))
	defer srv.Close()

	_, err := Submit(srv.Client(), srv.URL, "tok123", SubmitRequest{ProjectID: "cvrm_exohub", Version: "v0.0.4"})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	want := map[string]any{"project_id": "cvrm_exohub", "version": "v0.0.4"}
	if len(gotBody) != len(want) {
		t.Fatalf("body = %v, want exactly %v", gotBody, want)
	}
	for k, v := range want {
		if gotBody[k] != v {
			t.Errorf("body[%q] = %v, want %v", k, gotBody[k], v)
		}
	}
}

func TestSubmit_TypedOptionFields(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(SubmitResponse{OperationID: "op-1", OperationURL: "https://x/op-1"})
	}))
	defer srv.Close()

	_, err := Submit(srv.Client(), srv.URL, "tok123", SubmitRequest{
		ProjectID: "cvrm_exohub",
		Version:   "v0.0.4",
		Content:   ptrBool(false),
		Worktrees: ptrBool(false),
		Checkout:  ptrBool(true),
		Force:     ptrBool(true),
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if gotBody["content"] != false {
		t.Errorf("content = %v, want false", gotBody["content"])
	}
	if gotBody["worktrees"] != false {
		t.Errorf("worktrees = %v, want false", gotBody["worktrees"])
	}
	if gotBody["checkout"] != true {
		t.Errorf("checkout = %v, want true", gotBody["checkout"])
	}
	if gotBody["force"] != true {
		t.Errorf("force = %v, want true", gotBody["force"])
	}
}

func TestSubmit_ExtraMergedIntoBody(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(SubmitResponse{OperationID: "op-1", OperationURL: "https://x/op-1"})
	}))
	defer srv.Close()

	_, err := Submit(srv.Client(), srv.URL, "tok123", SubmitRequest{
		ProjectID: "cvrm_exohub",
		Version:   "v0.0.4",
		Extra:     map[string]any{"future_option": "value"},
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if gotBody["future_option"] != "value" {
		t.Errorf("future_option = %v, want %q", gotBody["future_option"], "value")
	}
}

func TestSubmit_422_RendersFastAPIDetailList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"detail": []map[string]any{
				{"loc": []any{"body", "content"}, "msg": "Input should be a valid boolean", "type": "bool_parsing"},
				{"loc": []any{"body", "foo"}, "msg": "Extra inputs are not permitted", "type": "extra_forbidden"},
			},
		})
	}))
	defer srv.Close()

	_, err := Submit(srv.Client(), srv.URL, "tok123", SubmitRequest{ProjectID: "cvrm_exohub"})
	if err == nil {
		t.Fatal("Submit() expected error on 422")
	}
	if !strings.Contains(err.Error(), "body.content: Input should be a valid boolean") {
		t.Errorf("error missing content validation line, got: %v", err)
	}
	if !strings.Contains(err.Error(), "body.foo: Extra inputs are not permitted") {
		t.Errorf("error missing extra-forbidden line, got: %v", err)
	}
}
