package doctor

import (
	"fmt"
	"strings"

	addcmd "github.com/Genentech/exohub/go/exo/commands/add"
	initcmd "github.com/Genentech/exohub/go/exo/commands/init"
)

// runRoutingChecks validates annex.routing in repoDir/.exohub/config
// (design D9) and warns about rules that can never match because an
// earlier rule fully shadows them.
func runRoutingChecks(repoDir string) []CheckResult {
	id := "repo.routing"
	cfg, err := initcmd.LoadExohubConfigAt(repoDir)
	if err != nil {
		return []CheckResult{fail(id, groupRepo, "Cannot read .exohub/config", err.Error(), "Fix YAML syntax in .exohub/config")}
	}
	if cfg == nil || cfg.Annex == nil || len(cfg.Annex.Routing) == 0 {
		return []CheckResult{skip(id, groupRepo, "No annex.routing rules configured")}
	}

	if err := cfg.Annex.ValidateRouting(); err != nil {
		return []CheckResult{fail(id, groupRepo, "annex.routing has an invalid rule", err.Error(),
			"Fix the rule in .exohub/config")}
	}

	results := []CheckResult{pass(id, groupRepo, fmt.Sprintf("annex.routing OK (%d rule(s))", len(cfg.Annex.Routing)))}

	if shadowed := addcmd.DetectShadowedRules(cfg.Annex.Routing); len(shadowed) > 0 {
		rules := make([]string, len(shadowed))
		for i, idx := range shadowed {
			rules[i] = fmt.Sprintf("rule %d", idx)
		}
		results = append(results, warn("repo.routing-shadow", groupRepo,
			fmt.Sprintf("%d annex.routing rule(s) can never match", len(shadowed)),
			strings.Join(rules, ", ")+" are fully shadowed by an earlier rule",
			"Remove the shadowed rule(s) or move them before the rule that shadows them in .exohub/config"))
	}
	return results
}
