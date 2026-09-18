package companion

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCreateFile_MakesARealEmptyFileAndRefreshesTheListing proves New
// File's real effect end to end: a real, empty file lands on disk at
// exactly the path asked for, and the parent directory's own real
// dir_listing arrives unprompted so the Explorer's tree updates without
// a second round trip.
func TestCreateFile_MakesARealEmptyFileAndRefreshesTheListing(t *testing.T) {
	dir := t.TempDir()
	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()

	if err := conn.WriteJSON(clientMessage{Type: "open_folder", Path: dir}); err != nil {
		t.Fatalf("write open_folder: %v", err)
	}
	_ = readMsg(t, conn) // folder_opened

	if err := conn.WriteJSON(clientMessage{Type: "create_file", Path: "notes.md"}); err != nil {
		t.Fatalf("write create_file: %v", err)
	}
	created := readMsg(t, conn)
	if created.Type != "path_created" || created.Path != "notes.md" {
		t.Fatalf("created = %+v, want path_created for notes.md", created)
	}
	listing := readMsg(t, conn)
	if listing.Type != "dir_listing" || listing.Path != "" {
		t.Fatalf("listing = %+v, want a real root dir_listing", listing)
	}

	info, err := os.Stat(filepath.Join(dir, "notes.md"))
	if err != nil {
		t.Fatalf("real file was not created on disk: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("real file size = %d, want 0 (empty)", info.Size())
	}
}

// TestCreateFile_RefusesToOverwriteAnExistingFile proves the real O_EXCL
// guard — New File must never silently truncate something already there.
func TestCreateFile_RefusesToOverwriteAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("real content"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()
	if err := conn.WriteJSON(clientMessage{Type: "open_folder", Path: dir}); err != nil {
		t.Fatalf("write open_folder: %v", err)
	}
	_ = readMsg(t, conn)

	if err := conn.WriteJSON(clientMessage{Type: "create_file", Path: "existing.txt"}); err != nil {
		t.Fatalf("write create_file: %v", err)
	}
	resp := readMsg(t, conn)
	if resp.Type != "error" {
		t.Fatalf("response = %+v, want a real error", resp)
	}
	onDisk, err := os.ReadFile(filepath.Join(dir, "existing.txt"))
	if err != nil || string(onDisk) != "real content" {
		t.Fatalf("existing file was touched: content = %q (err=%v), want unchanged", onDisk, err)
	}
}

// TestCreateFolder_DeletePath_RenamePath_AllTakeRealEffectOnDisk chains
// all three remaining Explorer context-menu operations against one real
// temp directory, checking the real filesystem after each — not the
// companion's own claim of success.
func TestCreateFolder_DeletePath_RenamePath_AllTakeRealEffectOnDisk(t *testing.T) {
	dir := t.TempDir()
	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()
	if err := conn.WriteJSON(clientMessage{Type: "open_folder", Path: dir}); err != nil {
		t.Fatalf("write open_folder: %v", err)
	}
	_ = readMsg(t, conn)

	// create_folder
	if err := conn.WriteJSON(clientMessage{Type: "create_folder", Path: "pkg"}); err != nil {
		t.Fatalf("write create_folder: %v", err)
	}
	created := readMsg(t, conn)
	if created.Type != "path_created" {
		t.Fatalf("created = %+v, want path_created", created)
	}
	_ = readMsg(t, conn) // real dir_listing refresh
	info, err := os.Stat(filepath.Join(dir, "pkg"))
	if err != nil || !info.IsDir() {
		t.Fatalf("real directory was not created: %v", err)
	}

	// rename_path: pkg -> lib
	if err := conn.WriteJSON(clientMessage{Type: "rename_path", Path: "pkg", NewPath: "lib"}); err != nil {
		t.Fatalf("write rename_path: %v", err)
	}
	renamed := readMsg(t, conn)
	if renamed.Type != "path_renamed" || renamed.Path != "pkg" || renamed.NewPath != "lib" {
		t.Fatalf("renamed = %+v, want path_renamed pkg -> lib", renamed)
	}
	_ = readMsg(t, conn) // real dir_listing refresh
	if _, err := os.Stat(filepath.Join(dir, "pkg")); !os.IsNotExist(err) {
		t.Fatalf("old path %q still exists on disk after rename", "pkg")
	}
	if info, err := os.Stat(filepath.Join(dir, "lib")); err != nil || !info.IsDir() {
		t.Fatalf("renamed directory does not exist on disk: %v", err)
	}

	// delete_path: lib
	if err := conn.WriteJSON(clientMessage{Type: "delete_path", Path: "lib"}); err != nil {
		t.Fatalf("write delete_path: %v", err)
	}
	deleted := readMsg(t, conn)
	if deleted.Type != "path_deleted" || deleted.Path != "lib" {
		t.Fatalf("deleted = %+v, want path_deleted lib", deleted)
	}
	_ = readMsg(t, conn) // real dir_listing refresh
	if _, err := os.Stat(filepath.Join(dir, "lib")); !os.IsNotExist(err) {
		t.Fatalf("deleted directory still exists on disk")
	}
}

// TestRenamePath_RefusesToOverwriteAnExistingDestination proves rename
// never silently clobbers something already at the destination.
func TestRenamePath_RefusesToOverwriteAnExistingDestination(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatalf("seed b.txt: %v", err)
	}
	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()
	if err := conn.WriteJSON(clientMessage{Type: "open_folder", Path: dir}); err != nil {
		t.Fatalf("write open_folder: %v", err)
	}
	_ = readMsg(t, conn)

	if err := conn.WriteJSON(clientMessage{Type: "rename_path", Path: "a.txt", NewPath: "b.txt"}); err != nil {
		t.Fatalf("write rename_path: %v", err)
	}
	resp := readMsg(t, conn)
	if resp.Type != "error" {
		t.Fatalf("response = %+v, want a real error", resp)
	}
	onDisk, err := os.ReadFile(filepath.Join(dir, "b.txt"))
	if err != nil || string(onDisk) != "b" {
		t.Fatalf("destination file was overwritten: content = %q (err=%v), want unchanged %q", onDisk, err, "b")
	}
}

// TestDeletePath_RefusesToDeleteTheOpenFolderItself is the one real
// guard delete_path needs beyond resolvePath's own traversal check —
// rel == "" resolves to rootDir itself, and no real Explorer selection
// can ever be the open folder's own root.
func TestDeletePath_RefusesToDeleteTheOpenFolderItself(t *testing.T) {
	dir := t.TempDir()
	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()
	if err := conn.WriteJSON(clientMessage{Type: "open_folder", Path: dir}); err != nil {
		t.Fatalf("write open_folder: %v", err)
	}
	_ = readMsg(t, conn)

	if err := conn.WriteJSON(clientMessage{Type: "delete_path", Path: ""}); err != nil {
		t.Fatalf("write delete_path: %v", err)
	}
	resp := readMsg(t, conn)
	if resp.Type != "error" {
		t.Fatalf("response = %+v, want a real error", resp)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the open folder itself was deleted: %v", err)
	}
}
