package tips

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setupCacheDir creates a temp directory and wires it as the cache root via
// EXO_CONFIG_DIR so all cache helpers resolve under it.
func setupCacheDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("EXO_CONFIG_DIR", dir)
	return dir
}

func writeCacheWithAge(t *testing.T, bank *TipBank, age time.Duration) string {
	t.Helper()
	cf, err := cacheFile()
	if err != nil {
		t.Fatalf("cacheFile: %v", err)
	}
	data, _ := json.Marshal(bank)
	if err := os.MkdirAll(filepath.Dir(cf), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(cf, data, 0644); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	mtime := time.Now().Add(-age)
	if err := os.Chtimes(cf, mtime, mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	return cf
}

// ---- FetchAndCache tests ----

func TestFetchAndCache_FreshCache(t *testing.T) {
	setupCacheDir(t)

	bank := &TipBank{Tips: []Tip{{ID: "1", Title: "Fresh tip", Body: "body"}}}
	writeCacheWithAge(t, bank, 1*time.Minute) // well within TTL

	got, err := FetchAndCache()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Tips) != 1 || got.Tips[0].ID != "1" {
		t.Fatalf("expected fresh-cached tip, got %+v", got)
	}
}

func TestFetchAndCache_StaleCache_FetchesRemote(t *testing.T) {
	setupCacheDir(t)

	// Write a stale cache with an old tip.
	stale := &TipBank{Tips: []Tip{{ID: "old", Title: "Old", Body: "old body"}}}
	writeCacheWithAge(t, stale, 25*time.Hour)

	// Serve a fresh bank from a test HTTP server.
	fresh := &TipBank{Tips: []Tip{{ID: "new", Title: "New", Body: "new body"}}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(fresh)
	}))
	defer srv.Close()

	// Override the package-level tipsURL var so FetchAndCache hits the test server.
	orig := tipsURL
	tipsURL = func() string { return srv.URL + "/tips.json" }
	defer func() { tipsURL = orig }()

	got, err := FetchAndCache()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Tips) == 0 {
		t.Fatal("expected tips from remote server")
	}
	if got.Tips[0].ID != "new" {
		t.Fatalf("expected new tip from remote, got id=%q", got.Tips[0].ID)
	}
}

func TestFetchAndCache_NoCacheOffline(t *testing.T) {
	setupCacheDir(t)
	// No cache file; network unreachable → returns empty bank gracefully.
	got, err := FetchAndCache()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil TipBank")
	}
}

// ---- PickRandom tests ----

func TestPickRandom_Empty(t *testing.T) {
	if PickRandom(&TipBank{}) != nil {
		t.Fatal("expected nil for empty bank")
	}
}

func TestPickRandom_Nil(t *testing.T) {
	if PickRandom(nil) != nil {
		t.Fatal("expected nil for nil bank")
	}
}

func TestPickRandom_Single(t *testing.T) {
	bank := &TipBank{Tips: []Tip{{ID: "1", Title: "Only tip", Body: "b"}}}
	tip := PickRandom(bank)
	if tip == nil {
		t.Fatal("expected a tip")
	}
	if tip.ID != "1" {
		t.Fatalf("expected id=1, got %s", tip.ID)
	}
}

func TestPickRandom_Multiple(t *testing.T) {
	bank := &TipBank{Tips: make([]Tip, 10)}
	for i := range bank.Tips {
		bank.Tips[i] = Tip{ID: string(rune('0' + i))}
	}
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		tip := PickRandom(bank)
		if tip != nil {
			seen[tip.ID] = true
		}
	}
	if len(seen) < 2 {
		t.Fatal("expected randomness across multiple calls")
	}
}

// ---- ShouldShowToday tests ----

func TestShouldShowToday_FirstCall(t *testing.T) {
	setupCacheDir(t)
	if !ShouldShowToday() {
		t.Fatal("first call should return true")
	}
}

func TestShouldShowToday_SecondCallSameDay(t *testing.T) {
	setupCacheDir(t)
	ShouldShowToday() // first call
	if ShouldShowToday() {
		t.Fatal("second call on same day should return false")
	}
}

