package companion

import (
	"encoding/base64"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// grantedConsent builds a ConsentStore already granted, backed by a real
// file in t's own temp dir — every test in this file that isn't itself
// testing the consent gate uses this, so the gate this package added
// doesn't have to be re-proven by every unrelated test.
func grantedConsent(t *testing.T) *ConsentStore {
	t.Helper()
	store := NewConsentStore(filepath.Join(t.TempDir(), "consent.json"))
	if err := store.Grant(); err != nil {
		t.Fatalf("grant consent: %v", err)
	}
	return store
}

func dialTestServer(t *testing.T, allowed AllowedOrigins, origin string) (*websocket.Conn, func()) {
	t.Helper()
	srv := httptest.NewServer(Handler(allowed, grantedConsent(t)))
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	headers := map[string][]string{}
	if origin != "" {
		headers["Origin"] = []string{origin}
	}
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		if resp != nil {
			t.Fatalf("dial: %v (status %s)", err, resp.Status)
		}
		t.Fatalf("dial: %v", err)
	}
	return conn, func() {
		_ = conn.Close()
		srv.Close()
	}
}

func readMsg(t *testing.T, conn *websocket.Conn) serverMessage {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var msg serverMessage
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read message: %v", err)
	}
	return msg
}

// TestOriginCheck_RejectsAnUnlistedOrigin is the real proof for this
// package's own central security claim: a WebSocket upgrade from a
// browser tab on some other site — the exact shape of "any website a
// user happens to have open" — never reaches a single file or shell
// operation.
func TestOriginCheck_RejectsAnUnlistedOrigin(t *testing.T) {
	allowed := NewAllowedOrigins("http://localhost:3000")
	srv := httptest.NewServer(Handler(allowed, grantedConsent(t)))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, map[string][]string{
		"Origin": {"https://evil.example.com"},
	})
	if err == nil {
		t.Fatal("dial from an unlisted origin succeeded, want it rejected")
	}
	if resp == nil || resp.StatusCode != 403 {
		status := "no response"
		if resp != nil {
			status = resp.Status
		}
		t.Fatalf("status = %s, want 403", status)
	}
}

func TestOriginCheck_AllowsTheConfiguredOrigin(t *testing.T) {
	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()
	// A successful dial (no error from dialTestServer, which would have
	// called t.Fatalf) is the proof — nothing further to assert.
	_ = conn
}

// TestFileRoundTrip_OpenListReadWrite proves the real, end-to-end file
// path: a real temp directory on this machine's own disk, opened over
// the wire, listed, read, and written to — each step verified against
// the real filesystem independently, not just against what the
// companion claims happened.
func TestFileRoundTrip_OpenListReadWrite(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatalf("seed dir: %v", err)
	}

	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()

	if err := conn.WriteJSON(clientMessage{Type: "open_folder", Path: dir}); err != nil {
		t.Fatalf("write open_folder: %v", err)
	}
	opened := readMsg(t, conn)
	if opened.Type != "folder_opened" {
		t.Fatalf("opened = %+v, want type folder_opened", opened)
	}

	if err := conn.WriteJSON(clientMessage{Type: "list_dir", Path: ""}); err != nil {
		t.Fatalf("write list_dir: %v", err)
	}
	listing := readMsg(t, conn)
	if listing.Type != "dir_listing" || len(listing.Entries) != 2 {
		t.Fatalf("listing = %+v, want 2 real entries (README.md, src)", listing)
	}

	if err := conn.WriteJSON(clientMessage{Type: "read_file", Path: "README.md"}); err != nil {
		t.Fatalf("write read_file: %v", err)
	}
	read := readMsg(t, conn)
	if read.Type != "file_content" {
		t.Fatalf("read = %+v, want type file_content", read)
	}
	content, err := base64.StdEncoding.DecodeString(read.ContentBase64)
	if err != nil || string(content) != "hello" {
		t.Fatalf("content = %q (err=%v), want %q", content, err, "hello")
	}

	newContent := base64.StdEncoding.EncodeToString([]byte("real new content"))
	if err := conn.WriteJSON(clientMessage{Type: "write_file", Path: "README.md", ContentBase64: newContent}); err != nil {
		t.Fatalf("write write_file: %v", err)
	}
	written := readMsg(t, conn)
	if written.Type != "file_written" {
		t.Fatalf("written = %+v, want type file_written", written)
	}
	// The real, independent proof: read the file straight off disk, not
	// through the companion's own claim that the write succeeded.
	onDisk, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil || string(onDisk) != "real new content" {
		t.Fatalf("on-disk content = %q (err=%v), want %q", onDisk, err, "real new content")
	}
}

// TestResolvePath_RejectsEscapingTheOpenFolder proves the path-traversal
// guard actually blocks a crafted path from reaching disk outside the
// folder the human opened — even though this is a trusted local
// process, not a defense against a remote attacker.
func TestResolvePath_RejectsEscapingTheOpenFolder(t *testing.T) {
	dir := t.TempDir()
	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()

	if err := conn.WriteJSON(clientMessage{Type: "open_folder", Path: dir}); err != nil {
		t.Fatalf("write open_folder: %v", err)
	}
	_ = readMsg(t, conn) // folder_opened

	if err := conn.WriteJSON(clientMessage{Type: "read_file", Path: "../../../../etc/passwd"}); err != nil {
		t.Fatalf("write read_file: %v", err)
	}
	resp := readMsg(t, conn)
	if resp.Type != "error" {
		t.Fatalf("response to a path-traversal read = %+v, want type error", resp)
	}
	if !strings.Contains(resp.Message, "escapes the open folder") {
		t.Fatalf("error message = %q, want it to name the real problem", resp.Message)
	}
}

// TestShell_RunsARealCommandAndReturnsRealOutput is IDE Stage's own real
// proof for the terminal: a real shell process, spawned by the
// companion, actually executes a real command against the open folder
// and the real output comes back over the wire — not a simulated
// response.
func TestShell_RunsARealCommandAndReturnsRealOutput(t *testing.T) {
	dir := t.TempDir()
	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()

	if err := conn.WriteJSON(clientMessage{Type: "open_folder", Path: dir}); err != nil {
		t.Fatalf("write open_folder: %v", err)
	}
	_ = readMsg(t, conn) // folder_opened

	if err := conn.WriteJSON(clientMessage{Type: "start_shell"}); err != nil {
		t.Fatalf("write start_shell: %v", err)
	}
	started := readMsg(t, conn)
	if started.Type != "shell_started" {
		t.Fatalf("started = %+v, want type shell_started", started)
	}

	marker := "HARMONIA_COMPANION_REAL_OUTPUT_MARKER_7f3a"
	var command string
	if runtime.GOOS == "windows" {
		command = "Write-Output '" + marker + "'\r\n"
	} else {
		command = "echo " + marker + "\n"
	}
	if err := conn.WriteJSON(clientMessage{Type: "shell_input", Data: command}); err != nil {
		t.Fatalf("write shell_input: %v", err)
	}

	// Real process output arrives as one or more shell_output chunks —
	// accumulate until the real marker shows up or we give up.
	var accumulated strings.Builder
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		var msg serverMessage
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read shell output: %v", err)
		}
		if msg.Type == "shell_output" {
			accumulated.WriteString(msg.Data)
			if strings.Contains(accumulated.String(), marker) {
				return
			}
		}
	}
	t.Fatalf("shell output never contained the real marker; got: %q", accumulated.String())
}
