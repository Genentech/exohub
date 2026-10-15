package eds

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// ResolveDeployment resolves an --eds argument to a Deployment.
//
// If edsArg starts with "http://" or "https://", it is treated as the EDS
// base URL directly: GET <edsArg>/ returns the deployment's root info, no
// index lookup needed.
//
// Otherwise edsArg is an index id: GET <baseURL>/eds is fetched and matched
// against deployments[].id. The index id is only used to find the entry —
// it is not the deployment_id, which lives in the entry's info and may
// differ (e.g. multiple index ids can share one deployment_id).
func ResolveDeployment(client *http.Client, edsArg, baseURL string) (*Deployment, error) {
	if strings.HasPrefix(edsArg, "http://") || strings.HasPrefix(edsArg, "https://") {
		return resolveByURL(client, edsArg)
	}
	return resolveByID(client, baseURL, edsArg)
}

func resolveByURL(client *http.Client, edsURL string) (*Deployment, error) {
	edsURL = strings.TrimRight(edsURL, "/")

	info, err := fetchRootInfo(client, edsURL)
	if err != nil {
		return nil, fmt.Errorf("fetching EDS root info from %s: %w", edsURL, err)
	}

	return &Deployment{URL: edsURL, Info: *info}, nil
}

func resolveByID(client *http.Client, baseURL, id string) (*Deployment, error) {
	indexURL := strings.TrimRight(baseURL, "/") + "/eds"

	resp, err := client.Get(indexURL)
	if err != nil {
		return nil, fmt.Errorf("fetching EDS index from %s: %w", indexURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching EDS index from %s: unexpected status %d", indexURL, resp.StatusCode)
	}

	var index IndexResponse
	if err := json.NewDecoder(resp.Body).Decode(&index); err != nil {
		return nil, fmt.Errorf("decoding EDS index from %s: %w", indexURL, err)
	}

	var available []string
	for _, entry := range index.Deployments {
		available = append(available, entry.ID)
		if entry.ID != id {
			continue
		}
		if entry.Status != "active" {
			if entry.Error != "" {
				return nil, fmt.Errorf("EDS %q is not active: %s", id, entry.Error)
			}
			return nil, fmt.Errorf("EDS %q is not active (status: %s)", id, entry.Status)
		}
		return &Deployment{URL: strings.TrimRight(entry.URL, "/"), Info: entry.Info}, nil
	}

	sort.Strings(available)
	return nil, fmt.Errorf("unknown EDS id %q; available: %s", id, strings.Join(available, ", "))
}

func fetchRootInfo(client *http.Client, edsURL string) (*Info, error) {
	resp, err := client.Get(edsURL + "/")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var info Info
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decoding root info: %w", err)
	}
	return &info, nil
}
