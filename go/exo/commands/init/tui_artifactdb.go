//go:build artifactdb

package init

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/Genentech/exohub/go/exo/commandutil"
)

func createArtifactDBRemoteTUI(ctx *tuiContext) error {
	name := "exohub-atlas"
	var s3url, instanceURL string

	// Derive s3url from first existing annex remote, replacing _annex suffix with _catalog
	suggestedS3URL := ""
	if remotesConfig, err := LoadRemotesConfig(); err == nil {
		for _, r := range remotesConfig.Remotes {
			if r.Type == "annex" && r.S3URL != "" {
				suggestedS3URL = strings.TrimSuffix(r.S3URL, "/_annex") + "/_catalog"
				break
			}
		}
	}
	if suggestedS3URL == "" {
		suggestedS3URL = fmt.Sprintf("s3://%s-data/_catalog", strings.ReplaceAll(ctx.Org, "/", "-"))
	}

	// Pre-fill s3url with the derived value
	s3url = suggestedS3URL

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Remote name").
				Value(&name).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("name is required")
					}
					if remoteExists(s) {
						return fmt.Errorf("remote '%s' already exists", s)
					}
					return nil
				}),
			huh.NewInput().
				Title("S3 URL").
				Placeholder(suggestedS3URL).
				Description("S3 location for catalog artifacts").
				Value(&s3url).
				Validate(func(s string) error {
					if !strings.HasPrefix(s, "s3://") {
						return fmt.Errorf("must start with s3://")
					}
					return nil
				}),
			huh.NewInput().
				Title("ArtifactDB instance URL (optional, for custom metadata)").
				Placeholder("https://artifactdb.example.com").
				Description("URL of the ArtifactDB instance").
				Value(&instanceURL),
		),
	).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())

	if err := form.Run(); err != nil {
		return err
	}

	// Ask about grants
	useGrants, err := promptGrants()
	if err != nil {
		return err
	}

	// Ensure we're in a git repo
	if !IsGitRepo() {
		fmt.Println("Not a git repository. Initializing...")
		if err := ensureGitRepo(); err != nil {
			return err
		}
	}

	// Ensure git-annex is initialized
	if err := ensureAnnexInitialized(); err != nil {
		return err
	}

	// Check binary availability
	if err := checkRemoteBinaries("artifactdb"); err != nil {
		return err
	}

	// Create the remote
	externalType := "artifactdb-export"
	fmt.Printf("\nInitializing %s as exporttree via %s external remote\n", name, externalType)
	initArgs := []string{
		"git", "annex", "initremote", name,
		"type=external", "externaltype=" + externalType, "encryption=none",
		"s3url=" + s3url, "exporttree=yes",
	}
	if useGrants {
		initArgs = append(initArgs, "grants=true")
	}
	if instanceURL != "" {
		initArgs = append(initArgs, "instance_url="+instanceURL)
	}
	if err := runCommand(initArgs); err != nil {
		return err
	}

	// Enable remote again to ensure settings are applied
	_ = runCommand([]string{"git", "annex", "enableremote", name, "s3url=" + s3url, "exporttree=yes"})

	ensureRemoteConfigS3URL(name, s3url)

	// Set preferred content: only export .exohub/bundles/ and .artifactdb/
	wanted := "include=.exohub/bundles/* or include=.artifactdb/*"
	_ = runCommand([]string{"git", "annex", "wanted", name, wanted})
	fmt.Printf("Set preferred content for '%s': %s\n", name, wanted)

	// Save to .exohub/remotes
	remotesConfig, err := LoadRemotesConfig()
	if err != nil {
		return err
	}

	uuid := getRemoteUUID(name)

	remotesConfig.AddRemote(RemoteConfig{
		Name:        name,
		Type:        "artifactdb",
		UUID:        uuid,
		S3URL:       s3url,
		Grants:      useGrants,
		InstanceURL: instanceURL,
		Mode:        "export",
	})

	if err := SaveRemotesConfig(remotesConfig); err != nil {
		return err
	}

	// Set up grants permissions file if grants enabled
	if useGrants {
		if _, err := loadOrCreatePermissions(); err != nil {
			fmt.Printf("⚠️  Could not create permissions file: %v\n", err)
		}
	}

	fmt.Printf("✅ Remote %s created and saved to .exohub/remotes\n", SuccessStyle.Render(name))
	return nil
}
