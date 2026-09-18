package companion

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

// ADR-012 Batch B: "the existing path-traversal guard from the
// companion work gets re-verified specifically against unattended
// mode, adversarially, not assumed to already cover this case." This
// file is that re-verification. resolvePath (companion.go) is the one
// real chokepoint every file-op handler already routes through —
// create_file, create_folder, delete_path, rename_path (both its old
// and new path), read_file, write_file — so driving real adversarial
// requests through the real WebSocket protocol at each of those
// handlers, and checking real disk state afterward (not just the
// response), is real end-to-end proof of the guard every one of them
// actually relies on, attended or unattended alike.

// outsideRoot sets up two real, separate temp directories — root (what
// gets opened) and outside (a real sibling directory a traversal
// attempt would have to escape into) — plus one real, known file
// already sitting inside outside, so a successful escape has something
// real to prove it reached.
func outsideRoot(t *testing.T) (root, outside, secretFile string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "project")
	outside = filepath.Join(base, "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	secretFile = filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secretFile, []byte("real secret content"), 0o644); err != nil {
		t.Fatalf("seed secret file: %v", err)
	}
	return root, outside, secretFile
}

func openTestFolder(t *testing.T, conn interface {
	WriteJSON(v any) error
}, root string) {
	t.Helper()
	if err := conn.WriteJSON(clientMessage{Type: "open_folder", Path: root}); err != nil {
		t.Fatalf("write open_folder: %v", err)
	}
}

func TestResolvePath_CreateFile_DeniesTraversalOutsideRoot(t *testing.T) {
	attacks := []string{
		"../evil.txt",
		"../../evil.txt",
		"../../../../../../tmp/evil.txt",
		`..\evil.txt`,
		`..\..\evil.txt`,
		"foo/../../evil.txt",
		"./../evil.txt",
	}
	for _, attack := range attacks {
		t.Run(attack, func(t *testing.T) {
			root, outside, _ := outsideRoot(t)
			conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
			defer cleanup()
			openTestFolder(t, conn, root)
			_ = readMsg(t, conn) // folder_opened

			if err := conn.WriteJSON(clientMessage{Type: "create_file", Path: attack}); err != nil {
				t.Fatalf("write create_file: %v", err)
			}
			resp := readMsg(t, conn)
			if resp.Type != "error" {
				t.Fatalf("response = %+v, want a real error for traversal attempt %q", resp, attack)
			}

			// Real proof: outside still contains exactly its one known
			// seed file (secret.txt, planted by outsideRoot) and
			// nothing new, and nothing escaped root's own parent
			// either.
			entries, err := os.ReadDir(outside)
			if err != nil {
				t.Fatalf("read outside dir: %v", err)
			}
			if len(entries) != 1 || entries[0].Name() != "secret.txt" {
				t.Fatalf("traversal %q escaped: outside dir now contains %v, want only secret.txt", attack, entries)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(root), "evil.txt")); err == nil {
				t.Fatalf("traversal %q escaped: evil.txt exists next to root", attack)
			}
		})
	}
}

func TestResolvePath_CreateFile_DeniesAbsolutePathOutsideRoot(t *testing.T) {
	root, _, secretFile := outsideRoot(t)
	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()
	openTestFolder(t, conn, root)
	_ = readMsg(t, conn)

	// The real absolute path to an already-real file outside root —
	// the strongest, most literal version of "point this operation
	// somewhere else entirely."
	if err := conn.WriteJSON(clientMessage{Type: "create_file", Path: secretFile}); err != nil {
		t.Fatalf("write create_file: %v", err)
	}
	resp := readMsg(t, conn)
	if resp.Type != "error" {
		t.Fatalf("response = %+v, want a real error for absolute path %q", resp, secretFile)
	}
	content, err := os.ReadFile(secretFile)
	if err != nil || string(content) != "real secret content" {
		t.Fatalf("secret file was touched: content=%q err=%v, want unchanged", content, err)
	}
}

func TestResolvePath_WriteFile_DeniesTraversalAndLeavesOutsideUntouched(t *testing.T) {
	root, _, secretFile := outsideRoot(t)
	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()
	openTestFolder(t, conn, root)
	_ = readMsg(t, conn)

	payload := base64.StdEncoding.EncodeToString([]byte("malicious overwrite"))
	if err := conn.WriteJSON(clientMessage{Type: "write_file", Path: "../outside/secret.txt", ContentBase64: payload}); err != nil {
		t.Fatalf("write write_file: %v", err)
	}
	resp := readMsg(t, conn)
	if resp.Type != "error" {
		t.Fatalf("response = %+v, want a real error", resp)
	}
	content, err := os.ReadFile(secretFile)
	if err != nil || string(content) != "real secret content" {
		t.Fatalf("secret file was overwritten: content=%q err=%v", content, err)
	}
}

func TestResolvePath_ReadFile_DeniesTraversalAndNeverReturnsOutsideContent(t *testing.T) {
	root, _, _ := outsideRoot(t)
	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()
	openTestFolder(t, conn, root)
	_ = readMsg(t, conn)

	if err := conn.WriteJSON(clientMessage{Type: "read_file", Path: "../outside/secret.txt"}); err != nil {
		t.Fatalf("write read_file: %v", err)
	}
	resp := readMsg(t, conn)
	if resp.Type == "file_content" {
		t.Fatalf("read_file traversal returned real content: %+v — the secret was actually read", resp)
	}
	if resp.Type != "error" {
		t.Fatalf("response = %+v, want a real error", resp)
	}
}

