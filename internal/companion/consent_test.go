package companion

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// TestConsent_WSRejectedBeforeConsentGranted is the real proof for
// ADR-010's central consent claim: on a fresh machine (a ConsentStore
// that has never been granted), the WebSocket never accepts a
// connection at all — not a degraded/read-only session, a real refusal
// before a single message is read.
func TestConsent_WSRejectedBeforeConsentGranted(t *testing.T) {
	consent := NewConsentStore(filepath.Join(t.TempDir(), "consent.json"))
	srv := httptest.NewServer(Handler(NewAllowedOrigins("http://localhost:3000"), consent))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, map[string][]string{
		"Origin": {"http://localhost:3000"},
	})
	if err == nil {
		t.Fatal("dial succeeded with consent never granted, want it rejected")
	}
	if resp == nil || resp.StatusCode != http.StatusPreconditionRequired {
		status := "no response"
		if resp != nil {
			status = resp.Status
		}
		t.Fatalf("status = %s, want %d", status, http.StatusPreconditionRequired)
	}
}

// TestConsent_WSAcceptedAfterConsentGranted proves the other half: the
// exact same store, after a real Grant() call — the same call the
// frontend's consent screen triggers via POST /consent once a human
// actually clicks through it — now allows the connection.
func TestConsent_WSAcceptedAfterConsentGranted(t *testing.T) {
	consent := NewConsentStore(filepath.Join(t.TempDir(), "consent.json"))
	if err := consent.Grant(); err != nil {
		t.Fatalf("grant: %v", err)
	}
	srv := httptest.NewServer(Handler(NewAllowedOrigins("http://localhost:3000"), consent))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, map[string][]string{
		"Origin": {"http://localhost:3000"},
	})
	if err != nil {
		status := "no response"
		if resp != nil {
			status = resp.Status
		}
		t.Fatalf("dial: %v (status %s)", err, status)
	}
	_ = conn.Close()
}

// TestConsent_PersistsAcrossProcessRestarts proves consent is real,
// durable state, not an in-memory flag that would silently re-ask on
// every companion relaunch — a fresh ConsentStore instance pointed at
// the same real file (standing in for the companion binary restarting)
// must see the earlier grant.
func TestConsent_PersistsAcrossProcessRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "consent.json")

	first := NewConsentStore(path)
	if first.Granted() {
		t.Fatal("a brand new consent path reports granted before Grant() was ever called")
	}
	if err := first.Grant(); err != nil {
		t.Fatalf("grant: %v", err)
	}

	second := NewConsentStore(path)
	if !second.Granted() {
		t.Fatal("a fresh ConsentStore over the same real file doesn't see the earlier grant")
	}
}

// TestConsentHandler_ReflectsRealState drives the actual HTTP handler
// the companion binary serves — GET before and after a real POST —
// proving the frontend's own consent-status check and consent-grant
// calls do what they claim against the real store, not just that the
// store's own Go methods work in isolation.
func TestConsentHandler_ReflectsRealState(t *testing.T) {
	consent := NewConsentStore(filepath.Join(t.TempDir(), "consent.json"))
	srv := httptest.NewServer(ConsentHandler(consent, NewAllowedOrigins("http://localhost:3000")))
	defer srv.Close()

	before, err := http.Get(srv.URL + "/consent")
	if err != nil {
		t.Fatalf("GET /consent: %v", err)
	}
	var beforeBody consentStatusResponse
	if err := decodeJSON(before, &beforeBody); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if beforeBody.Granted {
		t.Fatal("status reports granted before any POST /consent")
	}

	after, err := http.Post(srv.URL+"/consent", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /consent: %v", err)
	}
	var afterBody consentStatusResponse
	if err := decodeJSON(after, &afterBody); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !afterBody.Granted {
		t.Fatal("POST /consent response reports not granted")
	}

	confirm, err := http.Get(srv.URL + "/consent")
	if err != nil {
		t.Fatalf("GET /consent after grant: %v", err)
	}
	var confirmBody consentStatusResponse
	if err := decodeJSON(confirm, &confirmBody); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !confirmBody.Granted {
		t.Fatal("a subsequent GET /consent still reports not granted after a real POST /consent")
	}
}

// TestConsentHandler_CORSMatchesTheWSOriginAllowList proves /consent's
// CORS header follows the exact same allow-list /ws's own origin check
// enforces: an allowed origin's request gets a real
// Access-Control-Allow-Origin header (without it, the browser page's own
// fetch() can't read the response even though the request succeeds), an
// unlisted origin gets none.
func TestConsentHandler_CORSMatchesTheWSOriginAllowList(t *testing.T) {
	consent := NewConsentStore(filepath.Join(t.TempDir(), "consent.json"))
	srv := httptest.NewServer(ConsentHandler(consent, NewAllowedOrigins("http://localhost:3000")))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/consent", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Fatalf("Access-Control-Allow-Origin = %q for the allowed origin, want %q", got, "http://localhost:3000")
	}

	evilReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/consent", nil)
	evilReq.Header.Set("Origin", "https://evil.example.com")
	evilResp, err := http.DefaultClient.Do(evilReq)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = evilResp.Body.Close()
	if got := evilResp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q for an unlisted origin, want no header at all", got)
	}
}

func decodeJSON(resp *http.Response, v any) error {
	defer func() { _ = resp.Body.Close() }()
	return json.NewDecoder(resp.Body).Decode(v)
}
