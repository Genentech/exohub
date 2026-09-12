package get

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Genentech/exohub/go/exo/commandutil"
	"github.com/Genentech/exohub/go/exo/configdir"
)

func NewCommand() *cobra.Command {
	var (
		fromRemote string
		allFlag    bool
		jobsFlag   string
		jobs       = strings.TrimSpace(os.Getenv("EXOHUB_JOBS"))
	)
	if jobs == "" {
		jobs = "1"
	}

	rootCmd := &cobra.Command{
		Use:   "get [flags] [path ...]",
		Short: "Get annexed content (wrapper around git-annex get with sensible defaults)",
		Long: `Get annexed content from remotes into the local repository.

Wraps git-annex get with sensible defaults:
  - Resumes incomplete downloads first (--incomplete), then gets remaining content
  - Parallelism from EXOHUB_JOBS environment variable (default: 1)

Examples:
  exo get                     # get all annexed content in current directory
  exo get data/               # get content under data/
  exo get --from s3-backup .  # get from a specific remote
  exo get --all               # get all versions of all files
  exo get -J 8 .              # get with 8 parallel jobs`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireBins("git-annex"); err != nil {
				exitWithError(err)
			}

			// Pre-seed AWS credentials from file/env if available.
			// This is advisory: on the grants-managed path credentials are
			// supplied lazily by the git-annex credential helper, so a missing
			// file/env is not an error. Only log the detail under EXOHUB_CLI_DEBUG.
			if err := ensureAWSCredentials(); err != nil {
				if os.Getenv("EXOHUB_CLI_DEBUG") == "1" {
					fmt.Fprintf(os.Stderr, "debug: AWS credentials pre-seed skipped: %v\n", err)
				}
			}

			// CLI flag takes precedence over environment variable
			if jobsFlag != "" {
				jobs = jobsFlag
			}

			// git-annex get treats --incomplete, --all, and path arguments as
			// mutually exclusive modes. We run --incomplete first to resume any
			// previously interrupted downloads, then run the actual get.
			incompleteArgs := []string{"git", "annex", "get", "--incomplete", "-J", jobs}
			if fromRemote != "" {
				incompleteArgs = append(incompleteArgs, "--from", fromRemote)
			}
			// Best-effort: ignore errors from --incomplete (nothing to resume is fine)
			_ = runCommand(incompleteArgs)

			cmdArgs := []string{"git", "annex", "get", "-J", jobs}

			if fromRemote != "" {
				cmdArgs = append(cmdArgs, "--from", fromRemote)
			}

			if allFlag {
				cmdArgs = append(cmdArgs, "--all")
			}

			// If no paths provided and --all not specified, default to current directory
			if len(args) == 0 && !allFlag {
				cmdArgs = append(cmdArgs, ".")
			} else {
				cmdArgs = append(cmdArgs, args...)
			}

			return runCommand(cmdArgs)
		},
	}

	rootCmd.Flags().StringVar(&fromRemote, "from", "", "Source annex remote")
	rootCmd.Flags().BoolVar(&allFlag, "all", false, "Get all versions of all files")
	rootCmd.Flags().StringVarP(&jobsFlag, "jobs", "J", "", "Number of parallel jobs (default: $EXOHUB_JOBS or 1)")

	return rootCmd
}

func exitWithError(err error) {
	fmt.Fprintln(os.Stderr, err.Error())
	os.Exit(1)
}

func requireBins(names ...string) error {
	missing := false
	for _, name := range names {
		if !commandExists(name) {
			fmt.Fprintf(os.Stderr, "Required binary '%s' not found in PATH\n", name)
			missing = true
		}
	}
	if missing {
		return errors.New("missing binaries")
	}
	return nil
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func runCommand(args []string) error {
	cmd := commandutil.Command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// ensureAWSCredentials checks if AWS credentials are available and sets them from the credentials file if needed
func ensureAWSCredentials() error {
	if os.Getenv("AWS_ACCESS_KEY_ID") != "" && os.Getenv("AWS_SECRET_ACCESS_KEY") != "" {
		return nil
	}

	// Use exohub profile by default
	profile := os.Getenv("EXOHUB_AWS_PROFILE")
	if profile == "" {
		profile = "exohub"
	}

	credsFile, err := configdir.AWSCredentialsFile()
	if err != nil {
		return fmt.Errorf("failed to resolve AWS credentials path: %w", err)
	}

	accessKey, secretKey, sessionToken, err := parseAWSCredentials(credsFile, profile)
	if err != nil {
		return err
	}

	os.Setenv("AWS_ACCESS_KEY_ID", accessKey)
	os.Setenv("AWS_SECRET_ACCESS_KEY", secretKey)
	if sessionToken != "" {
		os.Setenv("AWS_SESSION_TOKEN", sessionToken)
	}

	return nil
}

func parseAWSCredentials(filePath, profile string) (accessKey, secretKey, sessionToken string, err error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to open AWS credentials file %s: %w", filePath, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	inProfile := false
	profileHeader := "[" + profile + "]"

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inProfile = (line == profileHeader)
			continue
		}

		if inProfile && strings.Contains(line, "=") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				continue
			}
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])

			switch key {
			case "aws_access_key_id":
				accessKey = value
			case "aws_secret_access_key":
				secretKey = value
			case "aws_session_token":
				sessionToken = value
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return "", "", "", fmt.Errorf("error reading credentials file: %w", err)
	}

	if accessKey == "" || secretKey == "" {
		return "", "", "", fmt.Errorf("AWS credentials not found for profile '%s' in %s", profile, filePath)
	}

	return accessKey, secretKey, sessionToken, nil
}
