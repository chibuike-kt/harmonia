package companion

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// withFakePicker substitutes folderPicker for the duration of one test
// and restores the real one after — never leaves the package-level
// picker pointed at a test fake for any test that runs after this one.
func withFakePicker(t *testing.T, fn func() (string, error)) {
	t.Helper()
	original := folderPicker
	folderPicker = fn
	t.Cleanup(func() { folderPicker = original })
}

// TestPickFolder_OpensTheChosenPath is the real proof that a folder
// picked through the (faked, for the test) native dialog actually ends
// up opened exactly like openFolder's own existing, already-proven path
// — same folder_opened response, same real directory becomes the
// session's real root.
func TestPickFolder_OpensTheChosenPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "picked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	withFakePicker(t, func() (string, error) { return dir, nil })

	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()

	if err := conn.WriteJSON(clientMessage{Type: "pick_folder"}); err != nil {
		t.Fatalf("write pick_folder: %v", err)
	}
	opened := readMsg(t, conn)
	if opened.Type != "folder_opened" {
		t.Fatalf("opened = %+v, want type folder_opened", opened)
	}
	abs, _ := filepath.Abs(dir)
	if opened.Path != abs {
		t.Fatalf("opened.Path = %q, want %q", opened.Path, abs)
	}
}

// TestPickFolder_CanceledDialogSendsNothing proves a human closing the
// native dialog without choosing anything is a real, silent no-op — not
// an error the frontend has to handle, and not a folder_opened for a
// folder nobody actually chose.
func TestPickFolder_CanceledDialogSendsNothing(t *testing.T) {
	withFakePicker(t, func() (string, error) { return "", nil })

	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()

	if err := conn.WriteJSON(clientMessage{Type: "pick_folder"}); err != nil {
		t.Fatalf("write pick_folder: %v", err)
	}
	// Prove silence by immediately sending a real, distinguishable
	// message on the same connection and confirming *that* response is
	// the very next thing read — if pick_folder had wrongly emitted
	// anything, it would arrive first.
	if err := conn.WriteJSON(clientMessage{Type: "list_dir", Path: ""}); err != nil {
		t.Fatalf("write list_dir: %v", err)
	}
	resp := readMsg(t, conn)
	if resp.Type != "error" || resp.Message == "" {
		t.Fatalf("expected list_dir's own real error (no folder open) as the next message, got %+v", resp)
	}
}

// TestPickFolder_PickerErrorReported proves a real failure from the
// native dialog mechanism itself (e.g. this platform not implementing
// one yet) reaches the frontend as a real error, not a silent hang.
func TestPickFolder_PickerErrorReported(t *testing.T) {
	withFakePicker(t, func() (string, error) { return "", errors.New("dialog failed") })

	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()

	if err := conn.WriteJSON(clientMessage{Type: "pick_folder"}); err != nil {
		t.Fatalf("write pick_folder: %v", err)
	}
	resp := readMsg(t, conn)
	if resp.Type != "error" {
		t.Fatalf("resp = %+v, want type error", resp)
	}
}
