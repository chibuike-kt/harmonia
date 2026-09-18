// Package companion is the local companion process the standalone
// Harmonia IDE talks to — a small process a user runs once on their own
// machine, binding to localhost only, giving Harmonia's web page real
// access to a real local project: open a folder, browse/read/write its
// files, and run a real shell against it. This is deliberately not
// something the browser can do on its own — the File System Access API
// gives a *page* file access, never a separate local process a shell
// could run in, which is the whole point of a real terminal here.
//
// Never binds to anything but 127.0.0.1, and never accepts a WebSocket
// upgrade whose Origin isn't an explicitly allowed Harmonia origin —
// without that second check, any website a user happens to have open
// could silently open a WebSocket to this same local port and get a
// real shell on their machine. That's the one thing standing between
// "a helpful local dev tool" and a real, serious vulnerability, so it's
// enforced before a single message is ever read off the connection, not
// somewhere further down.
package companion

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

// AllowedOrigins is the set of web origins permitted to open a
// WebSocket to this companion — checked on every upgrade request, never
// skipped. A real deployment's origin (HARMONIA_APP_URL) plus the local
// dev server, never a wildcard: this is the actual access-control
// boundary, not a formality.
type AllowedOrigins map[string]bool

func NewAllowedOrigins(origins ...string) AllowedOrigins {
	m := make(AllowedOrigins, len(origins))
	for _, o := range origins {
		m[o] = true
	}
	return m
}

func (a AllowedOrigins) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	// No Origin header at all means this wasn't a browser-issued
	// cross-context request in the first place (e.g. a same-process
	// health check) — allowed; every real browser WebSocket handshake
	// always sends one.
	if origin == "" {
		return true
	}
	return a[origin]
}

// CheckOrigin: true unconditionally — gorilla/websocket's own default
// CheckOrigin independently rejects any Origin that doesn't match the
// request's own Host, which would fight with (and, being same-origin
// only, always lose against) the real allow-list check Handler already
// runs before Upgrade is ever called. That earlier check is the actual
// security boundary; this just stops gorilla's own from redundantly,
// incorrectly overriding it.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  8192,
	WriteBufferSize: 8192,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// clientMessage is every shape a connected web page can send — a tagged
// union over Type, matching the same "one envelope, several real
// payloads" shape this codebase's own realtime.Message already uses on
// the server-sent side. TerminalID/Cols/Rows are the real multi-terminal
// protocol's own fields — every terminal_* message is scoped to one
// real terminal instance by TerminalID, never an implicit "the" shell.
type clientMessage struct {
	Type          string `json:"type"`
	Path          string `json:"path,omitempty"`
	NewPath       string `json:"new_path,omitempty"`
	ContentBase64 string `json:"content_base64,omitempty"`
	Data          string `json:"data,omitempty"`
	Command       string `json:"command,omitempty"`
	Query         string `json:"query,omitempty"`
	TerminalID    string `json:"terminal_id,omitempty"`
	Cols          int    `json:"cols,omitempty"`
	Rows          int    `json:"rows,omitempty"`
}

type serverMessage struct {
	Type          string          `json:"type"`
	Path          string          `json:"path,omitempty"`
	NewPath       string          `json:"new_path,omitempty"`
	Entries       []DirEntry      `json:"entries,omitempty"`
	ContentBase64 string          `json:"content_base64,omitempty"`
	Data          string          `json:"data,omitempty"`
	ExitCode      *int            `json:"exit_code,omitempty"`
	Message       string          `json:"message,omitempty"`
	TerminalID    string          `json:"terminal_id,omitempty"`
	Cwd           string          `json:"cwd,omitempty"`
	GitBranch     string          `json:"git_branch,omitempty"`
	GitFiles      []GitFileStatus `json:"git_files,omitempty"`
	GitRepo       bool            `json:"git_repo,omitempty"`
	SearchResults []SearchMatch   `json:"search_results,omitempty"`
	SearchQuery   string          `json:"search_query,omitempty"`
}

type DirEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
}

