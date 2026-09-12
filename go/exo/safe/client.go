// Package safe provides the ExoSafe credential store client.
// It communicates with the ExoHub API to manage user credentials
// (SSH keys, git tokens) backed by AWS Parameter Store.
package safe

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Genentech/exohub/go/exo/internal/defaults"
)

const (
	MaxSecretSize = 4096 // 4KB — Parameter Store Standard tier limit

	// Entry types
	TypeSSHKey = "ssh-key"
	TypeToken  = "token"
)

func debugHTTP(method, url string) {
	if os.Getenv("EXOHUB_CLI_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "DEBUG HTTP: %s %s\n", method, url)
	}
}

// safeKey encodes a name for use in API URL paths.
// Replaces characters not allowed in SSM Parameter Store paths.
func safeKey(name string) string {
	return strings.ReplaceAll(name, ":", "_")
}

// Entry represents a single safe entry returned by the API.
type Entry struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value,omitempty"`
}

// ListEntry represents a safe entry in list responses (no value).
type ListEntry struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Client talks to the ExoHub safe API.
type Client struct {
	apiBase    string
	httpClient *http.Client
}

// NewClient creates a safe client using the configured API base URL.
func NewClient() *Client {
	return &Client{
		apiBase:    strings.TrimRight(defaults.APIBase(), "/"),
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// Store uploads a typed secret to the safe.
func (c *Client) Store(token, name, entryType, value string) error {
	if len(value) > MaxSecretSize {
		return fmt.Errorf("secret exceeds %d byte limit (%d bytes)", MaxSecretSize, len(value))
	}

	body, err := json.Marshal(map[string]string{"value": value, "name": name})
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/safe/%s/%s", c.apiBase, entryType, safeKey(name))
	debugHTTP("PUT", url)

	req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to reach ExoHub API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

// List returns the names and types of all secrets in the user's safe.
func (c *Client) List(token string) ([]ListEntry, error) {
	url := fmt.Sprintf("%s/safe/keys", c.apiBase)
	debugHTTP("GET", url)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to reach ExoHub API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var entries []ListEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return entries, nil
}

// Delete removes a secret from the user's safe.
func (c *Client) Delete(token, entryType, name string) error {
	url := fmt.Sprintf("%s/safe/%s/%s", c.apiBase, entryType, safeKey(name))
	debugHTTP("DELETE", url)

	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to reach ExoHub API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

// GetAll returns all safe entries (names + types + values) for the authenticated user.
func (c *Client) GetAll(token string) ([]Entry, error) {
	url := fmt.Sprintf("%s/safe", c.apiBase)
	debugHTTP("GET", url)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to reach ExoHub API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var entries []Entry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return entries, nil
}
