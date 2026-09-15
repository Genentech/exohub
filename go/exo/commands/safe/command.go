package safe

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/Genentech/exohub/go/exo/commands/login"
	"github.com/Genentech/exohub/go/exo/commandutil"
	"github.com/Genentech/exohub/go/exo/gitprovider"
	"github.com/Genentech/exohub/go/exo/safe"
)

func typeIcon(t string) string {
	switch t {
	case safe.TypeSSHKey:
		return "🔑"
	case safe.TypeToken:
		return "🎫"
	default:
		return "•"
	}
}

var (
	storeFile    string
	storeType    string
	storeYes     bool
	storeFromEnv string
)

// wellKnownTokenEnvVars maps hostnames to well-known environment variable names.
var wellKnownTokenEnvVars = map[string]string{
	"github.com": "GITHUB_TOKEN",
	"gitlab.com": "GITLAB_TOKEN",
}

var (
	styleOrange = lipgloss.NewStyle().Foreground(commandutil.ColorOrange)
	styleDim    = lipgloss.NewStyle().Foreground(commandutil.ColorDim)
)

// NewCommand creates the `exo safe` parent command with subcommands.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "safe",
		Short: "Manage credentials in ExoSafe",
		Long: `Manage SSH keys and git tokens in your personal ExoSafe credential store.

ExoSafe stores credentials in the cloud (AWS Parameter Store) so they can be
provisioned automatically when you run 'exo login' in new environments.`,
	}

	cmd.AddCommand(newPullCommand())
	cmd.AddCommand(newStoreCommand())
	cmd.AddCommand(newListCommand())
	cmd.AddCommand(newShowCommand())
	cmd.AddCommand(newDeleteCommand())
	cmd.AddCommand(newClearCommand())
	cmd.AddCommand(newDumpCommand())
	cmd.AddCommand(newRestoreCommand())
	return cmd
}

func loadToken() (string, error) {
	tokenFile, err := login.GetTokenFile()
	if err != nil {
		return "", err
	}
	token, err := login.LoadToken(tokenFile)
	if err != nil {
		return "", fmt.Errorf("not authenticated; run 'exo login' first")
	}
	if !token.Valid() {
		if token.RefreshToken != "" {
			refreshed, _, err := login.RefreshAuth(token.RefreshToken)
			if err != nil {
				return "", fmt.Errorf("token expired and refresh failed; run 'exo login' first")
			}
			_ = login.SaveToken(tokenFile, refreshed)
			return refreshed.AccessToken, nil
		}
		return "", fmt.Errorf("token expired; run 'exo login' first")
	}
	return token.AccessToken, nil
}

func newStoreCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "store [name] --type <ssh-key|token>",
		Short: "Store a credential in ExoSafe",
		Long: `Upload a credential to your ExoSafe.

Run without arguments for an interactive wizard, or provide flags directly.

The name should be the git provider hostname (e.g., 'github.com', 'gitlab.com').

Examples:
  exo safe store                                                  # interactive wizard
  exo safe store github.com --type ssh-key --file ~/.ssh/id_ed25519
  exo safe store github.com --type token --file ~/token.txt
  exo safe store github.com --type token --from-env GITHUB_TOKEN
  exo safe store github.com --type token                          # auto-detects $GITHUB_TOKEN`,
		Args: cobra.MaximumNArgs(1),
		RunE: runStore,
	}
	cmd.Flags().StringVar(&storeType, "type", "", "Credential type: ssh-key or token")
	cmd.Flags().StringVar(&storeFile, "file", "", "Path to the credential file")
	cmd.Flags().StringVar(&storeFromEnv, "from-env", "", "Read credential value from this environment variable")
	cmd.Flags().BoolVarP(&storeYes, "yes", "y", false, "Skip confirmation prompts")
	return cmd
}

func runStore(cmd *cobra.Command, args []string) error {
	// Interactive mode: no args and no flags provided
	if len(args) == 0 && storeType == "" && storeFile == "" && storeFromEnv == "" {
		return runStoreInteractive()
	}

	// Non-interactive: require name and type
	if len(args) == 0 {
		return fmt.Errorf("name argument is required in non-interactive mode")
	}
	if storeType == "" {
		return fmt.Errorf("--type is required in non-interactive mode")
	}

	name := args[0]
	return doStore(name, storeType, storeFile, storeFromEnv)
}