func TestResolvePath_DeletePath_DeniesTraversalAndLeavesOutsideFileIntact(t *testing.T) {
	root, _, secretFile := outsideRoot(t)
	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()
	openTestFolder(t, conn, root)
	_ = readMsg(t, conn)

	if err := conn.WriteJSON(clientMessage{Type: "delete_path", Path: "../outside/secret.txt"}); err != nil {
		t.Fatalf("write delete_path: %v", err)
	}
	resp := readMsg(t, conn)
	if resp.Type != "error" {
		t.Fatalf("response = %+v, want a real error", resp)
	}
	if _, err := os.Stat(secretFile); err != nil {
		t.Fatalf("secret file was deleted via traversal: %v", err)
	}
}

// rename_path resolves BOTH its old and new path through resolvePath —
// each direction of escape (renaming something real inside root to a
// destination outside it, and vice versa) needs its own real proof.
func TestResolvePath_RenamePath_DeniesEscapingDestination(t *testing.T) {
	root, outside, _ := outsideRoot(t)
	if err := os.WriteFile(filepath.Join(root, "real.txt"), []byte("keep me"), 0o644); err != nil {
		t.Fatalf("seed real.txt: %v", err)
	}
	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()
	openTestFolder(t, conn, root)
	_ = readMsg(t, conn)

	if err := conn.WriteJSON(clientMessage{Type: "rename_path", Path: "real.txt", NewPath: "../outside/stolen.txt"}); err != nil {
		t.Fatalf("write rename_path: %v", err)
	}
	resp := readMsg(t, conn)
	if resp.Type != "error" {
		t.Fatalf("response = %+v, want a real error", resp)
	}
	if _, err := os.Stat(filepath.Join(root, "real.txt")); err != nil {
		t.Fatalf("real.txt no longer exists inside root after a denied rename: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "stolen.txt")); err == nil {
		t.Fatal("rename escaped: stolen.txt now exists outside root")
	}
}

func TestResolvePath_RenamePath_DeniesEscapingSource(t *testing.T) {
	root, _, secretFile := outsideRoot(t)
	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()
	openTestFolder(t, conn, root)
	_ = readMsg(t, conn)

	if err := conn.WriteJSON(clientMessage{Type: "rename_path", Path: "../outside/secret.txt", NewPath: "grabbed.txt"}); err != nil {
		t.Fatalf("write rename_path: %v", err)
	}
	resp := readMsg(t, conn)
	if resp.Type != "error" {
		t.Fatalf("response = %+v, want a real error", resp)
	}
	if _, err := os.Stat(secretFile); err != nil {
		t.Fatalf("secret file was moved out of outside via traversal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "grabbed.txt")); err == nil {
		t.Fatal("rename escaped: grabbed.txt now exists inside root")
	}
}

// A NUL byte is a classic argument-truncation primitive at the syscall
// layer — real proof this doesn't quietly resolve to something
// unintended rather than erroring cleanly. Walks the entire real temp
// tree afterward (not just a targeted Stat) so a file landing *anywhere*
// unexpected is caught, precisely, rather than assumed absent.
func TestResolvePath_RejectsNulByteInPath(t *testing.T) {
	root, outside, _ := outsideRoot(t)
	base := filepath.Dir(root)
	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()
	openTestFolder(t, conn, root)
	_ = readMsg(t, conn)

	if err := conn.WriteJSON(clientMessage{Type: "create_file", Path: "evil.txt\x00../../outside/planted.txt"}); err != nil {
		t.Fatalf("write create_file: %v", err)
	}
	resp := readMsg(t, conn)

	var realFiles []string
	_ = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			realFiles = append(realFiles, path)
		}
		return nil
	})
	if resp.Type != "error" {
		t.Errorf("response = %+v, want a real error for a NUL-embedded path", resp)
	}
	for _, f := range realFiles {
		if f != filepath.Join(outside, "secret.txt") {
			t.Errorf("NUL-byte path created a real file at an unexpected location: %s (full tree: %v)", f, realFiles)
		}
	}
}

// A real symlink created inside root, pointing at a real file outside
// it, is the one class of attack a purely lexical check (Clean + a
// string HasPrefix) cannot catch by construction — resolvePath verifies
// the *string* stays under root, but never asks the OS to actually
// resolve the symlink and check where the real underlying file lives.
// Skipped, not silently passed, on a platform/privilege combination
// where this process can't even create the symlink to test with —
// Windows requires either Developer Mode or an elevated process for
// os.Symlink on files, so this is a real environmental skip, not an
// assumption the risk doesn't exist there too.
func TestResolvePath_SymlinkInsideRootPointingOutside(t *testing.T) {
	root, _, secretFile := outsideRoot(t)
	linkPath := filepath.Join(root, "innocuous-looking-link")
	if err := os.Symlink(secretFile, linkPath); err != nil {
		t.Skipf("cannot create a real symlink on this platform/privilege level, skipping: %v", err)
	}

	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()
	openTestFolder(t, conn, root)
	_ = readMsg(t, conn)

	if err := conn.WriteJSON(clientMessage{Type: "read_file", Path: "innocuous-looking-link"}); err != nil {
		t.Fatalf("write read_file: %v", err)
	}
	resp := readMsg(t, conn)
	if resp.Type == "file_content" {
		decoded, _ := base64.StdEncoding.DecodeString(resp.ContentBase64)
		t.Fatalf("REAL GAP: a symlink inside root pointing outside it was followed — read %q from outside root via a lexically-inside-looking path", decoded)
	}
}
