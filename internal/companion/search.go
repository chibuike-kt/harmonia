package companion

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// SearchMatch is one real matching line from searchText — the Search
// activity-bar view's own real result shape: enough to render a
// clickable "path:line — text" row and open that exact location, the
// same real information a real `grep -n` line carries.
type SearchMatch struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// searchMaxResults bounds a real search against a real, potentially
// large project — a real cap, not a soft target: WalkDir stops the
// instant it's reached, so a broad query against a big repo returns
// promptly instead of scanning everything before answering.
const searchMaxResults = 500

// searchMaxFileSize skips anything bigger than this outright — a real,
// cheap proxy for "probably not a source file worth searching" (a
// lockfile, a bundled asset, a binary) without needing real content-type
// sniffing.
const searchMaxFileSize = 2 << 20 // 2 MiB

// searchSkipDirs are real directories no real project search should ever
// walk into — dependency trees and build output a human searching their
// own source is never looking inside, and which would otherwise dominate
// both the time this takes and the real result cap above.
var searchSkipDirs = map[string]bool{
	".git": true, "node_modules": true, ".next": true, "dist": true,
	"build": true, "target": true, "vendor": true, ".venv": true,
	"venv": true, "__pycache__": true, ".turbo": true,
}

// searchText walks the real open folder and returns every real line
// containing query, case-insensitive — Search's own one real caller.
// Real files, real bytes, real line numbers; no index, no cache, nothing
// pretending to be smarter than a real recursive scan of what's on disk
// right now.
func (s *session) searchText(query string) {
	root, err := s.gitRoot()
	if err != nil {
		s.sendError("search: %v", err)
		return
	}
	query = strings.TrimSpace(query)
	if query == "" {
		s.send(serverMessage{Type: "search_results", SearchQuery: query})
		return
	}
	lowerQuery := strings.ToLower(query)

	var results []SearchMatch
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if len(results) >= searchMaxResults {
			return filepath.SkipAll
		}
		if d.IsDir() {
			if path != root && searchSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > searchMaxFileSize || info.Size() == 0 {
			return nil
		}
		results = append(results, searchFile(root, path, lowerQuery, searchMaxResults-len(results))...)
		return nil
	})
	s.send(serverMessage{Type: "search_results", SearchQuery: query, SearchResults: results})
}

// searchFile scans one real file line by line — its own function so a
// single file's scan error (a genuinely binary file tripping bufio's
// real line-length limit) only skips that file, never the walk as a
// whole.
func searchFile(root, path, lowerQuery string, limit int) []SearchMatch {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	rel, err := filepath.Rel(root, path)
	if err != nil {
		return nil
	}
	rel = filepath.ToSlash(rel)

	var matches []SearchMatch
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if strings.Contains(strings.ToLower(line), lowerQuery) {
			matches = append(matches, SearchMatch{Path: rel, Line: lineNo, Text: strings.TrimSpace(line)})
			if len(matches) >= limit {
				break
			}
		}
	}
	// Best-effort: a real mid-file scan error (bufio's own line-length
	// limit hitting a genuinely binary file) just means this file's
	// results stop where they are, not that the whole search fails.
	_ = scanner.Err()
	return matches
}
