package companion

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ConsentStore is the companion's own local record of whether this
// machine's human has explicitly granted the one-time consent ADR-010
// requires before the companion does anything at all. Backed by a real
// file, not memory — consent must survive the companion process
// restarting (a crash, an update, a reboot), since re-asking on every
// launch would train the human to click through it without reading it,
// defeating the point of a dedicated, hard-to-miss screen.
type ConsentStore struct {
	path string

	mu      sync.Mutex
	granted bool
	loaded  bool
}

// NewConsentStore builds a store backed by path. The file doesn't need
// to exist yet — Granted() reports false until Grant() is called.
func NewConsentStore(path string) *ConsentStore {
	return &ConsentStore{path: path}
}

// DefaultConsentPath returns the real, OS-appropriate per-user config
// location for the consent record — os.UserConfigDir() (%AppData% on
// Windows, $XDG_CONFIG_HOME or ~/.config elsewhere), not a path this
// package invents itself.
func DefaultConsentPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "harmonia", "companion-consent.json"), nil
}

type consentRecord struct {
	Granted   bool      `json:"granted"`
	GrantedAt time.Time `json:"granted_at"`
}

// Granted reports whether this machine's human has already consented.
// Cached in memory after the first real read — every WebSocket upgrade
// attempt calls this, and a real disk read per connection attempt would
// be needless once the answer is known for this process's lifetime. A
// fresh Grant() call always updates the cache directly, so this never
// serves a stale false after Grant() has actually run.
func (c *ConsentStore) Granted() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loaded {
		return c.granted
	}
	c.loaded = true
	data, err := os.ReadFile(c.path)
	if err != nil {
		c.granted = false
		return false
	}
	var rec consentRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		c.granted = false
		return false
	}
	c.granted = rec.Granted
	return c.granted
}

// Grant records real, explicit consent to disk and marks it granted for
// the rest of this process's lifetime. Idempotent — granting twice just
// overwrites GrantedAt with the more recent real timestamp.
func (c *ConsentStore) Grant() error {
	rec := consentRecord{Granted: true, GrantedAt: time.Now().UTC()}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(c.path, data, 0o600); err != nil {
		return err
	}
	c.mu.Lock()
	c.granted = true
	c.loaded = true
	c.mu.Unlock()
	return nil
}

type consentStatusResponse struct {
	Granted bool `json:"granted"`
}

// ConsentHandler serves GET /consent (current status, for the frontend's
// consent screen to check before rendering anything) and POST /consent
// (records real consent — called only after a human has actually clicked
// the dedicated consent screen's own button, never automatically).
//
// Unlike /ws, this is a plain HTTP endpoint a browser reaches via
// fetch() rather than a WebSocket upgrade, so it needs its own real CORS
// handling — a fetch response with no Access-Control-Allow-Origin header
// is invisible to the page's own JavaScript even when the request itself
// succeeds. Reuses the exact same allowed-origins check /ws already
// enforces, never a permissive default: the two endpoints share one
// access-control boundary, not two that could quietly drift apart.
func ConsentHandler(store *ConsentStore, allowed AllowedOrigins) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && allowed[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(consentStatusResponse{Granted: store.Granted()})
		case http.MethodPost:
			if err := store.Grant(); err != nil {
				http.Error(w, "failed to record consent", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(consentStatusResponse{Granted: true})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}
