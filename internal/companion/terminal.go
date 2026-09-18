package companion

import (
	"os"
	"regexp"
	"sync"

	"github.com/google/uuid"
)

// terminal is one real terminal instance — its own real PTY-backed
// shell process, its own real working directory (tracked live via the
// shell-integration OSC hook, not just the folder it started in), fully
// independent of every other terminal this session has open. Multiple
// terminals, side-by-side splits, and independent kill/restart are all
// just real operations on this struct and the session's own map of
// them — there is no single "the shell" anymore.
type terminal struct {
	id  string
	pty PTY

	mu  sync.Mutex
	cwd string
}

func (t *terminal) setCwd(cwd string) {
	t.mu.Lock()
	t.cwd = cwd
	t.mu.Unlock()
}

// cwdOSCPattern matches the shell-integration hook's own real OSC 9;9
// "current working directory" announcement (see pty_windows.go's own
// doc comment) — real escape-sequence parsing against real shell
// output, not a heuristic guess at typed `cd` commands, and correct
// regardless of how the human or an agent actually changed directory
// (cd, pushd, a script, an alias). The terminator is either BEL (\a) or
// ST (ESC \\), both real, standard OSC terminators.
var cwdOSCPattern = regexp.MustCompile("\x1b\\]9;9;([^\a\x1b]*)(?:\a|\x1b\\\\)")

// defaultTerminalCols/Rows seed a new terminal before the frontend's own
// real ResizeObserver reports the panel's actual size and sends a real
// terminal_resize — a reasonable starting size, not a magic number: the
// same 80x24 default a real terminal emulator would assume.
const (
	defaultTerminalCols = 80
	defaultTerminalRows = 24
)

// createTerminal starts a brand-new real terminal instance, cwd'd at the
// session's open folder. Real multi-instance support: this session can
// have as many of these running concurrently as the human opens, each
// fully independent.
func (s *session) createTerminal(cols, rows int) {
	s.mu.Lock()
	root := s.rootDir
	s.mu.Unlock()
	if root == "" {
		// A terminal is genuinely useful before any project is open — git
		// clone something, look around, run a command before deciding
		// what to open. Falls back to the real user home directory, the
		// same starting point a fresh terminal window on this machine
		// would use, rather than refusing outright.
		home, err := os.UserHomeDir()
		if err != nil {
			s.sendError("create terminal: %v", err)
			return
		}
		root = home
	}
	if cols <= 0 {
		cols = defaultTerminalCols
	}
	if rows <= 0 {
		rows = defaultTerminalRows
	}

	p, err := startPTY(root, cols, rows)
	if err != nil {
		s.sendError("create terminal: %v", err)
		return
	}

	term := &terminal{id: uuid.NewString(), pty: p, cwd: root}
	s.termMu.Lock()
	s.terminals[term.id] = term
	s.termMu.Unlock()

	// The shell-integration hook is real input to the real shell, sent
	// exactly like anything a human would type — not a side channel.
	_, _ = p.Write([]byte(shellIntegrationScript() + "\r\n"))

	s.send(serverMessage{Type: "terminal_created", TerminalID: term.id, Cwd: root})

	go s.pumpTerminalOutput(term)
	s.termWG.Add(1)
	go s.waitTerminal(term)
}

func (s *session) getTerminal(id string) (*terminal, bool) {
	s.termMu.Lock()
	defer s.termMu.Unlock()
	t, ok := s.terminals[id]
	return t, ok
}

func (s *session) terminalInput(id, data string) {
	t, ok := s.getTerminal(id)
	if !ok {
		s.sendError("terminal input: no such terminal %q", id)
		return
	}
	if _, err := t.pty.Write([]byte(data)); err != nil {
		s.sendError("terminal input: %v", err)
	}
}

func (s *session) resizeTerminal(id string, cols, rows int) {
	t, ok := s.getTerminal(id)
	if !ok {
		// A resize racing a just-exited terminal is routine (the
		// panel's own ResizeObserver doesn't know that yet) — silent,
		// not a real error the human needs to see.
		return
	}
	// A real floor, not an arbitrary one: found live that resizing the
	// real PTY this small makes a real PowerShell/PSReadLine session
	// exit outright on its very next keystroke — PSReadLine's own
	// real minimum usable console size, not a client bug this server
	// should have to trust every caller to avoid on its own. A client-
	// side layout race (the frontend's own ResizeObserver firing before
	// its container has a real laid-out size) is the one real caller
	// this has been seen from; rejecting it here protects the real
	// shell regardless of what any future caller sends.
	const minCols, minRows = 10, 3
	if cols < minCols || rows < minRows {
		return
	}
	if err := t.pty.Resize(cols, rows); err != nil {
		s.sendError("resize terminal: %v", err)
	}
}

