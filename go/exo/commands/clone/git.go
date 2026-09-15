package clone

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Genentech/exohub/go/exo/commandutil"
)

// shallowCloneExohub performs a shallow clone of only the .exohub/ directory
func shallowCloneExohub(repoURL, destDir string) error {
	// Clone with --no-checkout instead of --sparse to avoid a git <=2.25 bug
	// where sparse-checkout init incorrectly uses the repo URL as a directory path.
	// We use --filter=tree:0 for maximum efficiency (skips tree objects entirely,
	// crucial for repos with 500k+ files).
	cmd := exec.Command("git", "clone", "--depth=1", "--filter=tree:0", "--no-checkout", "--progress", repoURL, destDir)
	commandutil.InjectGitSSHEnv(cmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to clone repository: %w", err)
	}

	// Initialize and configure sparse-checkout manually
	cmd = exec.Command("git", "sparse-checkout", "init", "--cone")
	cmd.Dir = destDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to initialize sparse checkout: %w\nOutput: %s", err, output)
	}

	// Set sparse checkout to only include .exohub/ using cone mode (more efficient)
	cmd = exec.Command("git", "sparse-checkout", "set", "--cone", ".exohub/")
	cmd.Dir = destDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(output), "Sparse checkout leaves no entry on working directory") {
			return fmt.Errorf("repository does not contain .exohub/ directory")
		}
		return fmt.Errorf("failed to set sparse checkout paths: %w\nOutput: %s", err, output)
	}

	// Checkout the sparse paths into the working directory
	cmd = exec.Command("git", "checkout")
	cmd.Dir = destDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to checkout sparse paths: %w", err)
	}

	return nil
}

// applyPreset applies a preset to an already cloned repository
func applyPreset(repoPath string, preset *ClonePreset) error {
	// Handle depth changes (unshallow if needed)
	if preset.Depth == 0 {
		// Full clone - check if already shallow before unshallowing
		isShallow, err := isShallowRepository(repoPath)
		if err != nil {
			return fmt.Errorf("failed to check if repository is shallow: %w", err)
		}

		if isShallow {
			fmt.Println("  Fetching full history...")
			cmd := exec.Command("git", "fetch", "--unshallow", "--progress")
			commandutil.InjectGitSSHEnv(cmd)
			cmd.Dir = repoPath
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("failed to unshallow repository: %w", err)
			}
		} else {
			fmt.Println("  Repository already has full history...")
		}
	} else if preset.Depth > 1 {
		// Specific depth - deepen the clone
		fmt.Printf("  Deepening clone to depth %d...\n", preset.Depth)
		cmd := exec.Command("git", "fetch", fmt.Sprintf("--depth=%d", preset.Depth), "--progress")
		commandutil.InjectGitSSHEnv(cmd)
		cmd.Dir = repoPath
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to deepen repository: %w", err)
		}
	}

	// Apply sparse checkout paths
	if len(preset.SparsePaths) > 0 {
		fmt.Println("  Applying sparse checkout...")
		// Build the sparse checkout path list (always include .exohub/)
		paths := append([]string{".exohub/"}, preset.SparsePaths...)

		// Re-initialize sparse-checkout in non-cone mode to allow both files
		// and directories. The initial clone uses cone mode (directories only),
		// and git <=2.25 doesn't switch core.sparseCheckoutCone via set --no-cone.
		cmd := exec.Command("git", "sparse-checkout", "init", "--no-cone")
		cmd.Dir = repoPath
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to reinitialize sparse checkout: %w\nOutput: %s", err, output)
		}

		args := append([]string{"sparse-checkout", "set", "--no-cone"}, paths...)
		cmd = exec.Command("git", args...)
		cmd.Dir = repoPath
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to set sparse checkout paths: %w", err)
		}
	} else {
		// No sparse paths = full checkout
		fmt.Println("  Checking out all files...")
		cmd := exec.Command("git", "sparse-checkout", "disable")
		cmd.Dir = repoPath
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to disable sparse checkout: %w", err)
		}
	}

	// Apply git config settings
	if len(preset.GitConfig) > 0 {
		fmt.Println("  Applying git config...")
		for key, value := range preset.GitConfig {
			cmd := exec.Command("git", "config", key, value)
			cmd.Dir = repoPath
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("failed to set git config %s=%s: %w", key, value, err)
			}
		}
	}

	return nil
}

// getCurrentCommit returns the current commit SHA
func getCurrentCommit(repoPath string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get current commit: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// getCurrentBranch returns the current branch name
func getCurrentBranch(repoPath string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get current branch: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// extractRepoName extracts the repository name from a git URL
func extractRepoName(repoURL string) string {
	// Remove trailing slash
	repoURL = strings.TrimSuffix(repoURL, "/")

	// Get the last part of the URL
	parts := strings.Split(repoURL, "/")
	name := parts[len(parts)-1]

	// Remove .git suffix if present
	name = strings.TrimSuffix(name, ".git")

	return name
}

// checkExohubExists checks if .exohub/ directory exists in the repository
func checkExohubExists(repoPath string) bool {
	exohubPath := filepath.Join(repoPath, ".exohub")
	info, err := os.Stat(exohubPath)
	return err == nil && info.IsDir()
}

// fullCloneDirect performs a standard full clone (like git clone)
func fullCloneDirect(repoURL, destDir string) error {
	cmd := exec.Command("git", "clone", "--progress", repoURL, destDir)
	commandutil.InjectGitSSHEnv(cmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone failed: %w", err)
	}
	return nil
}

// disableSparseCheckout disables sparse checkout to prepare for full clone
func disableSparseCheckout(repoPath string) error {
	cmd := exec.Command("git", "sparse-checkout", "disable")
	cmd.Dir = repoPath
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git sparse-checkout disable failed: %w\nOutput: %s", err, output)
	}
	return nil
}

// isShallowRepository checks if the git repository is a shallow clone
func isShallowRepository(repoPath string) (bool, error) {
	shallowFile := filepath.Join(repoPath, ".git", "shallow")
	_, err := os.Stat(shallowFile)
	if err == nil {
		return true, nil // .git/shallow exists = shallow repo
	}
	if os.IsNotExist(err) {
		return false, nil // .git/shallow doesn't exist = full repo
	}
	return false, err // some other error
}

// fetchFullRepository fetches the complete repository history and files
func fetchFullRepository(repoPath string) error {
	// Check if repository is shallow before trying to unshallow
	isShallow, err := isShallowRepository(repoPath)
	if err != nil {
		return fmt.Errorf("failed to check if repository is shallow: %w", err)
	}

	if isShallow {
		// Only unshallow if the repository is actually shallow
		fmt.Println("  Fetching full history...")
		cmd := exec.Command("git", "fetch", "--unshallow", "--progress")
		commandutil.InjectGitSSHEnv(cmd)
		cmd.Dir = repoPath
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to unshallow repository: %w", err)
		}
	} else {
		fmt.Println("  Repository already has full history...")
	}

	// Fetch all objects (this will get all missing blobs and trees)
	fmt.Println("  Fetching all objects...")
	cmd := exec.Command("git", "fetch", "--all", "--progress")
	commandutil.InjectGitSSHEnv(cmd)
	cmd.Dir = repoPath
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git fetch --all failed: %w", err)
	}

	// Checkout to populate the working directory with all files
	fmt.Println("  Checking out files...")
	cmd = exec.Command("git", "checkout", "HEAD")
	cmd.Dir = repoPath
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git checkout failed: %w", err)
	}

	return nil
}
