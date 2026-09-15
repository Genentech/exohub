//go:build !darwin
// +build !darwin

package tui

func ignorePatterns(m commonModel) []string {
	return []string{
		m.cfg.Gopath,
		"node_modules",
		".*",
	}
}
