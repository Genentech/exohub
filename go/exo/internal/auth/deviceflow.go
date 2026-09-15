// Package auth — deviceflow.go defines the split device-flow API shared by
// both auth provider implementations for callers (e.g. serve) that need the
// verification URI before polling completes.
package auth

import "context"

// DeviceAuthParams carries the user-facing parameters of a started device flow.
type DeviceAuthParams struct {
	// VerificationURI is where the user should enter the UserCode (or the
	// full URI with the code already embedded).
	VerificationURI string
	// UserCode is the short code the user enters at VerificationURI.
	UserCode string
}

// PollFn is the polling closure returned by StartDeviceAuthFlow.
// It blocks until the user completes authentication or the context is cancelled.
type PollFn func(ctx context.Context) (Tokens, error)

// DeviceAuthFlow groups the results of StartDeviceAuthFlow so the Provider
// and the PollFn share the same underlying connection/discovery state —
// no second discovery round-trip is needed for FetchAWSCredentials.
type DeviceAuthFlow struct {
	Params   DeviceAuthParams
	Poll     PollFn
	Provider Provider
}

// StartDeviceAuthFlow initiates the device authorization request and returns
// a DeviceAuthFlow whose Provider is already constructed from the same
// discovery result used to start the flow. Callers must use flow.Provider
// for any subsequent FetchAWSCredentials call instead of calling auth.New
// again.
//
// Exactly one of the build-tag-specific files (flow_oidc.go or the
// internal-build equivalent) provides the implementation via startDeviceAuthFlow.
func StartDeviceAuthFlow(ctx context.Context, cfg Config) (DeviceAuthFlow, error) {
	return startDeviceAuthFlow(ctx, cfg)
}
