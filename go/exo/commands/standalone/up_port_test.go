package standalone

import (
	"strings"
	"testing"
)

// TestVersitygwArgs guards the argv we hand to the managed versitygw process —
// most importantly the --port listen-address format.
//
// versitygw's --port takes an ADDRESS ("<ip>:<port>" or ":<port>"), not a bare
// port number. Passing a bare number fails in two different — and both
// user-hostile — ways, verified against the real upstream binaries:
//
//	v1.0.x: exits immediately with "missing port in address"
//	v1.8.x: starts successfully but binds NOTHING (silent failure; the stack
//	        then fails later with confusing S3 connection errors)
func TestVersitygwArgs(t *testing.T) {
	args := versitygwArgs("mykey", "mysecret", 9100, "/data/objects")

	want := []string{
		"--access=mykey",
		"--secret=mysecret",
		"--port=:9100",
		"posix",
		"/data/objects",
	}
	if len(args) != len(want) {
		t.Fatalf("got %d args %v, want %d %v", len(args), args, len(want), want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("arg[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

// TestVersitygwPortIsAddressForm is the focused regression guard: the port must
// never be emitted as a bare number.
func TestVersitygwPortIsAddressForm(t *testing.T) {
	for _, port := range []int{9100, 7070, 1} {
		args := versitygwArgs("k", "s", port, "/d")
		var portArg string
		for _, a := range args {
			if strings.HasPrefix(a, "--port=") {
				portArg = a
			}
		}
		if portArg == "" {
			t.Fatalf("port %d: no --port arg emitted", port)
		}
		val := strings.TrimPrefix(portArg, "--port=")
		if !strings.HasPrefix(val, ":") {
			t.Errorf("port %d: --port value %q must be an address (\":<port>\"), not a bare number", port, val)
		}
	}
}

// TestVersitygwUsesPosixBackend pins the storage backend subcommand.
func TestVersitygwUsesPosixBackend(t *testing.T) {
	args := versitygwArgs("k", "s", 9100, "/data")
	if args[len(args)-2] != "posix" || args[len(args)-1] != "/data" {
		t.Errorf("expected trailing \"posix <dataDir>\", got %v", args[len(args)-2:])
	}
}
