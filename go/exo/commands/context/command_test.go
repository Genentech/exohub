package context

import (
	"bytes"
	"os"
	"testing"
)

// executeCommand runs the context command with the given args and captures output.
func executeCommand(args ...string) (string, error) {
	cmd := NewCommand()

	// Capture output
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)

	// Silence usage on error so we only get error messages
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	err := cmd.Execute()
	return buf.String(), err
}

func TestCommandCreate(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	_, err := executeCommand("create", "myctx", "--host", "https://git.example.com", "--org", "myorg")
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// Verify context was created
	ctx, err := LoadContext("myctx")
	if err != nil {
		t.Fatalf("LoadContext failed: %v", err)
	}
	if ctx.Host != "https://git.example.com" {
		t.Errorf("host = %q, want %q", ctx.Host, "https://git.example.com")
	}
	if ctx.Org != "myorg" {
		t.Errorf("org = %q, want %q", ctx.Org, "myorg")
	}
}

func TestCommandCreateWithDescription(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	_, err := executeCommand("create", "described", "--host", "https://git.example.com", "--org", "org",
		"--description", "My production context")
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	ctx, err := LoadContext("described")
	if err != nil {
		t.Fatalf("LoadContext failed: %v", err)
	}
	if ctx.Description != "My production context" {
		t.Errorf("description = %q, want %q", ctx.Description, "My production context")
	}
}

func TestCommandCreateWithAllFields(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	_, err := executeCommand("create", "full",
		"--host", "https://git.example.com",
		"--org", "myorg",
		"--provider", "gitlab",
		"--template", "my-template",
		"--repo", "default-repo",
		"--description", "Full context",
	)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	ctx, err := LoadContext("full")
	if err != nil {
		t.Fatalf("LoadContext failed: %v", err)
	}
	if ctx.Provider != "gitlab" {
		t.Errorf("provider = %q, want %q", ctx.Provider, "gitlab")
	}
	if ctx.Template != "my-template" {
		t.Errorf("template = %q, want %q", ctx.Template, "my-template")
	}
	if ctx.Repo != "default-repo" {
		t.Errorf("repo = %q, want %q", ctx.Repo, "default-repo")
	}
	if ctx.Description != "Full context" {
		t.Errorf("description = %q, want %q", ctx.Description, "Full context")
	}
}

func TestCommandCreateMissingHost(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	_, err := executeCommand("create", "bad", "--org", "myorg")
	if err == nil {
		t.Fatal("expected error for missing --host")
	}
}

func TestCommandCreateMissingOrg(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	_, err := executeCommand("create", "bad", "--host", "https://example.com")
	if err == nil {
		t.Fatal("expected error for missing --org")
	}
}

func TestCommandCreateDuplicate(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	_, err := executeCommand("create", "dup", "--host", "https://example.com", "--org", "org")
	if err != nil {
		t.Fatalf("first create failed: %v", err)
	}

	_, err = executeCommand("create", "dup", "--host", "https://example.com", "--org", "org")
	if err == nil {
		t.Fatal("expected error for duplicate create")
	}
}

func TestCommandListNamesOnly(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// Create contexts
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if err := SaveContext(&Context{Name: name, Host: "https://example.com", Org: name + "-org"}); err != nil {
			t.Fatalf("SaveContext(%s) failed: %v", name, err)
		}
	}

	// list uses fmt.Println (stdout), so we just verify it succeeds
	_, err := executeCommand("list")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}

	// Verify the underlying data is correct
	names, err := ListContexts()
	if err != nil {
		t.Fatalf("ListContexts failed: %v", err)
	}
	if len(names) != 3 {
		t.Fatalf("expected 3 contexts, got %d", len(names))
	}
}

func TestCommandListEmpty(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// Empty list should not error
	_, err := executeCommand("list")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
}

