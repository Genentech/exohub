package init

import (
	"os"
	"testing"
)

func chdirTemp(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

func TestLoadProfileLockMissingReturnsNil(t *testing.T) {
	chdirTemp(t)

	lock, err := LoadProfileLock()
	if err != nil {
		t.Fatalf("LoadProfileLock: %v", err)
	}
	if lock != nil {
		t.Fatalf("expected nil lock, got %+v", lock)
	}
}

func TestSaveAndLoadProfileLockRoundTrip(t *testing.T) {
	chdirTemp(t)

	want := &ProfileLock{
		Profile: "my-profile",
		Hash:    "abc123",
		Answers: map[string]interface{}{
			"read_access": "authenticated",
			"create_repo": true,
		},
	}
	if err := SaveProfileLock(want); err != nil {
		t.Fatalf("SaveProfileLock: %v", err)
	}

	got, err := LoadProfileLock()
	if err != nil {
		t.Fatalf("LoadProfileLock: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil lock")
	}
	if got.Profile != want.Profile {
		t.Errorf("Profile: got %q want %q", got.Profile, want.Profile)
	}
	if got.Hash != want.Hash {
		t.Errorf("Hash: got %q want %q", got.Hash, want.Hash)
	}
	if got.Answers["read_access"] != "authenticated" {
		t.Errorf("Answers[read_access]: got %v", got.Answers["read_access"])
	}
	if got.Answers["create_repo"] != true {
		t.Errorf("Answers[create_repo]: got %v", got.Answers["create_repo"])
	}
}

func TestProfileSpecHashStableAndSensitiveToChange(t *testing.T) {
	spec1 := &ProfileSpec{
		Name:      "sandbox",
		Vars:      map[string]string{"bucket": "my-bucket"},
		Prompts:   []PromptSpec{{Name: "read_access", Kind: "choice"}},
		Templates: map[string]string{"remotes": "name: x\n"},
	}
	spec2 := &ProfileSpec{
		Name:      "sandbox-renamed", // Name isn't hashed, only Vars/Prompts/Templates
		Vars:      map[string]string{"bucket": "my-bucket"},
		Prompts:   []PromptSpec{{Name: "read_access", Kind: "choice"}},
		Templates: map[string]string{"remotes": "name: x\n"},
	}
	if ProfileSpecHash(spec1) != ProfileSpecHash(spec2) {
		t.Error("hash should be stable across calls with equivalent vars/prompts/templates")
	}

	spec3 := &ProfileSpec{
		Name:      "sandbox",
		Vars:      map[string]string{"bucket": "my-bucket"},
		Prompts:   []PromptSpec{{Name: "read_access", Kind: "choice"}},
		Templates: map[string]string{"remotes": "name: y\n"}, // changed
	}
	if ProfileSpecHash(spec1) == ProfileSpecHash(spec3) {
		t.Error("hash should change when templates change")
	}
}