// Handler returns the http.HandlerFunc a companion binary serves its one
// real endpoint on — GET /ws, upgraded to a WebSocket. Each connection
// gets its own Session (its own open folder, its own shell process, if
// any); nothing here is shared across connections, the same "no
// ambient state" posture a genuinely local, single-user tool should
// have.
//
// Refuses the upgrade outright — before Origin is even checked — unless
// consent.Granted() is true. This is the actual enforcement point ADR-010
// requires: the consent screen the frontend shows is real, but a screen
// alone is just UI a page could skip by opening the WebSocket directly.
// This check is what makes skipping it impossible rather than merely
// discouraged.
func Handler(allowed AllowedOrigins, consent *ConsentStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !consent.Granted() {
			http.Error(w, "consent required", http.StatusPreconditionRequired)
			return
		}
		if !allowed.checkOrigin(r) {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("companion: upgrade failed: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()

		s := &session{conn: conn, terminals: make(map[string]*terminal), done: make(chan struct{})}
		if sessionCreated != nil {
			sessionCreated(s)
		}
		defer close(s.done)
		s.serve()
	}
}

// sessionCreated, set only by this package's own tests, is invoked with
// every session Handler creates — the one way a test can observe real
// session teardown (every terminal's real OS process actually exited,
// not just had its handles closed; see stopAllTerminals) instead of
// racing httptest.Server.Close, which does not wait for hijacked
// connections (a WebSocket upgrade hijacks the connection, so Close
// returns without waiting for serve() to return).
var sessionCreated func(*session)

// session is one connected web page's own state — the folder it opened
// (if any) and every real terminal instance it has started. Every file
// operation is checked against rootDir before it touches disk (see
// resolvePath) — even on a trusted local process, a page that somehow
// sent a crafted path shouldn't be able to read or write outside the
// folder the human actually opened.
type session struct {
	conn *websocket.Conn

	mu      sync.Mutex
	rootDir string

	termMu    sync.Mutex
	terminals map[string]*terminal
	termWG    sync.WaitGroup

	done chan struct{}
}

func (s *session) send(msg serverMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.conn.WriteJSON(msg); err != nil {
		log.Printf("companion: write failed: %v", err)
	}
}

func (s *session) sendError(format string, args ...any) {
	s.send(serverMessage{Type: "error", Message: fmt.Sprintf(format, args...)})
}

func (s *session) serve() {
	defer s.stopAllTerminals()
	for {
		var msg clientMessage
		if err := s.conn.ReadJSON(&msg); err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Printf("companion: read failed: %v", err)
			}
			return
		}
		s.handle(msg)
	}
}

func (s *session) handle(msg clientMessage) {
	switch msg.Type {
	case "pick_folder":
		s.pickFolder()
	case "open_folder":
		s.openFolder(msg.Path)
	case "list_dir":
		s.listDir(msg.Path)
	case "read_file":
		s.readFile(msg.Path)
	case "write_file":
		s.writeFile(msg.Path, msg.ContentBase64)
	case "create_file":
		s.createFile(msg.Path)
	case "create_folder":
		s.createFolder(msg.Path)
	case "delete_path":
		s.deletePath(msg.Path)
	case "rename_path":
		s.renamePath(msg.Path, msg.NewPath)
	case "git_status":
		s.gitStatus()
	case "git_stage":
		s.gitStage(msg.Path)
	case "git_unstage":
		s.gitUnstage(msg.Path)
	case "git_commit":
		s.gitCommit(msg.Data)
	case "search_text":
		s.searchText(msg.Query)
	case "create_terminal":
		s.createTerminal(msg.Cols, msg.Rows)
	case "terminal_input":
		s.terminalInput(msg.TerminalID, msg.Data)
	case "terminal_resize":
		s.resizeTerminal(msg.TerminalID, msg.Cols, msg.Rows)
	case "kill_terminal":
		s.killTerminal(msg.TerminalID)
	case "terminal_narrate":
		s.narrateTerminal(msg.TerminalID, msg.Data)
	default:
		s.sendError("unknown message type %q", msg.Type)
	}
}

func (s *session) openFolder(path string) {
	info, err := os.Stat(path)
	if err != nil {
		s.sendError("open folder: %v", err)
		return
	}
	if !info.IsDir() {
		s.sendError("open folder: %q is not a directory", path)
		return
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		s.sendError("open folder: %v", err)
		return
	}
	s.mu.Lock()
	s.rootDir = abs
	s.mu.Unlock()
	s.send(serverMessage{Type: "folder_opened", Path: abs})
}

// resolvePath maps a client-supplied path (relative to the open
// folder — a real path within it, e.g. "src/main.go") to a real,
// verified-inside-root absolute path. Never trusts the client's own
// claim of where a ".." might legitimately land.
func (s *session) resolvePath(rel string) (string, error) {
	s.mu.Lock()
	root := s.rootDir
	s.mu.Unlock()
	if root == "" {
		return "", errors.New("no folder is open")
	}
	full := filepath.Clean(filepath.Join(root, rel))
	if full != root && !strings.HasPrefix(full, root+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the open folder", rel)
	}
	return full, nil
}

func (s *session) listDir(rel string) {
	dir, err := s.resolvePath(rel)
	if err != nil {
		s.sendError("list dir: %v", err)
		return
	}
	if err := s.sendDirListing(rel, dir); err != nil {
		s.sendError("list dir: %v", err)
	}
}

// sendDirListing is listDir's own real work, factored out so
// create/delete/rename below can push a fresh listing for whatever
// directory they just changed — the exact same real dir_listing message
// the frontend's file tree already knows how to apply, so a create,
// delete, or rename shows up there with no new client-side message type
// to handle.
func (s *session) sendDirListing(rel, dir string) error {
	items, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	entries := make([]DirEntry, 0, len(items))
	for _, it := range items {
		entries = append(entries, DirEntry{
			Name:  it.Name(),
			Path:  filepath.ToSlash(filepath.Join(rel, it.Name())),
			IsDir: it.IsDir(),
		})
	}
	s.send(serverMessage{Type: "dir_listing", Path: rel, Entries: entries})
	return nil
}

