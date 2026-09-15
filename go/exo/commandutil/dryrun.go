package commandutil

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

type DryRunQuery struct {
	Label string
	Kind  string
	Args  []string
}

func BuildDryRunQueries(remote string, includeDrops bool) []DryRunQuery {
	queries := []DryRunQuery{
		{
			Label: fmt.Sprintf("here -> %s", remote),
			Kind:  "here_to_remote",
			Args:  []string{"git", "annex", "find", "--not", "--in", remote, "--in", "here"},
		},
		{
			Label: fmt.Sprintf("%s -> here", remote),
			Kind:  "remote_to_here",
			Args:  []string{"git", "annex", "find", "--not", "--in", "here", "--in", remote},
		},
	}
	if includeDrops {
		queries = append(queries, DryRunQuery{
			Label: fmt.Sprintf("drop candidates (here missing in %s)", remote),
			Kind:  "drops",
			Args:  []string{"git", "annex", "find", "--in", "here", "--not", "--in", remote},
		})
	}
	return queries
}

func RunAnnexFindFiles(baseArgs []string, paths []string) ([]string, error) {
	args := append([]string{}, baseArgs...)
	args = append(args, "--json")
	args = append(args, paths...)
	out, err := runCommandOutput(args)
	if err != nil {
		return nil, err
	}
	return parseAnnexFindJSON(out), nil
}

func StreamDryRunJSON(writer io.Writer, remotes []string, paths []string, includeDrops bool, schema string) error {
	bufWriter := bufio.NewWriter(writer)
	if _, err := bufWriter.WriteString("{\"$schema\":"); err != nil {
		return err
	}
	if err := writeJSONValue(bufWriter, schema); err != nil {
		return err
	}
	if _, err := bufWriter.WriteString(",\"remotes\":["); err != nil {
		return err
	}
	for i, remote := range remotes {
		queries := BuildDryRunQueries(remote, includeDrops)
		if i > 0 {
			if _, err := bufWriter.WriteString(","); err != nil {
				return err
			}
		}
		if _, err := bufWriter.WriteString("{\"name\":"); err != nil {
			return err
		}
		if err := writeJSONValue(bufWriter, remote); err != nil {
			return err
		}
		if _, err := bufWriter.WriteString(",\"here_to_remote\":"); err != nil {
			return err
		}
		if err := streamAnnexFindJSONArray(bufWriter, queries[0].Args, paths); err != nil {
			return err
		}
		if _, err := bufWriter.WriteString(",\"remote_to_here\":"); err != nil {
			return err
		}
		if err := streamAnnexFindJSONArray(bufWriter, queries[1].Args, paths); err != nil {
			return err
		}
		if includeDrops {
			if _, err := bufWriter.WriteString(",\"drops\":"); err != nil {
				return err
			}
			if err := streamAnnexFindJSONArray(bufWriter, queries[2].Args, paths); err != nil {
				return err
			}
		}
		if _, err := bufWriter.WriteString("}"); err != nil {
			return err
		}
	}
	if _, err := bufWriter.WriteString("]"); err != nil {
		return err
	}
	if len(paths) > 0 {
		if _, err := bufWriter.WriteString(",\"paths\":"); err != nil {
			return err
		}
		if err := writeJSONValue(bufWriter, paths); err != nil {
			return err
		}
	}
	if _, err := bufWriter.WriteString("}\n"); err != nil {
		return err
	}
	return bufWriter.Flush()
}

// StreamDryRunJSONExport streams JSON dry-run output for export remotes,
// respecting preferred content expressions. Export remotes only have here->remote transfers.
func StreamDryRunJSONExport(writer io.Writer, remotes []string, paths []string, schema string) error {
	bufWriter := bufio.NewWriter(writer)
	if _, err := bufWriter.WriteString("{\"$schema\":"); err != nil {
		return err
	}
	if err := writeJSONValue(bufWriter, schema); err != nil {
		return err
	}
	if _, err := bufWriter.WriteString(",\"remotes\":["); err != nil {
		return err
	}
	for i, remote := range remotes {
		queries := BuildDryRunQueriesWithPreferredContent(remote, false, true)
		if i > 0 {
			if _, err := bufWriter.WriteString(","); err != nil {
				return err
			}
		}
		if _, err := bufWriter.WriteString("{\"name\":"); err != nil {
			return err
		}
		if err := writeJSONValue(bufWriter, remote); err != nil {
			return err
		}

		// Add preferred_content field if set
		preferredContent := GetPreferredContent(remote)
		if preferredContent != "" {
			if _, err := bufWriter.WriteString(",\"preferred_content\":"); err != nil {
				return err
			}
			if err := writeJSONValue(bufWriter, preferredContent); err != nil {
				return err
			}
		}

		if _, err := bufWriter.WriteString(",\"here_to_remote\":"); err != nil {
			return err
		}
		if err := streamAnnexFindJSONArray(bufWriter, queries[0].Args, paths); err != nil {
			return err
		}
		// Export remotes don't have remote_to_here, so output empty array for schema compatibility
		if _, err := bufWriter.WriteString(",\"remote_to_here\":[]"); err != nil {
			return err
		}
		if _, err := bufWriter.WriteString("}"); err != nil {
			return err
		}
	}
	if _, err := bufWriter.WriteString("]"); err != nil {
		return err
	}
	if len(paths) > 0 {
		if _, err := bufWriter.WriteString(",\"paths\":"); err != nil {
			return err
		}
		if err := writeJSONValue(bufWriter, paths); err != nil {
			return err
		}
	}
	if _, err := bufWriter.WriteString("}\n"); err != nil {
		return err
	}
	return bufWriter.Flush()
}