func TestShouldShowToday_NextDay(t *testing.T) {
	setupCacheDir(t)

	// Simulate yesterday being recorded.
	path, err := lastShownFile()
	if err != nil {
		t.Fatalf("lastShownFile: %v", err)
	}
	yesterday := time.Now().AddDate(0, 0, -1).Format(dateFormat)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(yesterday), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if !ShouldShowToday() {
		t.Fatal("should return true when last shown was yesterday")
	}
}

// ---- IsEnabled tests ----

func TestIsEnabled_Default(t *testing.T) {
	t.Setenv("EXOHUB_TIPS", "")
	setupCacheDir(t) // no preferences file → default true
	if !IsEnabled() {
		t.Fatal("expected enabled by default")
	}
}

func TestIsEnabled_EnvFalse(t *testing.T) {
	t.Setenv("EXOHUB_TIPS", "false")
	if IsEnabled() {
		t.Fatal("expected disabled when EXOHUB_TIPS=false")
	}
}

func TestIsEnabled_EnvZero(t *testing.T) {
	t.Setenv("EXOHUB_TIPS", "0")
	if IsEnabled() {
		t.Fatal("expected disabled when EXOHUB_TIPS=0")
	}
}

func TestIsEnabled_EnvTrue(t *testing.T) {
	t.Setenv("EXOHUB_TIPS", "true")
	if !IsEnabled() {
		t.Fatal("expected enabled when EXOHUB_TIPS=true")
	}
}

func TestIsEnabled_DisableTipsOne(t *testing.T) {
	t.Setenv("EXOHUB_DISABLE_TIPS", "1")
	if IsEnabled() {
		t.Fatal("expected disabled when EXOHUB_DISABLE_TIPS=1")
	}
}

func TestIsEnabled_DisableTipsTrue(t *testing.T) {
	t.Setenv("EXOHUB_DISABLE_TIPS", "true")
	if IsEnabled() {
		t.Fatal("expected disabled when EXOHUB_DISABLE_TIPS=true")
	}
}

func TestIsEnabled_DisableTipsYes(t *testing.T) {
	t.Setenv("EXOHUB_DISABLE_TIPS", "yes")
	if IsEnabled() {
		t.Fatal("expected disabled when EXOHUB_DISABLE_TIPS=yes")
	}
}

func TestIsEnabled_DisableTipsOverridesEnvTips(t *testing.T) {
	t.Setenv("EXOHUB_DISABLE_TIPS", "1")
	t.Setenv("EXOHUB_TIPS", "true")
	if IsEnabled() {
		t.Fatal("expected EXOHUB_DISABLE_TIPS=1 to override EXOHUB_TIPS=true")
	}
}

func TestIsEnabled_DisableTipsFallthroughToEnvTips(t *testing.T) {
	// Non-truthy EXOHUB_DISABLE_TIPS should be ignored; EXOHUB_TIPS=false then disables.
	t.Setenv("EXOHUB_DISABLE_TIPS", "0")
	t.Setenv("EXOHUB_TIPS", "false")
	if IsEnabled() {
		t.Fatal("expected disabled via EXOHUB_TIPS=false when EXOHUB_DISABLE_TIPS=0 falls through")
	}
}

// ---- MaybeShowDailyTip tests ----

func TestMaybeShowDailyTip_Disabled(t *testing.T) {
	t.Setenv("EXOHUB_TIPS", "false")
	setupCacheDir(t)

	var buf strings.Builder
	MaybeShowDailyTip(&buf)
	if buf.Len() != 0 {
		t.Fatal("expected no output when tips disabled")
	}
}

func TestMaybeShowDailyTip_DisableTipsEnv(t *testing.T) {
	t.Setenv("EXOHUB_DISABLE_TIPS", "1")
	t.Setenv("EXOHUB_TIPS", "true")
	setupCacheDir(t)

	var buf strings.Builder
	MaybeShowDailyTip(&buf)
	if buf.Len() != 0 {
		t.Fatal("expected no output when EXOHUB_DISABLE_TIPS=1")
	}
}

func TestMaybeShowDailyTip_AlreadyShownToday(t *testing.T) {
	t.Setenv("EXOHUB_TIPS", "true")
	setupCacheDir(t)

	// Record today already shown.
	path, _ := lastShownFile()
	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, []byte(time.Now().Format(dateFormat)), 0644)

	var buf strings.Builder
	MaybeShowDailyTip(&buf)
	if buf.Len() != 0 {
		t.Fatal("expected no output when already shown today")
	}
}
