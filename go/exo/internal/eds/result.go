package eds

import "fmt"

// Outcome is the classification of a terminal Operation into a CLI result.
type Outcome struct {
	// Success is true only for completed operations whose result status is
	// "ok" or absent.
	Success bool

	// Destination is where the data now lives on the EDS (completed, success).
	// It can be empty even on success — the EDS does not always report it —
	// so callers must not print a label with nothing after it.
	Destination string

	// Scope is the scope the EDS used for the download, when reported.
	Scope string

	// MissingFiles lists files still missing after a "degraded" completion.
	MissingFiles []string

	// Message is a human-readable summary for non-success outcomes.
	Message string
}

// Classify turns a terminal Operation into an Outcome. Callers should only
// pass operations for which IsTerminal(op.Status) is true.
func Classify(op *Operation) Outcome {
	switch op.Status {
	case StatusCompleted:
		return classifyCompleted(op)
	case StatusFailed, StatusCanceled, StatusTerminated:
		return Outcome{Message: terminalMessage(op)}
	default:
		return Outcome{Message: fmt.Sprintf("operation ended with unexpected status %q", op.Status)}
	}
}

func classifyCompleted(op *Operation) Outcome {
	resultStatus := ResultOK
	var destination, scope string
	var missing []string
	if op.Result != nil {
		if op.Result.Status != "" {
			resultStatus = op.Result.Status
		}
		destination = op.Result.RepoDir
		scope = op.Result.Scope
		missing = op.Result.MissingFiles
	}

	if resultStatus == ResultOK {
		return Outcome{Success: true, Destination: destination, Scope: scope}
	}

	if resultStatus == ResultDegraded {
		return Outcome{
			MissingFiles: missing,
			Message:      fmt.Sprintf("download degraded: %d file(s) still missing after sync", len(missing)),
		}
	}

	return Outcome{Message: fmt.Sprintf("download completed with unexpected result status %q", resultStatus)}
}

func terminalMessage(op *Operation) string {
	switch {
	case op.Reason != "":
		return op.Reason
	case op.Message != "":
		return op.Message
	case op.Error != "":
		return op.Error
	default:
		return fmt.Sprintf("operation %s", op.Status)
	}
}