func writeJSONValue(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func streamAnnexFindJSONArray(writer io.Writer, baseArgs []string, paths []string) error {
	bufWriter := bufio.NewWriter(writer)
	if _, err := bufWriter.WriteString("[\n"); err != nil {
		return err
	}
	wrote := false
	err := runAnnexFindJSONStream(baseArgs, paths, func(raw []byte) error {
		if wrote {
			if _, err := bufWriter.WriteString(",\n"); err != nil {
				return err
			}
		}
		if _, err := bufWriter.WriteString("  "); err != nil {
			return err
		}
		if _, err := bufWriter.Write(raw); err != nil {
			return err
		}
		wrote = true
		return nil
	})
	if err != nil {
		return err
	}
	if _, err := bufWriter.WriteString("\n]"); err != nil {
		return err
	}
	return bufWriter.Flush()
}

func runAnnexFindJSONStream(baseArgs []string, paths []string, handle func([]byte) error) error {
	args := append([]string{}, baseArgs...)
	args = append(args, "--json")
	args = append(args, paths...)
	cmd := Command(args[0], args[1:]...)
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(stdout)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		if !json.Valid(line) {
			return fmt.Errorf("invalid JSON from git annex find")
		}
		if err := handle(line); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return cmd.Wait()
}

func parseAnnexFindJSON(output string) []string {
	type payload struct {
		File string `json:"file"`
	}
	var files []string
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item payload
		if json.Unmarshal([]byte(line), &item) != nil {
			continue
		}
		if strings.TrimSpace(item.File) != "" {
			files = append(files, item.File)
		}
	}
	return uniqueStringsHelper(files)
}

func ParseAnnexFindJSON(output string) []string {
	return parseAnnexFindJSON(output)
}

func uniqueStringsHelper(input []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, item := range input {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func runCommandOutput(args []string) (string, error) {
	cmd := Command(args[0], args[1:]...)
	out, err := cmd.Output()
	return string(out), err
}

// RunCommandOutput is the exported version of runCommandOutput for use in other packages.
func RunCommandOutput(args []string) (string, error) {
	return runCommandOutput(args)
}

// GetPreferredContent returns the preferred content expression for a remote, if any.
// Returns empty string if no preferred content is set or if it's "standard".
func GetPreferredContent(remote string) string {
	out, err := runCommandOutput([]string{"git", "annex", "wanted", remote})
	if err != nil {
		return ""
	}
	wanted := strings.TrimSpace(out)
	if wanted == "" || wanted == "standard" {
		return ""
	}
	return wanted
}

// BuildDryRunQueriesWithPreferredContent returns queries that respect preferred content.
// For export remotes (which only push), it returns a single here->remote query.
// If the remote has preferred content configured, uses --want-get-by to filter.
func BuildDryRunQueriesWithPreferredContent(remote string, includeDrops bool, exportOnly bool) []DryRunQuery {
	preferredContent := GetPreferredContent(remote)

	if preferredContent != "" {
		// Use --want-get-by to respect preferred content expressions
		queries := []DryRunQuery{
			{
				Label: fmt.Sprintf("here -> %s", remote),
				Kind:  "here_to_remote",
				Args:  []string{"git", "annex", "find", "--in", "here", "--not", "--in", remote, "--want-get-by", remote},
			},
		}
		if !exportOnly {
			queries = append(queries, DryRunQuery{
				Label: fmt.Sprintf("%s -> here", remote),
				Kind:  "remote_to_here",
				Args:  []string{"git", "annex", "find", "--in", remote, "--not", "--in", "here", "--want-get"},
			})
		}
		if includeDrops {
			queries = append(queries, DryRunQuery{
				Label: fmt.Sprintf("drop candidates (here missing in %s)", remote),
				Kind:  "drops",
				Args:  []string{"git", "annex", "find", "--in", "here", "--not", "--in", remote, "--want-drop"},
			})
		}
		return queries
	}

	// No preferred content - use standard queries
	if exportOnly {
		return []DryRunQuery{
			{
				Label: fmt.Sprintf("here -> %s", remote),
				Kind:  "here_to_remote",
				Args:  []string{"git", "annex", "find", "--not", "--in", remote, "--in", "here"},
			},
		}
	}
	return BuildDryRunQueries(remote, includeDrops)
}
