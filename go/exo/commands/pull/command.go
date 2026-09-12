package pull

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/Genentech/exohub/go/exo/commandutil"
	manifestutil "github.com/Genentech/exohub/go/exo/commandutil"
)

var command = commandutil.Command

const longText = `Clone or update a git repository at a specific ref.

Clones the repository if it does not exist locally, or resets to the specified
ref if it does. Accepts a manifest YAML file as an alternative to flags.`

type manifest struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
	Ref  string `yaml:"ref"`
}

func NewCommand() *cobra.Command {
	var manifestPath string
	var url string
	var ref string

	rootCmd := &cobra.Command{
		Use:   "pull",
		Short: "Clone or update a repo at a ref",
		Long:  longText,
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) > 0 {
				_ = cmd.Help()
				os.Exit(1)
			}
			runPull(manifestPath, url, ref)
		},
	}

	rootCmd.Flags().StringVar(&manifestPath, "manifest", "", "Manifest file path")
	rootCmd.Flags().StringVar(&url, "url", "", "Git URL")
	rootCmd.Flags().StringVar(&ref, "ref", "", "Git ref (commit, tag, or branch)")

	return rootCmd
}

func runPull(manifestPath, url, ref string) {
	if manifestPath != "" {
		if err := validatePullManifest(manifestPath); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		m, err := parseManifest(manifestPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		if url == "" && m.URL != "" {
			url = m.URL
		}
		if ref == "" && m.Ref != "" {
			ref = m.Ref
		}
	}

	if url == "" {
		fmt.Fprintln(os.Stderr, "--url is required")
		os.Exit(1)
	}
	if ref == "" {
		fmt.Fprintln(os.Stderr, "--ref is required")
		os.Exit(1)
	}

	if !isDir(".git") {
		fmt.Printf("Cloning %s\n", url)
		if err := runCommand([]string{"git", "clone", url, "."}); err != nil {
			os.Exit(1)
		}
	} else {
		existingURL, _ := runCommandOutput([]string{"git", "remote", "get-url", "origin"})
		existingURL = strings.TrimSpace(existingURL)
		if existingURL != "" && existingURL != url {
			fmt.Fprintf(os.Stderr, "Existing repo origin URL (%s) does not match requested URL (%s)\n", existingURL, url)
			os.Exit(1)
		}
		fmt.Println("Repository already exists. Fetching updates...")
		if err := runCommand([]string{"git", "fetch", "--all", "--tags", "--prune"}); err != nil {
			os.Exit(1)
		}
	}

	if err := verifyRef(url, ref); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	targetHash, err := runCommandOutput([]string{"git", "rev-parse", fmt.Sprintf("%s^{commit}", ref)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ref '%s' (commit/branch/tag) not found in repository '%s'\n", ref, url)
		os.Exit(1)
	}
	targetHash = strings.TrimSpace(targetHash)

	currentHash, err := runCommandOutput([]string{"git", "rev-parse", "HEAD"})
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	currentHash = strings.TrimSpace(currentHash)

	if currentHash != targetHash {
		fmt.Printf("Checking out %s (%s)\n", ref, targetHash)
		if err := checkoutRef(ref, targetHash); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to check out desired ref '%s' (resolved %s)\n", ref, targetHash)
			os.Exit(1)
		}
		currentHash, _ = runCommandOutput([]string{"git", "rev-parse", "HEAD"})
		currentHash = strings.TrimSpace(currentHash)
		if currentHash != targetHash {
			fmt.Fprintf(os.Stderr, "Failed to check out desired ref '%s' (resolved %s)\n", ref, targetHash)
			os.Exit(1)
		}
	}

	fmt.Printf("Repository is now at commit %s\n", targetHash)
}

func validatePullManifest(path string) error {
	payload, err := manifestutil.ReadManifest(path)
	if err != nil {
		return err
	}
	normalized := manifestutil.NormalizeManifest(payload)
	manifestType, err := manifestutil.InferManifestType(normalized)
	if err != nil {
		return err
	}
	return manifestutil.ValidateManifestFile(manifestType, path)
}

func parseManifest(path string) (manifest, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return manifest{}, fmt.Errorf("Manifest not found: %s", path)
	}
	var m manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return manifest{}, err
	}
	return m, nil
}

func verifyRef(url, ref string) error {
	cmd := command("git", "rev-parse", "--quiet", "--verify", fmt.Sprintf("%s^{commit}", ref))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Ref '%s' (commit/branch/tag) not found in repository '%s'", ref, url)
	}
	return nil
}

func checkoutRef(ref, hash string) error {
	if command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+ref).Run() == nil {
		return runCommand([]string{"git", "checkout", ref})
	}
	return runCommand([]string{"git", "checkout", hash})
}

func runCommand(args []string) error {
	cmd := command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func runCommandOutput(args []string) (string, error) {
	cmd := command(args[0], args[1:]...)
	out, err := cmd.Output()
	return string(out), err
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
