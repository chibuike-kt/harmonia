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
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
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
// the server-sent side.
type clientMessage struct {
	Type          string `json:"type"`
	Path          string `json:"path,omitempty"`
	ContentBase64 string `json:"content_base64,omitempty"`
	Data          string `json:"data,omitempty"`
	Command       string `json:"command,omitempty"`
}

type serverMessage struct {
	Type          string     `json:"type"`
	Path          string     `json:"path,omitempty"`
	Entries       []DirEntry `json:"entries,omitempty"`
	ContentBase64 string     `json:"content_base64,omitempty"`
	Data          string     `json:"data,omitempty"`
	ExitCode      *int       `json:"exit_code,omitempty"`
	Message       string     `json:"message,omitempty"`
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

		s := &session{conn: conn}
		s.serve()
	}
}

// session is one connected web page's own state — the folder it opened
// (if any) and the shell process it started (if any). Every file
// operation is checked against rootDir before it touches disk (see
// resolvePath) — even on a trusted local process, a page that somehow
// sent a crafted path shouldn't be able to read or write outside the
// folder the human actually opened.
type session struct {
	conn *websocket.Conn

	mu      sync.Mutex
	rootDir string

	shellMu  sync.Mutex
	shellCmd *exec.Cmd
	shellIn  io.WriteCloser
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
	defer s.stopShell()
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
	case "open_folder":
		s.openFolder(msg.Path)
	case "list_dir":
		s.listDir(msg.Path)
	case "read_file":
		s.readFile(msg.Path)
	case "write_file":
		s.writeFile(msg.Path, msg.ContentBase64)
	case "start_shell":
		s.startShell()
	case "shell_input":
		s.writeShellInput(msg.Data)
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
	items, err := os.ReadDir(dir)
	if err != nil {
		s.sendError("list dir: %v", err)
		return
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
