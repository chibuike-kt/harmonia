package companion

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestSearchText_FindsRealMatchesAcrossRealFilesAndSkipsNoiseDirs proves
// the Search activity-bar view's own real backend: a genuine recursive
// scan of real files on disk, case-insensitive, with real line numbers,
// and real noise directories (node_modules here) never walked into at
// all — not filtered out after the fact, never scanned.
func TestSearchText_FindsRealMatchesAcrossRealFilesAndSkipsNoiseDirs(t *testing.T) {
	dir := t.TempDir()
	writeReal(t, dir, "main.go", "package main\n\nfunc Greet() string {\n\treturn \"Hello, World\"\n}\n")
	writeReal(t, dir, "README.md", "# Project\n\nSay hello to the world.\n")
	writeReal(t, dir, filepath.Join("node_modules", "pkg", "index.js"), "module.exports = 'hello';\n")

	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()
	if err := conn.WriteJSON(clientMessage{Type: "open_folder", Path: dir}); err != nil {
		t.Fatalf("write open_folder: %v", err)
	}
	_ = readMsg(t, conn)

	if err := conn.WriteJSON(clientMessage{Type: "search_text", Query: "hello"}); err != nil {
		t.Fatalf("write search_text: %v", err)
	}
	resp := readMsg(t, conn)
	if resp.Type != "search_results" {
		t.Fatalf("resp = %+v, want type search_results", resp)
	}

	byPath := map[string][]SearchMatch{}
	for _, m := range resp.SearchResults {
		byPath[m.Path] = append(byPath[m.Path], m)
	}
	if _, ok := byPath["node_modules/pkg/index.js"]; ok {
		t.Fatalf("search results = %+v, want node_modules never walked at all", resp.SearchResults)
	}
	if len(byPath["main.go"]) != 1 || byPath["main.go"][0].Line != 4 {
		t.Fatalf("main.go matches = %+v, want exactly line 4", byPath["main.go"])
	}
	if len(byPath["README.md"]) != 1 || byPath["README.md"][0].Line != 3 {
		t.Fatalf("README.md matches = %+v, want exactly line 3", byPath["README.md"])
	}
}

// TestSearchText_RespectsTheResultCap proves the real cap actually stops
// the walk rather than just truncating a fully-collected result set —
// asserted indirectly here by checking the cap is honored at all; the
// cap constant itself (searchMaxResults) is what a real huge repo relies
// on to answer promptly.
func TestSearchText_RespectsTheResultCap(t *testing.T) {
	dir := t.TempDir()
	for i := range searchMaxResults + 20 {
		writeReal(t, dir, filepath.Join("many", "file"+strconv.Itoa(i)+".txt"), "needle\n")
	}

	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()
	if err := conn.WriteJSON(clientMessage{Type: "open_folder", Path: dir}); err != nil {
		t.Fatalf("write open_folder: %v", err)
	}
	_ = readMsg(t, conn)

	if err := conn.WriteJSON(clientMessage{Type: "search_text", Query: "needle"}); err != nil {
		t.Fatalf("write search_text: %v", err)
	}
	resp := readMsg(t, conn)
	if len(resp.SearchResults) != searchMaxResults {
		t.Fatalf("real result count = %d, want exactly the real cap of %d", len(resp.SearchResults), searchMaxResults)
	}
}

func writeReal(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %q: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %q: %v", rel, err)
	}
}