// refreshDir best-effort re-sends a directory's listing after a real
// create/delete/rename — silent on failure (the parent might no longer
// exist at all, e.g. after deleting the last entry of an already-removed
// directory), since the operation's own real success or failure was
// already reported to the human via its own serverMessage/sendError.
func (s *session) refreshDir(rel string) {
	dir, err := s.resolvePath(rel)
	if err != nil {
		return
	}
	_ = s.sendDirListing(rel, dir)
}

// parentRel returns rel's own parent directory, in the same "/"-joined,
// root-relative shape every path in this protocol already uses — "" for
// a top-level entry, meaning the open folder's own root listing.
func parentRel(rel string) string {
	rel = strings.TrimSuffix(filepath.ToSlash(rel), "/")
	idx := strings.LastIndex(rel, "/")
	if idx < 0 {
		return ""
	}
	return rel[:idx]
}

// createFile makes a real, empty file at rel — real O_EXCL so this can
// never silently truncate something already there; New File in the
// Explorer's own real right-click menu is this call's one real caller.
func (s *session) createFile(rel string) {
	full, err := s.resolvePath(rel)
	if err != nil {
		s.sendError("create file: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		s.sendError("create file: %v", err)
		return
	}
	f, err := os.OpenFile(full, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		s.sendError("create file: %v", err)
		return
	}
	_ = f.Close()
	s.send(serverMessage{Type: "path_created", Path: rel})
	s.refreshDir(parentRel(rel))
}

// createFolder makes a real directory at rel, including any missing
// real parent directories — New Folder's one real caller.
func (s *session) createFolder(rel string) {
	full, err := s.resolvePath(rel)
	if err != nil {
		s.sendError("create folder: %v", err)
		return
	}
	if _, err := os.Stat(full); err == nil {
		s.sendError("create folder: %q already exists", rel)
		return
	}
	if err := os.MkdirAll(full, 0o755); err != nil {
		s.sendError("create folder: %v", err)
		return
	}
	s.send(serverMessage{Type: "path_created", Path: rel})
	s.refreshDir(parentRel(rel))
}

// deletePath removes a real file or, recursively, a real directory — the
// Explorer's own real right-click Delete. Refuses to delete the open
// folder's own root ("" resolves to rootDir itself, and root can never
// legitimately be one of its own listing's entries, so a real rel here
// is always something a human explicitly chose in the tree).
func (s *session) deletePath(rel string) {
	if rel == "" {
		s.sendError("delete: cannot delete the open folder itself")
		return
	}
	full, err := s.resolvePath(rel)
	if err != nil {
		s.sendError("delete: %v", err)
		return
	}
	if err := os.RemoveAll(full); err != nil {
		s.sendError("delete: %v", err)
		return
	}
	s.send(serverMessage{Type: "path_deleted", Path: rel})
	s.refreshDir(parentRel(rel))
}

// renamePath moves rel to newRel, both still real, verified-inside-root
// paths — the Explorer's own real right-click Rename (a rename to a
// path in the same directory) and, generally, a real move to a different
// one. Refuses to silently overwrite an existing real file or folder at
// the destination.
func (s *session) renamePath(rel, newRel string) {
	full, err := s.resolvePath(rel)
	if err != nil {
		s.sendError("rename: %v", err)
		return
	}
	newFull, err := s.resolvePath(newRel)
	if err != nil {
		s.sendError("rename: %v", err)
		return
	}
	if _, err := os.Stat(newFull); err == nil {
		s.sendError("rename: %q already exists", newRel)
		return
	}
	if err := os.Rename(full, newFull); err != nil {
		s.sendError("rename: %v", err)
		return
	}
	s.send(serverMessage{Type: "path_renamed", Path: rel, NewPath: newRel})
	oldParent, newParent := parentRel(rel), parentRel(newRel)
	s.refreshDir(oldParent)
	if newParent != oldParent {
		s.refreshDir(newParent)
	}
}

func (s *session) readFile(rel string) {
	full, err := s.resolvePath(rel)
	if err != nil {
		s.sendError("read file: %v", err)
		return
	}
	data, err := os.ReadFile(full)
	if err != nil {
		s.sendError("read file: %v", err)
		return
	}
	s.send(serverMessage{
		Type:          "file_content",
		Path:          rel,
		ContentBase64: base64.StdEncoding.EncodeToString(data),
	})
}

func (s *session) writeFile(rel, contentBase64 string) {
	full, err := s.resolvePath(rel)
	if err != nil {
		s.sendError("write file: %v", err)
		return
	}
	data, err := base64.StdEncoding.DecodeString(contentBase64)
	if err != nil {
		s.sendError("write file: invalid content: %v", err)
		return
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		s.sendError("write file: %v", err)
		return
	}
	s.send(serverMessage{Type: "file_written", Path: rel})
}
