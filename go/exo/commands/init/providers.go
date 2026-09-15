//go:build !internal

package init

// defaultGitProviders returns the list of selectable git providers for the init TUI.
func defaultGitProviders() []providerOption {
	return []providerOption{
		{name: "GitHub (github.com)", provider: "github", host: "https://github.com"},
		{name: "GitLab", provider: "gitlab", host: ""},
		{name: "Gitea", provider: "gitea", host: ""},
	}
}
