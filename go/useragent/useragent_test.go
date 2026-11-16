package useragent

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"testing"
	"time"
)

func TestString_Default(t *testing.T) {
	os.Unsetenv("EXO_USER_AGENT")
	SetVersion("1.2.3")
	defer SetVersion("dev")

	want := fmt.Sprintf("exo/1.2.3 (%s/%s)", runtime.GOOS, runtime.GOARCH)
	if got := String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestString_EnvOverride(t *testing.T) {
	os.Setenv("EXO_USER_AGENT", "custom-agent/9.9")
	defer os.Unsetenv("EXO_USER_AGENT")
	SetVersion("1.2.3")
	defer SetVersion("dev")

	if got := String(); got != "custom-agent/9.9" {
		t.Errorf("String() = %q, want %q", got, "custom-agent/9.9")
	}
}

func TestAppID(t *testing.T) {
	SetVersion("1.2.3")
	defer SetVersion("dev")

	if got, want := AppID(), "exo-1.2.3"; got != want {
		t.Errorf("AppID() = %q, want %q", got, want)
	}
}

func TestAppID_IgnoresUserAgentOverride(t *testing.T) {
	os.Setenv("EXO_USER_AGENT", "custom-agent/9.9")
	defer os.Unsetenv("EXO_USER_AGENT")
	SetVersion("1.2.3")
	defer SetVersion("dev")

	// AppID must stay a short SDK-legal token, independent of EXO_USER_AGENT.
	if got, want := AppID(), "exo-1.2.3"; got != want {
		t.Errorf("AppID() = %q, want %q", got, want)
	}
}

func TestTransport_NilBaseDoesNotRecurseAfterInstall(t *testing.T) {
	// Regression test: once Install wraps http.DefaultTransport, a
	// Base-less Transport must not delegate to the live
	// http.DefaultTransport (which would now be itself), or RoundTrip
	// recurses forever. It must fall back to the pre-Install transport.
	origDefault := http.DefaultTransport
	defer func() { http.DefaultTransport = origDefault }()

	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
	}))
	defer srv.Close()

	os.Unsetenv("EXO_USER_AGENT")
	SetVersion("7.8.9")
	defer SetVersion("dev")

	Install("7.8.9")

	done := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Get(srv.URL)
		if err == nil {
			resp.Body.Close()
		}
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("GET failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request hung — Transport.RoundTrip recursed into itself via http.DefaultTransport")
	}

	want := fmt.Sprintf("exo/7.8.9 (%s/%s)", runtime.GOOS, runtime.GOARCH)
	if gotUA != want {
		t.Errorf("User-Agent = %q, want %q", gotUA, want)
	}
}

func TestInstall_Idempotent(t *testing.T) {
	origDefault := http.DefaultTransport
	defer func() { http.DefaultTransport = origDefault }()

	Install("1.0.0")
	first := http.DefaultTransport

	Install("2.0.0")
	second := http.DefaultTransport

	if first != second {
		t.Error("Install wrapped http.DefaultTransport twice instead of being idempotent")
	}
	if _, ok := second.(*Transport); !ok {
		t.Fatalf("http.DefaultTransport is %T, want *Transport", second)
	}
	// SetVersion must still apply on the second call.
	if got, want := version, "2.0.0"; got != want {
		t.Errorf("version = %q, want %q", got, want)
	}
}

func TestInstall_DefaultClientCarriesHeader(t *testing.T) {
	origDefault := http.DefaultTransport
	defer func() { http.DefaultTransport = origDefault }()

	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
	}))
	defer srv.Close()

	os.Unsetenv("EXO_USER_AGENT")
	Install("3.2.1")

	resp, err := http.DefaultClient.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	resp.Body.Close()

	want := fmt.Sprintf("exo/3.2.1 (%s/%s)", runtime.GOOS, runtime.GOARCH)
	if gotUA != want {
		t.Errorf("User-Agent = %q, want %q", gotUA, want)
	}
}

func TestTransport_SetsHeader(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
	}))
	defer srv.Close()

	client := &http.Client{Transport: &Transport{}}

	t.Run("default", func(t *testing.T) {
		os.Unsetenv("EXO_USER_AGENT")
		SetVersion("4.5.6")
		defer SetVersion("dev")

		resp, err := client.Get(srv.URL)
		if err != nil {
			t.Fatalf("GET failed: %v", err)
		}
		resp.Body.Close()

		want := fmt.Sprintf("exo/4.5.6 (%s/%s)", runtime.GOOS, runtime.GOARCH)
		if gotUA != want {
			t.Errorf("User-Agent = %q, want %q", gotUA, want)
		}
	})

	t.Run("env override", func(t *testing.T) {
		os.Setenv("EXO_USER_AGENT", "override/1.0")
		defer os.Unsetenv("EXO_USER_AGENT")
		SetVersion("4.5.6")
		defer SetVersion("dev")

		resp, err := client.Get(srv.URL)
		if err != nil {
			t.Fatalf("GET failed: %v", err)
		}
		resp.Body.Close()

		if gotUA != "override/1.0" {
			t.Errorf("User-Agent = %q, want %q", gotUA, "override/1.0")
		}
	})
}
