package tips

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Genentech/exohub/go/exo/configdir"
	"github.com/Genentech/exohub/go/exo/internal/defaults"
	"github.com/Genentech/exohub/go/exo/preferences"
)

const (
	cacheTTL    = 24 * time.Hour
	dateFormat  = "2006-01-02"
	httpTimeout = 5 * time.Second
)

// tipsURL derives the tips.json URL from EXOHUB_API_URL env var (same pattern as the rest of the CLI).
// The website base is the API base with the "/api" suffix stripped.
// This is a package-level var so tests can override it with an httptest server URL.
var tipsURL = func() string {
	base := defaults.APIBase()
	base = strings.TrimSuffix(strings.TrimRight(base, "/"), "/api")
	return base + "/tips.json"
}

// Tip represents a single tip entry from the tips bank.
type Tip struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// TipBank holds the collection of tips fetched from the remote.
type TipBank struct {
	Tips []Tip `json:"tips"`
}

// cacheDir returns the directory where tips cache files are stored.
func cacheDir() (string, error) {
	if root := configdir.Root(); root != "" {
		p := filepath.Join(root, "cache", "exo")
		return p, os.MkdirAll(p, 0755)
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, "cache", "exo")
	return p, os.MkdirAll(p, 0755)
}

func cacheFile() (string, error) {
	dir, err := cacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tips.json"), nil
}

func lastShownFile() (string, error) {
	dir, err := cacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tips-last-shown"), nil
}

// FetchAndCache fetches tips.json from the remote and caches it locally.
// If the cache is still fresh (< 24h old) the network call is skipped.
// Returns the tip bank from cache or remote. Gracefully returns an empty
// bank (not an error) when both fetch and cache are unavailable.
func FetchAndCache() (*TipBank, error) {
	cf, err := cacheFile()
	if err != nil {
		return &TipBank{}, nil
	}

	if info, err := os.Stat(cf); err == nil {
		if time.Since(info.ModTime()) < cacheTTL {
			return loadCache(cf)
		}
	}

	bank, fetchErr := fetchRemote()
	if fetchErr != nil {
		// Fall back to cache even if stale.
		if cached, err := loadCache(cf); err == nil {
			return cached, nil
		}
		return &TipBank{}, nil
	}

	_ = writeCache(cf, bank)
	return bank, nil
}

func fetchRemote() (*TipBank, error) {
	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Get(tipsURL())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("tips fetch: %s", resp.Status)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	var bank TipBank
	if err := json.Unmarshal(data, &bank); err != nil {
		return nil, err
	}
	return &bank, nil
}

func loadCache(path string) (*TipBank, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var bank TipBank
	if err := json.Unmarshal(data, &bank); err != nil {
		return nil, err
	}
	return &bank, nil
}

func writeCache(path string, bank *TipBank) error {
	data, err := json.Marshal(bank)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// PickRandom returns a random tip from the bank, or nil if the bank is empty.
func PickRandom(bank *TipBank) *Tip {
	if bank == nil || len(bank.Tips) == 0 {
		return nil
	}
	//nolint:gosec
	i := rand.Intn(len(bank.Tips))
	t := bank.Tips[i]
	return &t
}

// HasShownToday reports whether a tip has already been shown today.
// It is a read-only check and does not update the on-disk state.
func HasShownToday() bool {
	path, err := lastShownFile()
	if err != nil {
		return false
	}
	today := time.Now().Format(dateFormat)
	data, err := os.ReadFile(path)
	return err == nil && strings.TrimSpace(string(data)) == today
}

// MarkShownToday records that a tip was shown today so that HasShownToday
// returns true for the remainder of the calendar day.
// NOTE: There is a known TOCTOU race: two concurrent exo invocations can both
// call HasShownToday before either calls MarkShownToday, causing both to show
// a tip on the same day. Full file locking would mitigate this but is not
// implemented given the low impact of the race.
func MarkShownToday() {
	path, err := lastShownFile()
	if err != nil {
		return
	}
	today := time.Now().Format(dateFormat)
	_ = os.WriteFile(path, []byte(today), 0644)
}

// ShouldShowToday returns true the first time it is called on a given calendar
// day. Subsequent calls on the same day return false. The check is persisted
// across process invocations via a small file in the cache directory.
func ShouldShowToday() bool {
	if HasShownToday() {
		return false
	}
	MarkShownToday()
	return true
}

// IsEnabled reports whether the tips feature is active.
// Precedence (highest to lowest):
//  1. EXOHUB_DISABLE_TIPS=1|true|yes  — always disables tips (for servers/CI).
//     Any other non-empty value (0, false, no, …) is ignored and falls through
//     to the next check; the variable is purely opt-in for disabling.
//  2. EXOHUB_TIPS=false|0             — disables; any other non-empty value enables.
//  3. preferences.TipsEnabled         — user preference.
//  4. default: enabled.
func IsEnabled() bool {
	if v := os.Getenv("EXOHUB_DISABLE_TIPS"); v != "" {
		v = strings.ToLower(v)
		if v == "1" || v == "true" || v == "yes" {
			return false
		}
	}
	if v := os.Getenv("EXOHUB_TIPS"); v != "" {
		return strings.ToLower(v) != "false" && v != "0"
	}
	prefs, err := preferences.Load()
	if err != nil || prefs == nil {
		return true
	}
	if prefs.TipsEnabled != nil {
		return *prefs.TipsEnabled
	}
	return true
}

// MaybeShowDailyTip writes a randomly selected tip to w once per day,
// provided the feature is enabled. Errors are silently ignored so the
// calling command is never affected.
//
// The slot is only consumed (marked as shown) after a tip has been
// successfully fetched and selected, so an offline or empty-bank run
// does not burn the day's tip.
func MaybeShowDailyTip(w io.Writer) {
	if !IsEnabled() {
		return
	}
	if HasShownToday() {
		return
	}

	bank, err := FetchAndCache()
	if err != nil || bank == nil {
		return
	}

	tip := PickRandom(bank)
	if tip == nil {
		return
	}

	MarkShownToday()
	fmt.Fprintf(w, "\n💡 Tip of the day: %s\n%s\n\n", tip.Title, tip.Body)
}