func runStoreInteractive() error {
	var (
		entryType string
		name      string
		filePath  string
		fromEnv   string
		source    string // token source selection result
		wizardErr error
		done      bool
	)

	// Enter alternate screen for the wizard forms
	fmt.Print("\x1b[?1049h")

	// Steps: 0=type, 1=hostname, 2=source, 3=manual input (if needed)
	step := 0
	for step >= 0 && !done {
		fmt.Print("\x1b[2J\x1b[H") // clear within alt screen between steps
		switch step {
		case 0: // Select credential type
			entryType = ""
			form := huh.NewForm(
				huh.NewGroup(
					huh.NewSelect[string]().
						Title("What type of credential do you want to store?").
						Options(
							huh.NewOption("🔑 SSH Key — for git clone/push to SSH git hosts", safe.TypeSSHKey),
							huh.NewOption("🎫 Git Token — to let exo create repositories and push over HTTPS", safe.TypeToken),
						).
						Value(&entryType),
				),
			).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())
			if err := form.Run(); err != nil {
				if errors.Is(err, huh.ErrUserAborted) {
					step = -1 // exit wizard
					continue
				}
				wizardErr = err
				done = true
				continue
			}
			step++

		case 1: // Git provider hostname
			name = ""
			var hostTitle string
			var hostOptions []huh.Option[string]

			hostTitle, hostOptions = safeHostOptions(entryType, styleDim)

			selected := hostOptions[0].Value // pre-select first
			selectForm := huh.NewForm(
				huh.NewGroup(
					huh.NewSelect[string]().
						Title(hostTitle).
						Options(hostOptions...).
						Value(&selected),
				),
			).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())
			if err := selectForm.Run(); err != nil {
				if errors.Is(err, huh.ErrUserAborted) {
					step--
					continue
				}
				wizardErr = err
				done = true
				continue
			}

			if selected == ":custom:" {
				// Manual input
				inputForm := huh.NewForm(
					huh.NewGroup(
						huh.NewInput().
							Title(hostTitle).
							Placeholder("e.g. gitlab.example.com").
							Value(&name).
							Validate(func(s string) error {
								s = strings.TrimSpace(s)
								if s == "" {
									return fmt.Errorf("hostname is required")
								}
								if strings.ContainsAny(s, " /\\") {
									return fmt.Errorf("hostname should not contain spaces or slashes")
								}
								return nil
							}),
					),
				).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())
				if err := inputForm.Run(); err != nil {
					if errors.Is(err, huh.ErrUserAborted) {
						continue // re-show the host select
					}
					wizardErr = err
					done = true
					continue
				}
				name = strings.TrimSpace(name)
			} else {
				name = selected
			}
			step++

		case 2: // Source selection
			filePath = ""
			fromEnv = ""
			source = ""

			if entryType == safe.TypeSSHKey {
				if err := stepSSHKeySelect(&filePath); err != nil {
					if errors.Is(err, huh.ErrUserAborted) {
						step--
						continue
					}
					wizardErr = err
					done = true
					continue
				}
				if filePath != "" {
					done = true
					continue
				}
				step++
			} else {
				var err error
				source, err = promptTokenSource()
				if err != nil {
					if errors.Is(err, huh.ErrUserAborted) {
						step--
						continue
					}
					wizardErr = err
					done = true
					continue
				}
				if strings.HasPrefix(source, "env:") {
					fromEnv = strings.TrimPrefix(source, "env:")
					done = true
					continue
				}
				step++
			}

		case 3: // Manual input (SSH path or token paste)
			if entryType == safe.TypeSSHKey {
				filePath = ""
				form := huh.NewForm(
					huh.NewGroup(
						huh.NewInput().
							Title("Path to SSH private key").
							Placeholder("~/.ssh/id_ed25519").
							Value(&filePath).
							Validate(func(s string) error {
								s = strings.TrimSpace(s)
								if s == "" {
									return fmt.Errorf("file path is required")
								}
								expanded := expandHome(s)
								if _, err := os.Stat(expanded); os.IsNotExist(err) {
									return fmt.Errorf("file not found: %s", s)
								}
								return nil
							}),
					),
				).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())
				if err := form.Run(); err != nil {
					if errors.Is(err, huh.ErrUserAborted) {
						step--
						continue
					}
					wizardErr = err
					done = true
					continue
				}
				filePath = expandHome(strings.TrimSpace(filePath))
				done = true
			} else {
				var tokenValue string
				form := huh.NewForm(
					huh.NewGroup(
						huh.NewInput().
							Title("Paste your token").
							EchoMode(huh.EchoModePassword).
							Value(&tokenValue).
							Validate(func(s string) error {
								if strings.TrimSpace(s) == "" {
									return fmt.Errorf("token is required")
								}
								return nil
							}),
					),
				).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())
				if err := form.Run(); err != nil {
					if errors.Is(err, huh.ErrUserAborted) {
						step--
						continue
					}
					wizardErr = err
					done = true
					continue
				}
				tmpFile, err := os.CreateTemp("", "exosafe-token-*")
				if err != nil {
					wizardErr = fmt.Errorf("failed to create temp file: %w", err)
					done = true
					continue
				}
				_, writeErr := tmpFile.WriteString(strings.TrimSpace(tokenValue))
				tmpFile.Close()
				if writeErr != nil {
					os.Remove(tmpFile.Name())
					wizardErr = fmt.Errorf("failed to write temp file: %w", writeErr)
					done = true
					continue
				}
				defer os.Remove(tmpFile.Name())
				filePath = tmpFile.Name()
				done = true
			}
		}
	}

	// Exit alt screen before validation/upload output
	fmt.Print("\x1b[?1049l")

	if wizardErr != nil {
		return wizardErr
	}
	if step < 0 {
		return nil // user cancelled
	}

	return doStore(name, entryType, filePath, fromEnv)
}