func TestCommandShow(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := SaveContext(&Context{
		Name:        "showme",
		Host:        "https://git.example.com",
		Org:         "myorg",
		Provider:    "gitea",
		Template:    "my-template",
		Description: "Test description",
	}); err != nil {
		t.Fatalf("SaveContext failed: %v", err)
	}

	// show uses fmt.Printf (stdout), so we verify no error and the context is loadable
	_, err := executeCommand("show", "showme")
	if err != nil {
		t.Fatalf("show failed: %v", err)
	}

	// Verify the context data is correct (show reads via LoadContext)
	ctx, err := LoadContext("showme")
	if err != nil {
		t.Fatalf("LoadContext failed: %v", err)
	}
	if ctx.Description != "Test description" {
		t.Errorf("description = %q, want %q", ctx.Description, "Test description")
	}
}

func TestCommandShowWithContextFlag(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := SaveContext(&Context{
		Name: "flagctx",
		Host: "https://git.example.com",
		Org:  "flagorg",
	}); err != nil {
		t.Fatalf("SaveContext failed: %v", err)
	}

	// Set OverrideContext to simulate --context flag
	OverrideContext = "flagctx"
	t.Cleanup(func() { OverrideContext = "" })

	_, err := executeCommand("show")
	if err != nil {
		t.Fatalf("show with --context flag should succeed: %v", err)
	}
}

func TestCommandShowWithEnvVar(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := SaveContext(&Context{
		Name: "envctx",
		Host: "https://git.example.com",
		Org:  "envorg",
	}); err != nil {
		t.Fatalf("SaveContext failed: %v", err)
	}

	OverrideContext = ""
	t.Cleanup(func() { OverrideContext = "" })
	t.Setenv("EXOHUB_CONTEXT", "envctx")

	// show uses fmt.Printf (stdout) not cmd.OutOrStdout, so we just verify no error
	_, err := executeCommand("show")
	if err != nil {
		t.Fatalf("show with EXOHUB_CONTEXT should succeed: %v", err)
	}
}

func TestCommandShowNotFound(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	_, err := executeCommand("show", "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent context")
	}
}

func TestCommandShowNoArgs(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	OverrideContext = ""
	t.Cleanup(func() { OverrideContext = "" })
	os.Unsetenv("EXOHUB_CONTEXT")

	_, err := executeCommand("show")
	if err == nil {
		t.Fatal("expected error when no context specified")
	}
}

func TestCommandUpdate(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := SaveContext(&Context{
		Name: "updatable",
		Host: "https://old.example.com",
		Org:  "oldorg",
	}); err != nil {
		t.Fatalf("SaveContext failed: %v", err)
	}

	_, err := executeCommand("update", "updatable", "--host", "https://new.example.com", "--description", "Updated")
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}

	ctx, err := LoadContext("updatable")
	if err != nil {
		t.Fatalf("LoadContext failed: %v", err)
	}
	if ctx.Host != "https://new.example.com" {
		t.Errorf("host = %q, want %q", ctx.Host, "https://new.example.com")
	}
	if ctx.Org != "oldorg" {
		t.Errorf("org should be unchanged, got %q", ctx.Org)
	}
	if ctx.Description != "Updated" {
		t.Errorf("description = %q, want %q", ctx.Description, "Updated")
	}
}

func TestCommandUpdateNonexistent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	_, err := executeCommand("update", "ghost", "--host", "https://example.com")
	if err == nil {
		t.Fatal("expected error for nonexistent context")
	}
}

func TestCommandDelete(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := SaveContext(&Context{Name: "deleteme", Host: "https://example.com", Org: "org"}); err != nil {
		t.Fatalf("SaveContext failed: %v", err)
	}

	_, err := executeCommand("delete", "deleteme")
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	exists, _ := ContextExists("deleteme")
	if exists {
		t.Error("context should be deleted")
	}
}

func TestCommandDeleteNonexistent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	_, err := executeCommand("delete", "ghost")
	if err == nil {
		t.Fatal("expected error for nonexistent context")
	}
}
