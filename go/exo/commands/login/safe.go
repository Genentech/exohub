package login

import (
	"fmt"
	"os"

	"github.com/Genentech/exohub/go/exo/safe"
)

// retrieveSafe fetches credentials from ExoSafe and provisions them locally.
// Errors are logged as warnings and never fail the login.
func retrieveSafe(token string, jsonMode bool) {
	client := safe.NewClient()
	entries, err := client.GetAll(token)
	if err != nil {
		if jsonMode {
			fmt.Fprintln(os.Stderr, "Warning: ExoSafe retrieval failed:", err)
		} else {
			fmt.Printf("⚠️  ExoSafe retrieval failed: %v\n", err)
		}
		return
	}

	if len(entries) == 0 {
		return
	}

	// Route entries by type
	var sshEntries, tokenEntries []safe.Entry
	for _, e := range entries {
		switch e.Type {
		case safe.TypeSSHKey:
			sshEntries = append(sshEntries, e)
		case safe.TypeToken:
			tokenEntries = append(tokenEntries, e)
		default:
			warn(jsonMode, "Unknown safe entry type %q for %q, skipping", e.Type, e.Name)
		}
	}

	if n, err := safe.ProvisionSSHKeys(sshEntries); err != nil {
		warn(jsonMode, "SSH key provisioning failed: %v", err)
	} else if n > 0 {
		info(jsonMode, "Provisioned %d SSH key(s) to ExoSafe SSH directory.", n)
	}

	if n, err := safe.ProvisionGitCredentials(tokenEntries); err != nil {
		warn(jsonMode, "Git credential provisioning failed: %v", err)
	} else if n > 0 {
		info(jsonMode, "Provisioned %d git credential(s).", n)
	}
}

func warn(jsonMode bool, format string, args ...any) {
	if jsonMode {
		fmt.Fprintf(os.Stderr, "Warning: "+format+"\n", args...)
	} else {
		fmt.Printf("⚠️  "+format+"\n", args...)
	}
}

func info(jsonMode bool, format string, args ...any) {
	if jsonMode {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	} else {
		fmt.Printf("🔐 "+format+"\n", args...)
	}
}