// stepSSHKeySelect shows the SSH key picker. Sets filePath if a key is selected.
// Returns huh.ErrUserAborted on Escape, nil otherwise.
func stepSSHKeySelect(filePath *string) error {
	keys := listSSHPrivateKeys()
	if len(keys) == 0 {
		// No keys found — will fall through to manual path input
		return nil
	}

	options := make([]huh.Option[string], 0, len(keys)+1)
	for _, k := range keys {
		options = append(options, huh.NewOption(k, k))
	}
	const manualEntry = ":manual:"
	options = append(options, huh.NewOption(styleDim.Render("Enter path manually..."), manualEntry))

	selected := keys[0] // pre-select first key
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Select SSH private key").
				Options(options...).
				Value(&selected),
		),
	).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())
	if err := form.Run(); err != nil {
		return err
	}
	if selected != manualEntry {
		*filePath = selected
	}
	return nil
}

// doStore performs the actual store operation (shared by interactive and non-interactive modes).
func doStore(name, entryType, filePath, fromEnv string) error {
	// Validate type
	if entryType != safe.TypeSSHKey && entryType != safe.TypeToken {
		return fmt.Errorf("invalid type %q; must be 'ssh-key' or 'token'", entryType)
	}

	// --file and --from-env are mutually exclusive
	if filePath != "" && fromEnv != "" {
		return fmt.Errorf("--file and --from-env are mutually exclusive")
	}

	token, err := loadToken()
	if err != nil {
		return err
	}

	var content string

	switch {
	case filePath != "":
		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("failed to read file: %w", err)
		}
		content = string(data)

	case fromEnv != "":
		if entryType != safe.TypeToken {
			return fmt.Errorf("--from-env is only supported with --type token")
		}
		content = os.Getenv(fromEnv)
		if content == "" {
			return fmt.Errorf("environment variable %s is not set or empty", fromEnv)
		}

	default:
		// No file or env: try auto-detect for tokens
		if entryType == safe.TypeToken {
			content, err = autoDetectToken(name)
			if err != nil {
				return err
			}
		} else {
			return fmt.Errorf("--file is required for --type ssh-key")
		}
	}

	// Size check
	if len(content) > safe.MaxSecretSize {
		return fmt.Errorf("secret exceeds %d byte limit (%d bytes)", safe.MaxSecretSize, len(content))
	}

	// Type-specific validation
	if entryType == safe.TypeSSHKey {
		if err := safe.ValidateSSHPrivateKey([]byte(content)); err != nil {
			return fmt.Errorf("SSH key validation failed: %w", err)
		}

		// Host verification
		fmt.Printf("🔑 Verifying SSH key against %s...\n", name)
		tmpFile, err := os.CreateTemp("", "exosafe-key-*")
		if err == nil {
			tmpPath := tmpFile.Name()
			_ = os.WriteFile(tmpPath, []byte(content), 0600)
			tmpFile.Close()
			defer os.Remove(tmpPath)

			if warning := safe.WarnSSHKeyAgainstHost(tmpPath, name); warning != "" {
				fmt.Printf("⚠️  %s\n", warning)
				if !storeYes {
					fmt.Print("Store anyway? [y/N] ")
					reader := bufio.NewReader(os.Stdin)
					answer, _ := reader.ReadString('\n')
					answer = strings.TrimSpace(strings.ToLower(answer))
					if answer != "y" && answer != "yes" {
						return fmt.Errorf("aborted: SSH key not accepted by %s", name)
					}
				}
			} else {
				fmt.Printf("✅ SSH key accepted by %s\n", name)
			}
		}
	}

	// Token verification
	if entryType == safe.TypeToken {
		fmt.Printf("🎫 Verifying token against %s...\n", name)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		provider, verifyErr := gitprovider.VerifyToken(ctx, name, content)
		cancel()
		if verifyErr != nil {
			fmt.Printf("⚠️  %s\n", verifyErr)
			if !storeYes {
				fmt.Print("Store anyway? [y/N] ")
				reader := bufio.NewReader(os.Stdin)
				answer, _ := reader.ReadString('\n')
				answer = strings.TrimSpace(strings.ToLower(answer))
				if answer != "y" && answer != "yes" {
					return fmt.Errorf("aborted: token not accepted by %s", name)
				}
			}
		} else {
			fmt.Printf("✅ Token accepted by %s (detected: %s)\n", name, provider)
		}
	}

	client := safe.NewClient()
	if err := client.Store(token, name, entryType, content); err != nil {
		return err
	}

	fmt.Printf("Stored '%s' (type: %s) in ExoSafe.\n", name, entryType)
	return nil
}