// killTerminal ends one real terminal instance independently of every
// other one this session has open.
func (s *session) killTerminal(id string) {
	t, ok := s.getTerminal(id)
	if !ok {
		return
	}
	_ = t.pty.Close()
	// waitTerminal's own goroutine removes it from the map and sends
	// terminal_exited once Close() actually finishes tearing the real
	// process down — a real, observed exit, not this handler's own
	// optimistic guess that it worked.
}

// stopAllTerminals closes every real terminal's PTY and blocks until each
// one's underlying OS process has genuinely finished exiting (not just
// had its handles closed) — the session's own connection teardown (see
// serve's defer) must not return while a real child process is still
// tearing down, or a caller reusing the session's open folder right
// after disconnect (this package's own tests included) can race a
// process that still holds it open (e.g. as its working directory).
// narrateTerminal writes agent narration onto the same real terminal
// output stream real PTY output arrives on — one real xterm.js renderer,
// no separate fake overlay for an agent's conversational lines. Sent
// straight to the browser as a synthetic terminal_output, never through
// the PTY's own stdin: writing it there would hand the text to the real
// shell as if a human had typed it, and a real shell would try to
// execute "[ChatGPT] taking a look at this now" as a command. text is
// expected to already carry its own real ANSI SGR styling (bold, a
// distinct color, a bracketed name prefix) — this layer doesn't know
// agent names or colors, the caller relaying the action does.
func (s *session) narrateTerminal(id, text string) {
	if _, ok := s.getTerminal(id); !ok {
		s.sendError("terminal narrate: no such terminal %q", id)
		return
	}
	// A full blank line before every narration line, not just a single
	// \r\n — real readability fix, found live: narration ran visually
	// flush against whatever real output preceded it (a shell prompt, a
	// command's own output), with only ANSI color carrying the
	// distinction between "the shell said this" and "an agent said
	// this." A real blank line is a much stronger, color-independent
	// separator; the single trailing \r\n is enough after, since
	// whatever comes next (a real prompt reappearing, or another
	// narration line, which now always leads with its own blank line)
	// supplies its own separation.
	s.send(serverMessage{Type: "terminal_output", TerminalID: id, Data: "\r\n\r\n" + text + "\r\n"})
}

func (s *session) stopAllTerminals() {
	s.termMu.Lock()
	terms := make([]*terminal, 0, len(s.terminals))
	for _, t := range s.terminals {
		terms = append(terms, t)
	}
	s.termMu.Unlock()
	for _, t := range terms {
		_ = t.pty.Close()
	}
	s.termWG.Wait()
}

func (s *session) pumpTerminalOutput(t *terminal) {
	buf := make([]byte, 4096)
	var pending []byte
	for {
		n, err := t.pty.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
			// Real cwd changes are extracted and stripped before the
			// remaining real output is sent — see cwdOSCPattern's own
			// doc comment. Only fully-buffered matches are consumed;
			// anything after the last complete match is held back in
			// case a match is split across two real reads.
			lastMatchEnd := 0
			for _, loc := range cwdOSCPattern.FindAllSubmatchIndex(pending, -1) {
				cwd := string(pending[loc[2]:loc[3]])
				t.setCwd(cwd)
				s.send(serverMessage{Type: "terminal_cwd", TerminalID: t.id, Cwd: cwd})
				lastMatchEnd = loc[1]
			}
			toSend := pending[:lastMatchEnd]
			remaining := append([]byte(nil), pending[lastMatchEnd:]...)
			pending = remaining
			// Never hold back real output indefinitely waiting for a
			// match that isn't coming — an unmatched ESC near the end of
			// `pending` is the only real reason to hold anything back at
			// all.
			if idx := lastEscapeCandidate(pending); idx >= 0 {
				toSend = append(toSend, pending[:idx]...)
				pending = pending[idx:]
			} else {
				toSend = append(toSend, pending...)
				pending = nil
			}
			if len(toSend) > 0 {
				s.send(serverMessage{Type: "terminal_output", TerminalID: t.id, Data: string(toSend)})
			}
		}
		if err != nil {
			return
		}
	}
}

// lastEscapeCandidate finds the last ESC byte in buf that could still be
// the start of an as-yet-incomplete cwd OSC sequence — real reads can
// split a real escape sequence across two Read() calls, and flushing a
// half-written one as visible terminal output would show raw escape
// bytes instead of silently completing the match on the next read.
// Returns -1 when nothing in buf could be a partial match.
func lastEscapeCandidate(buf []byte) int {
	for i := len(buf) - 1; i >= 0; i-- {
		if buf[i] == 0x1b {
			return i
		}
	}
	return -1
}

func (s *session) waitTerminal(t *terminal) {
	defer s.termWG.Done()
	code, _ := t.pty.Wait()
	s.termMu.Lock()
	delete(s.terminals, t.id)
	s.termMu.Unlock()
	s.send(serverMessage{Type: "terminal_exited", TerminalID: t.id, ExitCode: &code})
}
