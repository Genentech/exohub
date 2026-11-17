// Package useragent provides a shared User-Agent string and HTTP transport
// for exo's binaries, so every outgoing HTTP request identifies itself as
// exo/<version> (<os>/<arch>), or the value of EXO_USER_AGENT when set.
package useragent

import (
	"fmt"
	"net/http"
	"os"
	"runtime"
)

var version = "dev"

// defaultBase captures http.DefaultTransport at package init time, before
// Install can wrap it. Transport.RoundTrip falls back to this (never to
// http.DefaultTransport directly) when Base is nil, so wrapping
// http.DefaultTransport with a Base-less Transport cannot recurse into
// itself.
var defaultBase = http.DefaultTransport

// SetVersion records the binary's version for use in the default User-Agent
// string. Call once at process startup with the build-time version.
func SetVersion(v string) {
	version = v
}

// String returns the User-Agent value to send on outgoing requests:
// EXO_USER_AGENT's value when set, otherwise exo/<version> (<os>/<arch>).
func String() string {
	if v := os.Getenv("EXO_USER_AGENT"); v != "" {
		return v
	}
	return fmt.Sprintf("exo/%s (%s/%s)", version, runtime.GOOS, runtime.GOARCH)
}

// Transport is an http.RoundTripper that sets the User-Agent header on every
// request before delegating to Base. A nil Base delegates to the transport
// http.DefaultTransport pointed at before Install ran (see defaultBase),
// never to the live http.DefaultTransport, so wrapping
// http.DefaultTransport with a Base-less Transport cannot recurse.
type Transport struct {
	Base http.RoundTripper
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = defaultBase
	}
	req = req.Clone(req.Context())
	req.Header.Set("User-Agent", String())
	return base.RoundTrip(req)
}

// Install records version and, unless http.DefaultTransport is already a
// *Transport (idempotent — safe to call more than once), wraps
// http.DefaultTransport with a Transport that sets the User-Agent header on
// every request made through it or through http.DefaultClient.
func Install(v string) {
	SetVersion(v)
	if _, ok := http.DefaultTransport.(*Transport); ok {
		return
	}
	http.DefaultTransport = &Transport{Base: http.DefaultTransport}
}

// AppID returns the identifier to pass to the AWS SDK v2's
// config.WithAppID / awsconfig.WithAppID, so aws-sdk-go-v2 clients — which
// use their own HTTP transport and so never see Transport's header — still
// identify themselves as an "app/exo-<version>" token in the SDK's own
// User-Agent string. EXO_USER_AGENT is not applied here: AppID must be a
// short SDK-legal token (no spaces or parens), unlike a free-form
// User-Agent override.
func AppID() string {
	return "exo-" + version
}