// autoDetectToken checks well-known env vars based on the entry name and
// prompts the user for confirmation.
func autoDetectToken(name string) (string, error) {
	envVar, ok := wellKnownTokenEnvVars[name]
	if !ok {
		return "", fmt.Errorf("no --file or --from-env specified; for '%s' no well-known env var is configured", name)
	}

	value := os.Getenv(envVar)
	if value == "" {
		return "", fmt.Errorf("no --file or --from-env specified; $%s is not set", envVar)
	}

	fmt.Printf("Use $%s? [Y/n] ", envVar)
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer != "" && answer != "y" && answer != "yes" {
		return "", fmt.Errorf("cancelled")
	}

	return value, nil
}

// promptTokenSource presents a select with detected env vars and manual options.
// Returns "env:<VAR_NAME>" or "paste".
func promptTokenSource() (string, error) {
	available := listAvailableTokenEnvVars()

	options := make([]huh.Option[string], 0, len(available)+2)
	for _, ev := range available {
		options = append(options, huh.NewOption(fmt.Sprintf("$%s", ev), "env:"+ev))
	}
	options = append(options, huh.NewOption(styleDim.Render("Enter environment variable name..."), "env:custom"))
	options = append(options, huh.NewOption(styleOrange.Render("Paste token directly"), "paste"))

	var source string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("How do you want to provide the token?").
				Options(options...).
				Value(&source),
		),
	).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())
	if err := form.Run(); err != nil {
		return "", err
	}

	if source == "env:custom" {
		var envName string
		inputForm := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Environment variable name").
					Placeholder("e.g. GITHUB_TOKEN").
					Value(&envName).
					Validate(func(s string) error {
						s = strings.TrimSpace(s)
						if s == "" {
							return fmt.Errorf("variable name is required")
						}
						if os.Getenv(s) == "" {
							return fmt.Errorf("$%s is not set or empty", s)
						}
						return nil
					}),
			),
		).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())
		if err := inputForm.Run(); err != nil {
			return "", err
		}
		return "env:" + strings.TrimSpace(envName), nil
	}

	return source, nil
}

// listAvailableTokenEnvVars returns well-known token env vars that are currently set.
func listAvailableTokenEnvVars() []string {
	allVars := []string{
		"GITHUB_TOKEN", "GITLAB_TOKEN", "GITEA_TOKEN",
		"EXOHUB_GITHUB_TOKEN", "EXOHUB_GITLAB_TOKEN", "EXOHUB_GITEA_TOKEN",
	}
	var available []string
	for _, v := range allVars {
		if os.Getenv(v) != "" {
			available = append(available, v)
		}
	}
	return available
}

