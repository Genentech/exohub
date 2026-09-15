//go:build exohubapi

package serve

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/oauth2"

	"github.com/Genentech/exohub/go/exo/internal/auth"
)

const authPollTimeout = 2 * time.Minute

// AuthResult holds the outcome of a device flow authentication.
type AuthResult struct {
	Token    *oauth2.Token
	Username string
}

// StartDeviceFlow initiates an OIDC device flow. It returns the verification URL
// and user code immediately (device auth request runs synchronously), then polls
// for completion in a background goroutine.
//
// The caller must read exactly one value from either resultCh or errCh.
func StartDeviceFlow() (authURL string, userCode string, resultCh <-chan AuthResult, errCh <-chan error, err error) {
	cfg := auth.LoadConfig()

	flow, err := auth.StartDeviceAuthFlow(context.Background(), cfg)
	if err != nil {
		return "", "", nil, nil, fmt.Errorf("failed to start device auth: %w", err)
	}

	rCh := make(chan AuthResult, 1)
	eCh := make(chan error, 1)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), authPollTimeout)
		defer cancel()

		tokens, pollErr := flow.Poll(ctx)
		if pollErr != nil {
			if ctx.Err() == context.DeadlineExceeded {
				eCh <- fmt.Errorf("authentication timed out after %v", authPollTimeout)
			} else {
				eCh <- fmt.Errorf("authentication failed: %w", pollErr)
			}
			return
		}

		oauthToken := &oauth2.Token{
			AccessToken:  tokens.AccessToken,
			RefreshToken: tokens.RefreshToken,
			Expiry:       tokens.Expiry,
		}

		rCh <- AuthResult{
			Token:    oauthToken,
			Username: auth.UsernameFromToken(tokens.AccessToken),
		}
	}()

	return flow.Params.VerificationURI, flow.Params.UserCode, rCh, eCh, nil
}
