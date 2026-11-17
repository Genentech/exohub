package main

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/Genentech/exohub/go/exo/internal/help"
)

// These mirror the exohub-workers EDS --help parser contract
// (api/data_service/exo_commands.py): description is everything before the
// first "\nUsage:" line, section headers are literal "Flags:" / "Global
// Flags:" lines, and flag lines are cobra's FlagUsages() format.
var (
	edsSectionHeaderRe = regexp.MustCompile(`(?m)^(\S[^:]*):\s*$`)
	edsFlagLineRe      = regexp.MustCompile(`(?m)^\s+(?:-\w, )?--(?P<name>[A-Za-z0-9][\w-]*)(?: (?P<type>\w+))?\s{2,}(?P<description>.*)$`)
)

func edsDescription(out string) string {
	return strings.SplitN(out, "\nUsage:", 2)[0]
}

// edsFlag is a (name, type, global) tuple as EDS would extract it: the flag
// name and declared type, and whether it came from the "Global Flags:"
// section rather than "Flags:".
type edsFlag struct {
	name   string
	typ    string
	global bool
}

// edsParseFlags walks out line by line, tracking the current section via
// edsSectionHeaderRe and collecting every flag line matched within it,
// exactly as EDS's two-pass (split sections, then regex each) parser would.
func edsParseFlags(out string) []edsFlag {
	var flags []edsFlag
	section := ""
	for _, line := range strings.Split(out, "\n") {
		if m := edsSectionHeaderRe.FindStringSubmatch(line); m != nil {
			section = m[1]
			continue
		}
		if m := edsFlagLineRe.FindStringSubmatch(line); m != nil {
			flags = append(flags, edsFlag{name: m[1], typ: m[2], global: section == "Global Flags"})
		}
	}
	return flags
}

// TestEDSParserContract applies the EDS --help parsing contract to full and
// short help output for a leaf (exo sync), a nested command (exo provenance
// show), and a parent (exo provenance), and asserts: the short-mode
// description is exactly the command's Short (Long's prose is dropped), the
// flag sets (name, type, global-or-local) extracted from Flags:/Global
// Flags: are identical between modes, and the last line is exactly
// "Full manual: <base>/docs/reference/cli/exo-<words>/" in both modes.
func TestEDSParserContract(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"leaf", []string{"sync"}},
		{"nested", []string{"provenance", "show"}},
		{"parent", []string{"provenance"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := buildRootCmd("dev", "", "")
			cmd, _, err := root.Find(tc.args)
			if err != nil {
				t.Fatalf("Find(%v): %v", tc.args, err)
			}
			wantShort := strings.TrimSpace(cmd.Short)
			wantManual := "Full manual: " + help.ManPageURL(cmd)

			t.Setenv("EXO_HELP", "")
			fullOut := runHelp(t, tc.args...)
			t.Setenv("EXO_HELP", "short")
			shortOut := runHelp(t, tc.args...)

			shortDesc := strings.TrimSpace(edsDescription(shortOut))
			if shortDesc != wantShort {
				t.Errorf("short-mode description = %q, want command's Short %q", shortDesc, wantShort)
			}

			fullHasGlobal := strings.Contains(fullOut, "\nGlobal Flags:\n")
			shortHasGlobal := strings.Contains(shortOut, "\nGlobal Flags:\n")
			if fullHasGlobal != shortHasGlobal {
				t.Errorf("Global Flags: header presence differs: full=%v short=%v", fullHasGlobal, shortHasGlobal)
			}

			fullFlags := edsParseFlags(fullOut)
			shortFlags := edsParseFlags(shortOut)
			if len(fullFlags) == 0 {
				t.Fatalf("no flags parsed from full help:\n%s", fullOut)
			}
			if !sameFlagSet(fullFlags, shortFlags) {
				t.Errorf("flag sets (name, type, global) differ between full and short help:\nfull:  %v\nshort: %v", fullFlags, shortFlags)
			}

			for _, out := range []string{fullOut, shortOut} {
				trimmed := strings.TrimRight(out, "\n")
				lines := strings.Split(trimmed, "\n")
				last := lines[len(lines)-1]
				if last != wantManual {
					t.Errorf("last line = %q, want %q", last, wantManual)
				}
				if edsSectionHeaderRe.MatchString(last) {
					t.Errorf("Full manual: line was mistaken for a section header: %q", last)
				}
				if edsFlagLineRe.MatchString(last) {
					t.Errorf("Full manual: line was mistaken for a flag line: %q", last)
				}
			}
		})
	}
}

func sameFlagSet(a, b []edsFlag) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	key := func(f edsFlag) string { return fmt.Sprintf("%s|%s|%v", f.name, f.typ, f.global) }
	for _, f := range a {
		seen[key(f)]++
	}
	for _, f := range b {
		seen[key(f)]--
	}
	for _, c := range seen {
		if c != 0 {
			return false
		}
	}
	return true
}