// listSSHPrivateKeys scans ~/.ssh/ for files that look like SSH private keys.
func listSSHPrivateKeys() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	sshDir := filepath.Join(home, ".ssh")
	entries, err := os.ReadDir(sshDir)
	if err != nil {
		return nil
	}

	var keys []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		// Skip known non-key files and public keys
		name := entry.Name()
		if strings.HasSuffix(name, ".pub") ||
			name == "known_hosts" ||
			name == "authorized_keys" ||
			name == "config" ||
			strings.HasSuffix(name, ".old") {
			continue
		}
		// Check if the file starts with a PEM header
		path := filepath.Join(sshDir, name)
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		buf := make([]byte, 40)
		n, _ := f.Read(buf)
		f.Close()
		if n > 0 && strings.Contains(string(buf[:n]), "-----BEGIN") {
			keys = append(keys, path)
		}
	}
	return keys
}

// pullResult is the JSON output for `exo safe pull --json`.
type pullResult struct {
	SSHKeys        int `json:"ssh_keys"`
	GitCredentials int `json:"git_credentials"`
}

func newPullCommand() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Provision credentials from ExoSafe (non-interactive)",
		Long: `Fetch and provision credentials from ExoSafe without any interactive prompt.

Reads the bearer token via the standard precedence:
  EXO_TOKEN_FILE > EXO_CONFIG_DIR credentials/token.json > system default

Provisions SSH keys to <EXO_CONFIG_DIR>/exo/ssh/ and git tokens to
<EXO_CONFIG_DIR>/exo/git-credentials.

Exits non-zero if the token is missing or expired, or if the safe is empty.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPull(jsonOutput)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	return cmd
}

func runPull(jsonOutput bool) error {
	token, err := loadToken()
	if err != nil {
		return err
	}

	client := safe.NewClient()
	entries, err := client.GetAll(token)
	if err != nil {
		return fmt.Errorf("ExoSafe retrieval failed: %w", err)
	}

	if len(entries) == 0 {
		return fmt.Errorf("ExoSafe is empty; store credentials first with 'exo safe store <host> --type <ssh-key|token>'")
	}

	var sshEntries, tokenEntries []safe.Entry
	for _, e := range entries {
		switch e.Type {
		case safe.TypeSSHKey:
			sshEntries = append(sshEntries, e)
		case safe.TypeToken:
			tokenEntries = append(tokenEntries, e)
		}
	}

	nSSH, err := safe.ProvisionSSHKeys(sshEntries)
	if err != nil {
		return fmt.Errorf("SSH key provisioning failed: %w", err)
	}

	nToken, err := safe.ProvisionGitCredentials(tokenEntries)
	if err != nil {
		return fmt.Errorf("git credential provisioning failed: %w", err)
	}

	if jsonOutput {
		out, _ := json.Marshal(pullResult{SSHKeys: nSSH, GitCredentials: nToken})
		fmt.Println(string(out))
		return nil
	}

	if nSSH > 0 {
		fmt.Printf("🔑 Provisioned %d SSH key(s).\n", nSSH)
	}
	if nToken > 0 {
		fmt.Printf("🎫 Provisioned %d git credential(s).\n", nToken)
	}
	return nil
}

// expandHome expands a leading ~ to the user's home directory.
func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return home + path[1:]
	}
	return path
}

func newListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List credentials in ExoSafe",
		Args:  cobra.NoArgs,
		RunE:  runList,
	}
}

func runList(cmd *cobra.Command, args []string) error {
	token, err := loadToken()
	if err != nil {
		return err
	}

	client := safe.NewClient()
	entries, err := client.List(token)
	if err != nil {
		return err
	}

	if len(entries) == 0 {
		fmt.Println("ExoSafe is empty.")
		return nil
	}

	nameStyle := lipgloss.NewStyle().Foreground(commandutil.ColorPink)
	typeStyle := lipgloss.NewStyle().Foreground(commandutil.ColorDim)
	for _, entry := range entries {
		padded := fmt.Sprintf("%-35s", entry.Name)
		fmt.Printf("%s %s\n", nameStyle.Render(padded), typeStyle.Render(typeIcon(entry.Type)+" "+entry.Type))
	}
	return nil
}

func newShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show [name]",
		Short: "Show a credential from ExoSafe",
		Long: `Show the value of a credential stored in ExoSafe.

