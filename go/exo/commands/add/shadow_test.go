package add

import (
	"reflect"
	"testing"

	initcmd "github.com/Genentech/exohub/go/exo/commands/init"
)

func TestDetectShadowedRulesDesignExample(t *testing.T) {
	// design D9: rule 1 "**/*.csv" fully shadows rule 3 "data/*.csv",
	// regardless of the (different) destination each rule picks.
	rules := []initcmd.RoutingRule{
		{Path: "**/*.csv", To: "git"},
		{Path: "**/*.md", To: "git"},
		{Path: "data/*.csv", To: "annex"},
	}
	got := DetectShadowedRules(rules)
	if !reflect.DeepEqual(got, []int{3}) {
		t.Errorf("DetectShadowedRules() = %v, want [3]", got)
	}
}

func TestDetectShadowedRulesNoShadowWhenNotProvable(t *testing.T) {
	tests := []struct {
		name  string
		rules []initcmd.RoutingRule
	}{
		{
			"different directories",
			[]initcmd.RoutingRule{
				{Path: "data/*.csv", To: "git"},
				{Path: "other/*.csv", To: "annex"},
			},
		},
		{
			"earlier rule has a restrictive key the later rule doesn't share",
			[]initcmd.RoutingRule{
				{Path: "**/*.csv", SmallerThan: "1MB", To: "git"},
				{Path: "data/*.csv", To: "annex"},
			},
		},
		{
			"earlier rule has no match-key overlap",
			[]initcmd.RoutingRule{
				{LargerThan: "1MB", To: "git"},
				{Path: "data/*.csv", To: "annex"},
			},
		},
		{
			"earlier content differs",
			[]initcmd.RoutingRule{
				{Content: "text", To: "git"},
				{Content: "binary", To: "annex"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectShadowedRules(tt.rules); len(got) != 0 {
				t.Errorf("DetectShadowedRules() = %v, want none", got)
			}
		})
	}
}

func TestDetectShadowedRulesSizeIntervals(t *testing.T) {
	// larger_than: an earlier rule with a lower (or equal) minimum covers
	// every later rule whose own minimum is at least as high.
	rules := []initcmd.RoutingRule{
		{LargerThan: "1KB", To: "git"},
		{LargerThan: "1MB", To: "annex"},
	}
	if got := DetectShadowedRules(rules); !reflect.DeepEqual(got, []int{2}) {
		t.Errorf("DetectShadowedRules() = %v, want [2]", got)
	}

	// smaller_than: an earlier rule with a higher (or equal) ceiling
	// covers every later rule whose own ceiling is at most as high.
	rules = []initcmd.RoutingRule{
		{SmallerThan: "1MB", To: "git"},
		{SmallerThan: "1KB", To: "annex"},
	}
	if got := DetectShadowedRules(rules); !reflect.DeepEqual(got, []int{2}) {
		t.Errorf("DetectShadowedRules() = %v, want [2]", got)
	}
}

func TestDetectShadowedRulesContentMatch(t *testing.T) {
	rules := []initcmd.RoutingRule{
		{Content: "binary", To: "git"},
		{Content: "binary", LargerThan: "1MB", To: "annex"},
	}
	if got := DetectShadowedRules(rules); !reflect.DeepEqual(got, []int{2}) {
		t.Errorf("DetectShadowedRules() = %v, want [2]", got)
	}
}
