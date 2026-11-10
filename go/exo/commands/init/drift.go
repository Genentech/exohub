package init

import (
	"encoding/json"
	"fmt"
	"os"
)

// driftExitCode is the exit code for `exo init --profile <name> --dry-run
// --json` when drift is found between the profile and the repo's
// .exohub/remotes, distinct from cobra's generic exit 1 for command errors,
// so automation (e.g. obol) can tell "stale repo" apart from "exo itself
// failed" (exo-cli#164).
const driftExitCode = 3

// computeDriftEntries re-renders spec's "remotes" template for each remote
// in remotesConfig (or only remoteFilter, if non-empty) and returns the
// per-field drift between the profile and the repo's current
// .exohub/remotes. A remote whose answers can't be resolved (no
// .exohub/profile.lock and no matching current field) is reported with
// status "unresolved" rather than skipped silently.
func computeDriftEntries(spec *ProfileSpec, remotesConfig *RemotesConfig, remoteFilter string) ([]driftEntry, error) {
	var entries []driftEntry
	for _, current := range remotesConfig.Remotes {
		if remoteFilter != "" && current.Name != remoteFilter {
			continue
		}

		rendered, err := renderedRemoteFor(spec, current)
		if err != nil {
			entries = append(entries, driftEntry{Remote: current.Name, Status: "unresolved", Profile: err.Error()})
			continue
		}

		entries = append(entries, diffRemoteConfig(current.Name, current, rendered)...)
	}

	if remoteFilter != "" && len(entries) == 0 {
		if _, found := findRemoteConfig(remotesConfig, remoteFilter); !found {
			return nil, fmt.Errorf("remote %q not found in .exohub/remotes", remoteFilter)
		}
	}
	return entries, nil
}

// hasActionableDrift reports whether entries contains anything a refresh
// would act on or that couldn't be checked ("local-only" is informational
// only and never actionable).
func hasActionableDrift(entries []driftEntry) bool {
	for _, e := range entries {
		if e.Status == "added" || e.Status == "changed" || e.Status == "unresolved" {
			return true
		}
	}
	return false
}

// runProfileDriftReport fetches profileName, computes drift against
// remotesConfig, and prints it as a JSON array. It never writes files or
// runs profile actions. It exits the process directly with driftExitCode
// when drift is found, so the normal cobra error path (exit 1) stays
// reserved for actual failures.
func runProfileDriftReport(profileName, remoteFilter string, remotesConfig *RemotesConfig) error {
	spec, err := FetchProfile(profileName)
	if err != nil {
		return err
	}

	entries, err := computeDriftEntries(spec, remotesConfig, remoteFilter)
	if err != nil {
		return err
	}

	out, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(out))

	if hasActionableDrift(entries) {
		os.Exit(driftExitCode)
	}
	return nil
}