Run without arguments to interactively select which credential to show.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runShow,
	}
}

func runShow(cmd *cobra.Command, args []string) error {
	token, err := loadToken()
	if err != nil {
		return err
	}

	client := safe.NewClient()
	entries, err := client.GetAll(token)
	if err != nil {
		return err
	}

	if len(entries) == 0 {
		fmt.Println("ExoSafe is empty.")
		return nil
	}

	var name string
	if len(args) > 0 {
		name = args[0]
	} else {
		// Interactive select
		options := make([]huh.Option[string], 0, len(entries))
		for _, e := range entries {
			label := fmt.Sprintf("%-30s %s", e.Name, styleDim.Render(typeIcon(e.Type)+" "+e.Type))
			options = append(options, huh.NewOption(label, e.Name))
		}

		form := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Select credential to show").
					Options(options...).
					Value(&name),
			),
		).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())
		if err := form.Run(); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return nil
			}
			return err
		}
	}

	// Find the entry
	nameStyle := lipgloss.NewStyle().Foreground(commandutil.ColorPink).Bold(true)
	typeStyle := lipgloss.NewStyle().Foreground(commandutil.ColorDim)
	labelStyle := lipgloss.NewStyle().Foreground(commandutil.ColorDarkOrange).Bold(true)

	for _, e := range entries {
		if e.Name == name {
			fmt.Printf("%s %s  %s %s\n", labelStyle.Render("Name:"), nameStyle.Render(e.Name), labelStyle.Render("Type:"), typeStyle.Render(e.Type))
			fmt.Println(labelStyle.Render("Value:"))
			fmt.Println(e.Value)
			return nil
		}
	}

	return fmt.Errorf("secret '%s' not found", name)
}

func newDeleteCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "delete [name]",
		Short: "Delete a credential from ExoSafe",
		Long: `Delete a credential from ExoSafe.

Run without arguments to interactively select which credential to delete.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runDelete,
	}
}

func runDelete(cmd *cobra.Command, args []string) error {
	token, err := loadToken()
	if err != nil {
		return err
	}

	var name, entryType string
	if len(args) > 0 {
		name = args[0]
		// Need to look up the type from the API
		client := safe.NewClient()
		entries, err := client.List(token)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.Name == name {
				entryType = e.Type
				break
			}
		}
		if entryType == "" {
			return fmt.Errorf("secret '%s' not found in ExoSafe", name)
		}
	} else {
		// Interactive: fetch entries and let user pick
		client := safe.NewClient()
		entries, err := client.List(token)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			fmt.Println("ExoSafe is empty.")
			return nil
		}

		// Encode type+name as "type/name" in the value
		options := make([]huh.Option[string], 0, len(entries))
		for _, e := range entries {
			label := fmt.Sprintf("%-30s %s", e.Name, styleDim.Render(typeIcon(e.Type)+" "+e.Type))
			options = append(options, huh.NewOption(label, e.Type+"/"+e.Name))
		}

		var selected string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Select credential to delete").
					Options(options...).
					Value(&selected),
			),
		).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())
		if err := form.Run(); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return nil
			}
			return err
		}
		parts := strings.SplitN(selected, "/", 2)
		entryType, name = parts[0], parts[1]
	}

	// Confirmation
	var confirm bool
	confirmForm := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Delete '%s' (%s) from ExoSafe?", name, entryType)).
				Value(&confirm),
		),
	).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())
	if err := confirmForm.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return nil
		}
		return err
	}
	if !confirm {
		fmt.Println("Cancelled.")
		return nil
	}

	client := safe.NewClient()
	if err := client.Delete(token, entryType, name); err != nil {
		return err
	}

	fmt.Printf("Deleted '%s' (%s) from ExoSafe.\n", name, entryType)
	return nil
}

func newClearCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Remove all credentials from ExoSafe",
		RunE:  runClear,
	}
}

func runClear(cmd *cobra.Command, args []string) error {
	token, err := loadToken()
	if err != nil {
		return err
	}

	client := safe.NewClient()
	entries, err := client.List(token)
	if err != nil {
		return err
	}

	if len(entries) == 0 {
		fmt.Println("ExoSafe is already empty.")
		return nil
	}

	// Show what will be deleted
	fmt.Printf("⚠️  This will delete %d credential(s) from ExoSafe:\n\n", len(entries))
	nameStyle := lipgloss.NewStyle().Foreground(commandutil.ColorPink)
	typeStyle := lipgloss.NewStyle().Foreground(commandutil.ColorDim)
	for _, e := range entries {
		padded := fmt.Sprintf("%-35s", e.Name)
		fmt.Printf("  %s %s\n", nameStyle.Render(padded), typeStyle.Render(typeIcon(e.Type)+" "+e.Type))
	}
	fmt.Println()

	// Confirmation
	var confirm bool
	confirmForm := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("🚨 Delete ALL credentials from ExoSafe?").
				Value(&confirm),
		),
	).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())
	if err := confirmForm.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return nil
		}
		return err
	}
	if !confirm {
		fmt.Println("Cancelled.")
		return nil
	}

	// Delete all
	deleted := 0
	for _, e := range entries {
		if err := client.Delete(token, e.Type, e.Name); err != nil {
			fmt.Printf("⚠️  Failed to delete '%s': %v\n", e.Name, err)
		} else {
			deleted++
		}
	}

	fmt.Printf("🗑️  Cleared %d/%d credential(s) from ExoSafe.\n", deleted, len(entries))
	return nil
}

// dumpEntry is the JSON format for dump/restore files.
type dumpEntry struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

func newDumpCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "dump <file>",
		Short: "Dump all credentials to a file",
		Long: `Export all ExoSafe credentials to a JSON file for backup or migration.

