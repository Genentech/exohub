package context

import (
	"os"
	"path/filepath"
	"testing"
)

func TestContextValidation(t *testing.T) {
	tests := []struct {
		name    string
		ctx     *Context
		wantErr bool
	}{
		{
			name: "valid context",
			ctx: &Context{
				Name: "test",
				Host: "https://example.com/",
				Org:  "testorg",
			},
			wantErr: false,
		},
		{
			name: "missing name",
			ctx: &Context{
				Host: "https://example.com/",
				Org:  "testorg",
			},
			wantErr: true,
		},
		{
			name: "missing host",
			ctx: &Context{
				Name: "test",
				Org:  "testorg",
			},
			wantErr: true,
		},
		{
			name: "missing org",
			ctx: &Context{
				Name: "test",
				Host: "https://example.com/",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateContext(tt.ctx)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateContext() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSaveAndLoadContext(t *testing.T) {
	// Create temporary config directory
	tmpDir := t.TempDir()
	os.Setenv("XDG_CONFIG_HOME", tmpDir)
	defer os.Unsetenv("XDG_CONFIG_HOME")

	ctx := &Context{
		Name: "test-context",
		Host: "https://git.example.com/",
		Org:  "testorg",
		Repo: "myrepo",
	}

	// Save context
	if err := SaveContext(ctx); err != nil {
		t.Fatalf("SaveContext() error = %v", err)
	}

	// Verify file exists
	configDir, _ := GetConfigDir()
	filePath := filepath.Join(configDir, "test-context.yaml")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Errorf("Context file was not created")
	}

	// Load context
	loaded, err := LoadContext("test-context")
	if err != nil {
		t.Fatalf("LoadContext() error = %v", err)
	}

	// Verify loaded context matches
	if loaded.Name != ctx.Name {
		t.Errorf("Name mismatch: got %v, want %v", loaded.Name, ctx.Name)
	}
	if loaded.Host != ctx.Host {
		t.Errorf("Host mismatch: got %v, want %v", loaded.Host, ctx.Host)
	}
	if loaded.Org != ctx.Org {
		t.Errorf("Org mismatch: got %v, want %v", loaded.Org, ctx.Org)
	}
	if loaded.Repo != ctx.Repo {
		t.Errorf("Repo mismatch: got %v, want %v", loaded.Repo, ctx.Repo)
	}
}

func TestGetCurrentContext(t *testing.T) {
	// Create temporary config directory
	tmpDir := t.TempDir()
	os.Setenv("XDG_CONFIG_HOME", tmpDir)
	defer os.Unsetenv("XDG_CONFIG_HOME")

	// Create a context and use override
	testCtx := &Context{
		Name: "test-context",
		Host: "https://git.example.com/",
		Org:  "testorg",
	}
	if err := SaveContext(testCtx); err != nil {
		t.Fatalf("SaveContext() error = %v", err)
	}
	OverrideContext = "test-context"
	t.Cleanup(func() { OverrideContext = "" })

	// Should return override context
	ctx, err := GetCurrentContext()
	if err != nil {
		t.Fatalf("GetCurrentContext() error = %v", err)
	}
	if ctx.Host != testCtx.Host {
		t.Errorf("Expected host %v, got %v", testCtx.Host, ctx.Host)
	}
	if ctx.Org != testCtx.Org {
		t.Errorf("Expected org %v, got %v", testCtx.Org, ctx.Org)
	}
}

func TestDeleteContext(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("XDG_CONFIG_HOME", tmpDir)
	defer os.Unsetenv("XDG_CONFIG_HOME")

	// Create a test context
	ctx := &Context{
		Name: "test-context",
		Host: "https://git.example.com/",
		Org:  "testorg",
	}
	if err := SaveContext(ctx); err != nil {
		t.Fatalf("SaveContext() error = %v", err)
	}

	// Delete existing context
	if err := DeleteContext("test-context"); err != nil {
		t.Errorf("DeleteContext() error = %v", err)
	}

	// Verify file is removed
	exists, _ := ContextExists("test-context")
	if exists {
		t.Errorf("Context file should be deleted")
	}

	// Delete non-existent context should fail
	err := DeleteContext("non-existent")
	if err == nil {
		t.Errorf("DeleteContext() should fail for non-existent context")
	}
}

func TestListContexts(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("XDG_CONFIG_HOME", tmpDir)
	defer os.Unsetenv("XDG_CONFIG_HOME")

	// Test empty directory
	names, err := ListContexts()
	if err != nil {
		t.Fatalf("ListContexts() error = %v", err)
	}
	if len(names) != 0 {
		t.Errorf("Expected empty list, got %v", names)
	}

	// Create multiple contexts
	contexts := []Context{
		{Name: "dev", Host: "https://git.dev.com/", Org: "devteam"},
		{Name: "prod", Host: "https://git.prod.com/", Org: "prodteam"},
		{Name: "staging", Host: "https://git.staging.com/", Org: "stageteam"},
	}

	for _, ctx := range contexts {
		if err := SaveContext(&ctx); err != nil {
			t.Fatalf("SaveContext() error = %v", err)
		}
	}

	// List should return all contexts
	names, err = ListContexts()
	if err != nil {
		t.Fatalf("ListContexts() error = %v", err)
	}
	if len(names) != 3 {
		t.Errorf("Expected 3 contexts, got %v", len(names))
	}

	// Verify names are correct
	nameMap := make(map[string]bool)
	for _, name := range names {
		nameMap[name] = true
	}
	for _, ctx := range contexts {
		if !nameMap[ctx.Name] {
			t.Errorf("Expected context %v in list", ctx.Name)
		}
	}
}

func TestListContextsIgnoresNonYaml(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("XDG_CONFIG_HOME", tmpDir)
	defer os.Unsetenv("XDG_CONFIG_HOME")

	configDir, _ := EnsureConfigDir()

	// Create a valid context
	ctx := &Context{Name: "valid", Host: "https://git.example.com/", Org: "testorg"}
	if err := SaveContext(ctx); err != nil {
		t.Fatalf("SaveContext() error = %v", err)
	}

	// Create non-yaml files
	os.WriteFile(filepath.Join(configDir, "ignore.txt"), []byte("ignore"), 0644)
	os.WriteFile(filepath.Join(configDir, ".selected"), []byte("valid"), 0644)
	os.WriteFile(filepath.Join(configDir, "README.md"), []byte("docs"), 0644)

	// List should only return yaml contexts
	names, err := ListContexts()
	if err != nil {
		t.Fatalf("ListContexts() error = %v", err)
	}
	if len(names) != 1 {
		t.Errorf("Expected 1 context, got %v: %v", len(names), names)
	}
	if names[0] != "valid" {
		t.Errorf("Expected 'valid', got %v", names[0])
	}
}

func TestUpdateContext(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("XDG_CONFIG_HOME", tmpDir)
	defer os.Unsetenv("XDG_CONFIG_HOME")

	// Create a context
	ctx := &Context{
		Name: "test-context",
		Host: "https://git.example.com/",
		Org:  "testorg",
	}
	if err := SaveContext(ctx); err != nil {
		t.Fatalf("SaveContext() error = %v", err)
	}

	// Update host
	ctx.Host = "https://git.newhost.com/"
	if err := SaveContext(ctx); err != nil {
		t.Errorf("SaveContext() error = %v", err)
	}

	// Verify update
	loaded, err := LoadContext("test-context")
	if err != nil {
		t.Fatalf("LoadContext() error = %v", err)
	}
	if loaded.Host != "https://git.newhost.com/" {
		t.Errorf("Expected updated host, got %v", loaded.Host)
	}
	if loaded.Org != "testorg" {
		t.Errorf("Expected org to remain, got %v", loaded.Org)
	}

	// Update org
	ctx.Org = "neworg"
	if err := SaveContext(ctx); err != nil {
		t.Errorf("SaveContext() error = %v", err)
	}

	// Verify update
	loaded, err = LoadContext("test-context")
	if err != nil {
		t.Fatalf("LoadContext() error = %v", err)
	}
	if loaded.Org != "neworg" {
		t.Errorf("Expected updated org, got %v", loaded.Org)
	}
}

func TestUpdateNonExistentContext(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("XDG_CONFIG_HOME", tmpDir)
	defer os.Unsetenv("XDG_CONFIG_HOME")

	// Try to load non-existent context
	_, err := LoadContext("non-existent")
	if err == nil {
		t.Errorf("LoadContext() should fail for non-existent context")
	}
}

func TestCreateDuplicateFails(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("XDG_CONFIG_HOME", tmpDir)
	defer os.Unsetenv("XDG_CONFIG_HOME")

	// Create a context
	ctx := &Context{
		Name: "test-context",
		Host: "https://git.example.com/",
		Org:  "testorg",
	}
	if err := SaveContext(ctx); err != nil {
		t.Fatalf("SaveContext() error = %v", err)
	}

	// Check if exists
	exists, err := ContextExists("test-context")
	if err != nil {
		t.Fatalf("ContextExists() error = %v", err)
	}
	if !exists {
		t.Errorf("Context should exist")
	}

	// Attempting to create again should be caught by the command
	// (the command checks ContextExists before calling SaveContext)
}

func TestLoadContextCorruptedYAML(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("XDG_CONFIG_HOME", tmpDir)
	defer os.Unsetenv("XDG_CONFIG_HOME")

	configDir, _ := EnsureConfigDir()

	// Create corrupted YAML file
	corruptedYAML := `name: test
host: invalid yaml without quotes: breaks here
org`
	yamlPath := filepath.Join(configDir, "corrupted.yaml")
	os.WriteFile(yamlPath, []byte(corruptedYAML), 0644)

	// Try to load corrupted context
	_, err := LoadContext("corrupted")
	if err == nil {
		t.Errorf("LoadContext() should fail for corrupted YAML")
	}
}

func TestSaveContextInvalidData(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("XDG_CONFIG_HOME", tmpDir)
	defer os.Unsetenv("XDG_CONFIG_HOME")

	tests := []struct {
		name string
		ctx  *Context
	}{
		{
			name: "empty name",
			ctx:  &Context{Name: "", Host: "https://example.com/", Org: "testorg"},
		},
		{
			name: "empty host",
			ctx:  &Context{Name: "test", Host: "", Org: "testorg"},
		},
		{
			name: "empty org",
			ctx:  &Context{Name: "test", Host: "https://example.com/", Org: ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := SaveContext(tt.ctx)
			if err == nil {
				t.Errorf("SaveContext() should fail for invalid data: %s", tt.name)
			}
		})
	}
}

func TestEXOHUB_CONTEXT_EnvVar(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	// Create a context
	ctx := &Context{
		Name: "envctx",
		Host: "https://git.example.com/",
		Org:  "testorg",
	}
	if err := SaveContext(ctx); err != nil {
		t.Fatalf("SaveContext() error = %v", err)
	}

	// No flag set, env var provides context
	OverrideContext = ""
	t.Cleanup(func() { OverrideContext = "" })
	t.Setenv("EXOHUB_CONTEXT", "envctx")

	got, err := GetCurrentContext()
	if err != nil {
		t.Fatalf("GetCurrentContext() error = %v", err)
	}
	if got.Name != "envctx" {
		t.Errorf("Expected context name 'envctx', got %v", got.Name)
	}
}

func TestContextFlagOverridesEnvVar(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	// Create two contexts
	for _, name := range []string{"flagctx", "envctx"} {
		ctx := &Context{Name: name, Host: "https://git.example.com/", Org: name + "-org"}
		if err := SaveContext(ctx); err != nil {
			t.Fatalf("SaveContext(%s) error = %v", name, err)
		}
	}

	// Flag wins over env var
	OverrideContext = "flagctx"
	t.Cleanup(func() { OverrideContext = "" })
	t.Setenv("EXOHUB_CONTEXT", "envctx")

	got, err := GetCurrentContext()
	if err != nil {
		t.Fatalf("GetCurrentContext() error = %v", err)
	}
	if got.Org != "flagctx-org" {
		t.Errorf("Expected flag context org 'flagctx-org', got %v", got.Org)
	}
}

func TestEXOHUB_CONTEXT_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	OverrideContext = ""
	t.Cleanup(func() { OverrideContext = "" })
	t.Setenv("EXOHUB_CONTEXT", "nonexistent")

	_, err := GetCurrentContext()
	if err == nil {
		t.Errorf("GetCurrentContext() should fail for nonexistent context")
	}
}

func TestDefaultBuiltinContext(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	// No user-created "default" context exists
	OverrideContext = "default"
	t.Cleanup(func() { OverrideContext = "" })

	got, err := GetCurrentContext()
	if err != nil {
		t.Fatalf("GetCurrentContext() error = %v", err)
	}
	if got.Host != DefaultContext.Host {
		t.Errorf("Expected default host %v, got %v", DefaultContext.Host, got.Host)
	}
	if got.Org != DefaultContext.Org {
		t.Errorf("Expected default org %v, got %v", DefaultContext.Org, got.Org)
	}
	if got.Template != DefaultContext.Template {
		t.Errorf("Expected default template %v, got %v", DefaultContext.Template, got.Template)
	}
}

func TestDefaultContextOverriddenByUser(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	// Create a user context named "default"
	userDefault := &Context{
		Name: "default",
		Host: "https://custom.example.com/",
		Org:  "custom-org",
	}
	if err := SaveContext(userDefault); err != nil {
		t.Fatalf("SaveContext() error = %v", err)
	}

	OverrideContext = "default"
	t.Cleanup(func() { OverrideContext = "" })

	got, err := GetCurrentContext()
	if err != nil {
		t.Fatalf("GetCurrentContext() error = %v", err)
	}
	if got.Host != "https://custom.example.com/" {
		t.Errorf("Expected user-created default host, got %v", got.Host)
	}
}

func TestContextDescription(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	ctx := &Context{
		Name:        "described",
		Host:        "https://git.example.com/",
		Org:         "testorg",
		Description: "My test context for production data",
	}
	if err := SaveContext(ctx); err != nil {
		t.Fatalf("SaveContext() error = %v", err)
	}

	loaded, err := LoadContext("described")
	if err != nil {
		t.Fatalf("LoadContext() error = %v", err)
	}
	if loaded.Description != "My test context for production data" {
		t.Errorf("Description mismatch: got %v", loaded.Description)
	}
}

func TestResolveContextName(t *testing.T) {
	// Flag takes priority
	OverrideContext = "flagval"
	t.Cleanup(func() { OverrideContext = "" })
	t.Setenv("EXOHUB_CONTEXT", "envval")

	if got := ResolveContextName(); got != "flagval" {
		t.Errorf("Expected 'flagval', got %v", got)
	}

	// Env var when no flag
	OverrideContext = ""
	if got := ResolveContextName(); got != "envval" {
		t.Errorf("Expected 'envval', got %v", got)
	}

	// Empty when neither
	t.Setenv("EXOHUB_CONTEXT", "")
	if got := ResolveContextName(); got != "" {
		t.Errorf("Expected empty, got %v", got)
	}
}

func TestNoContextSpecified(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	OverrideContext = ""
	t.Cleanup(func() { OverrideContext = "" })
	t.Setenv("EXOHUB_CONTEXT", "")

	_, err := GetCurrentContext()
	if err == nil {
		t.Errorf("GetCurrentContext() should fail when no context is specified")
	}
}

func TestContextExistsError(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("XDG_CONFIG_HOME", tmpDir)
	defer os.Unsetenv("XDG_CONFIG_HOME")

	// Check non-existent context
	exists, err := ContextExists("non-existent")
	if err != nil {
		t.Fatalf("ContextExists() error = %v", err)
	}
	if exists {
		t.Errorf("Context should not exist")
	}

	// Create context
	ctx := &Context{
		Name: "test-context",
		Host: "https://git.example.com/",
		Org:  "testorg",
	}
	if err := SaveContext(ctx); err != nil {
		t.Fatalf("SaveContext() error = %v", err)
	}

	// Check existing context
	exists, err = ContextExists("test-context")
	if err != nil {
		t.Fatalf("ContextExists() error = %v", err)
	}
	if !exists {
		t.Errorf("Context should exist")
	}
}
