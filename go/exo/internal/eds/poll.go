package eds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// DefaultPollInterval is how often PollOperation checks operation status
// when the caller does not override it.
//
// Every poll costs the EDS a Temporal "describe" call. The numbers it
// returns are only as fresh as the workflow's own heartbeats
// (exohub-workers !472): the progress heartbeat fires every 30s, and
// bytes_synced/files_synced only refresh when the metrics scan runs, every
// 120s. Polling faster than ~10s re-reads the same numbers and only
// notices completion a few seconds sooner, which does not matter for
// downloads that take minutes to hours. Do not tune this down without
// revisiting that heartbeat cadence.
const DefaultPollInterval = 10 * time.Second

// ErrCanceled is returned by PollOperation when ctx is canceled (e.g.
// Ctrl-C) before the operation reaches a terminal status. The remote
// download is not affected — it keeps running on the EDS.
var ErrCanceled = errors.New("polling canceled; download continues on the EDS")

// ErrMaxWaitExceeded is returned by PollOperation when maxWait elapses
// before the operation reaches a terminal status.
var ErrMaxWaitExceeded = errors.New("timed out waiting for the operation; download continues on the EDS")

// ValidatePollInterval rejects zero or negative poll intervals.
func ValidatePollInterval(interval time.Duration) error {
	if interval <= 0 {
		return fmt.Errorf("poll interval must be positive, got %s", interval)
	}
	return nil
}

// PollOperation polls an EDS operation until it reaches a terminal status,
// ctx is canceled, or maxWait elapses.
//
// It polls operationURL first: this is the unauthenticated share-link
// returned at submit time (the server embeds an unguessable token in the
// URL itself), which keeps working after the login Bearer token expires —
// important for downloads that outlast the ~1h token lifetime. If
// operationURL fails or 404s, it falls back once to fallbackOperationsURL
// with the Bearer token, and that fallback is then used for the rest of
// the poll loop.
func PollOperation(
	ctx context.Context,
	client *http.Client,
	operationURL, fallbackOperationsURL, bearerToken string,
	interval, maxWait time.Duration,
	progressFn func(*Progress),
	onFallback func(fallbackURL string),
) (*Operation, error) {
	if err := ValidatePollInterval(interval); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(maxWait)
	pollURL := operationURL
	useBearer := false

	for {
		op, status, err := fetchOperation(client, pollURL, bearerToken, useBearer)
		if (err != nil || status == http.StatusNotFound) && !useBearer && fallbackOperationsURL != "" {
			pollURL = fallbackOperationsURL
			useBearer = true
			if onFallback != nil {
				onFallback(pollURL)
			}
			op, _, err = fetchOperation(client, pollURL, bearerToken, useBearer)
		}
		if err != nil {
			return nil, err
		}

		if progressFn != nil {
			progressFn(op.Progress)
		}

		if IsTerminal(op.Status) {
			return op, nil
		}

		if time.Now().After(deadline) {
			return nil, ErrMaxWaitExceeded
		}

		select {
		case <-ctx.Done():
			return nil, ErrCanceled
		case <-time.After(interval):
		}
	}
}

func fetchOperation(client *http.Client, pollURL, bearerToken string, useBearer bool) (*Operation, int, error) {
	req, err := http.NewRequest(http.MethodGet, pollURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("building poll request: %w", err)
	}
	if useBearer {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("polling %s: %w", pollURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("polling %s: unexpected status %d", pollURL, resp.StatusCode)
	}

	var op Operation
	if err := json.NewDecoder(resp.Body).Decode(&op); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("decoding operation from %s: %w", pollURL, err)
	}
	return &op, resp.StatusCode, nil
}