The file contains secrets in plaintext — handle with care.`,
		Args: cobra.ExactArgs(1),
		RunE: runDump,
	}
}

func runDump(cmd *cobra.Command, args []string) error {
	outPath := args[0]

	token, err := loadToken()
	if err != nil {
		return err
	}

	client := safe.NewClient()
	entries, err := client.GetAll(token)
	if err != nil {
		return err
	}

	if len(entries) == 0 {
		fmt.Println("ExoSafe is empty, nothing to dump.")
		return nil
	}

	dump := make([]dumpEntry, 0, len(entries))
	for _, e := range entries {
		dump = append(dump, dumpEntry{Name: e.Name, Type: e.Type, Value: e.Value})
	}

	data, err := json.MarshalIndent(dump, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal dump: %w", err)
	}

	if err := os.WriteFile(outPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write dump file: %w", err)
	}

	fmt.Printf("📦 Dumped %d credential(s) to %s\n", len(dump), outPath)
	return nil
}

func newRestoreCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "restore <file>",
		Short: "Restore credentials from a dump file",
		Long: `Import ExoSafe credentials from a JSON dump file.

Existing credentials with the same name and type will be overwritten.`,
		Args: cobra.ExactArgs(1),
		RunE: runRestore,
	}
}

func runRestore(cmd *cobra.Command, args []string) error {
	inPath := args[0]

	token, err := loadToken()
	if err != nil {
		return err
	}

	data, err := os.ReadFile(inPath)
	if err != nil {
		return fmt.Errorf("failed to read dump file: %w", err)
	}

	var entries []dumpEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("failed to parse dump file: %w", err)
	}

	if len(entries) == 0 {
		fmt.Println("Dump file is empty, nothing to restore.")
		return nil
	}

	// Show what will be restored
	fmt.Printf("📦 Restoring %d credential(s) from %s:\n\n", len(entries), inPath)
	nameStyle := lipgloss.NewStyle().Foreground(commandutil.ColorPink)
	typeStyle := lipgloss.NewStyle().Foreground(commandutil.ColorDim)
	for _, e := range entries {
		padded := fmt.Sprintf("%-35s", e.Name)
		fmt.Printf("  %s %s\n", nameStyle.Render(padded), typeStyle.Render(typeIcon(e.Type)+" "+e.Type))
	}
	fmt.Println()

	var confirm bool
	confirmForm := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Restore these credentials to ExoSafe?").
				Value(&confirm),
		),
	).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())
	if err := confirmForm.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return nil
		}
		return err
	}
	if !confirm {
		fmt.Println("Cancelled.")
		return nil
	}

	client := safe.NewClient()
	restored := 0
	for _, e := range entries {
		if err := client.Store(token, e.Name, e.Type, e.Value); err != nil {
			fmt.Printf("⚠️  Failed to restore '%s': %v\n", e.Name, err)
		} else {
			restored++
		}
	}

	fmt.Printf("✅ Restored %d/%d credential(s) to ExoSafe.\n", restored, len(entries))
	return nil
}
