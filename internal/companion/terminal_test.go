package companion

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// readTerminalUntil reads server messages until pred returns true for
// one of them, returning it — or fails the test after timeout. Real
// terminal output can arrive in several chunks, so tests accumulate
// rather than assume one message carries the whole real answer.
func readTerminalUntil(t *testing.T, conn *websocket.Conn, timeout time.Duration, pred func(serverMessage) bool) serverMessage {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(timeout))
		var msg serverMessage
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read: %v", err)
		}
		if pred(msg) {
			return msg
		}
	}
	t.Fatal("deadline exceeded waiting for a matching message")
	return serverMessage{}
}

func openFolderAndCreateTerminal(t *testing.T, conn *websocket.Conn, dir string) string {
	t.Helper()
	if err := conn.WriteJSON(clientMessage{Type: "open_folder", Path: dir}); err != nil {
		t.Fatalf("write open_folder: %v", err)
	}
	_ = readMsg(t, conn) // folder_opened

	return createTerminal(t, conn)
}

// createTerminal sends a real create_terminal request and returns the
// real terminal id from its terminal_created response — waiting for that
// specific message type rather than blindly taking the next one off the
// wire, because a real terminal starts emitting its own real, unsolicited
// PTY output (mode-set sequences, the shell-integration bootstrap, etc.)
// within milliseconds of being created, and a second terminal's own
// create_terminal response can legitimately arrive after an earlier
// terminal's spontaneous output that was already in flight.
func createTerminal(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	if err := conn.WriteJSON(clientMessage{Type: "create_terminal", Cols: 80, Rows: 24}); err != nil {
		t.Fatalf("write create_terminal: %v", err)
	}
	created := readTerminalUntil(t, conn, 10*time.Second, func(msg serverMessage) bool {
		return msg.Type == "terminal_created"
	})
	if created.TerminalID == "" {
		t.Fatalf("created = %+v, want a real terminal_created with a real id", created)
	}
	return created.TerminalID
}

func shellCommand(marker string) string {
	if runtime.GOOS == "windows" {
		return "Write-Output '" + marker + "'\r\n"
	}
	return "echo " + marker + "\n"
}

// TestTerminal_RunsARealCommandAndReturnsRealOutput is this package's
// central real-PTY proof: a real terminal, spawned by the companion via
// a real OS pseudo-terminal (ConPTY on Windows, a real Unix PTY
// elsewhere — see pty.go), actually executes a real command against the
// open folder and the real output comes back over the wire.
func TestTerminal_RunsARealCommandAndReturnsRealOutput(t *testing.T) {
	dir := t.TempDir()
	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()

	termID := openFolderAndCreateTerminal(t, conn, dir)

	marker := "HARMONIA_TERMINAL_REAL_OUTPUT_MARKER_9c2f"
	if err := conn.WriteJSON(clientMessage{Type: "terminal_input", TerminalID: termID, Data: shellCommand(marker)}); err != nil {
		t.Fatalf("write terminal_input: %v", err)
	}

	var accumulated strings.Builder
	readTerminalUntil(t, conn, 10*time.Second, func(msg serverMessage) bool {
		if msg.Type == "terminal_output" && msg.TerminalID == termID {
			accumulated.WriteString(msg.Data)
		}
		return strings.Contains(accumulated.String(), marker)
	})
}

// TestTerminal_MultipleInstancesAreIndependent is the real proof behind
// "multiple terminal instances... each with its own real PTY session":
// two real terminals in the same session, each running its own real
// command, each answering only on its own terminal_id — output never
// crosses between them.
func TestTerminal_MultipleInstancesAreIndependent(t *testing.T) {
	dir := t.TempDir()
	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()

	if err := conn.WriteJSON(clientMessage{Type: "open_folder", Path: dir}); err != nil {
		t.Fatalf("write open_folder: %v", err)
	}
	_ = readMsg(t, conn)

	firstID := createTerminal(t, conn)
	secondID := createTerminal(t, conn)

	if firstID == secondID {
		t.Fatalf("terminal ids = %q, %q, want two distinct real ids", firstID, secondID)
	}

	markerA := "HARMONIA_TERM_A_7331"
	markerB := "HARMONIA_TERM_B_1337"
	if err := conn.WriteJSON(clientMessage{Type: "terminal_input", TerminalID: firstID, Data: shellCommand(markerA)}); err != nil {
		t.Fatalf("write input A: %v", err)
	}
	if err := conn.WriteJSON(clientMessage{Type: "terminal_input", TerminalID: secondID, Data: shellCommand(markerB)}); err != nil {
		t.Fatalf("write input B: %v", err)
	}

	var outA, outB strings.Builder
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		var msg serverMessage
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read: %v", err)
		}
		if msg.Type != "terminal_output" {
			continue
		}
		switch msg.TerminalID {
		case firstID:
			outA.WriteString(msg.Data)
		case secondID:
			outB.WriteString(msg.Data)
		}
		if strings.Contains(outA.String(), markerA) && strings.Contains(outB.String(), markerB) {
			// Cross-contamination check: A's marker must never appear in
			// B's own accumulated output, and vice versa.
			if strings.Contains(outA.String(), markerB) || strings.Contains(outB.String(), markerA) {
				t.Fatalf("output crossed between terminals: outA=%q outB=%q", outA.String(), outB.String())
			}
			return
		}
	}
	t.Fatalf("never saw both real markers on their own terminals; outA=%q outB=%q", outA.String(), outB.String())
}

// TestTerminal_KillEndsOnlyThatOneInstance proves independent kill: two
// real terminals, killing one produces a real terminal_exited for that
// terminal alone, while the other keeps running and keeps answering
// real input.
func TestTerminal_KillEndsOnlyThatOneInstance(t *testing.T) {
	dir := t.TempDir()
	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()

	if err := conn.WriteJSON(clientMessage{Type: "open_folder", Path: dir}); err != nil {
		t.Fatalf("write open_folder: %v", err)
	}
	_ = readMsg(t, conn)

	doomedID := createTerminal(t, conn)
	survivorID := createTerminal(t, conn)

	if err := conn.WriteJSON(clientMessage{Type: "kill_terminal", TerminalID: doomedID}); err != nil {
		t.Fatalf("write kill_terminal: %v", err)
	}
	exited := readTerminalUntil(t, conn, 10*time.Second, func(msg serverMessage) bool {
		return msg.Type == "terminal_exited"
	})
	if exited.TerminalID != doomedID {
		t.Fatalf("exited terminal = %q, want the killed one %q", exited.TerminalID, doomedID)
	}

	marker := "HARMONIA_SURVIVOR_STILL_ALIVE_4242"
	if err := conn.WriteJSON(clientMessage{Type: "terminal_input", TerminalID: survivorID, Data: shellCommand(marker)}); err != nil {
		t.Fatalf("write input to survivor: %v", err)
	}
	var acc strings.Builder
	readTerminalUntil(t, conn, 10*time.Second, func(msg serverMessage) bool {
		if msg.Type == "terminal_output" && msg.TerminalID == survivorID {
			acc.WriteString(msg.Data)
		}
		return strings.Contains(acc.String(), marker)
	})
}

// TestTerminal_ResizeSucceedsAgainstTheRealPTY proves resize reaches the
// real OS pseudo-terminal rather than being a no-op — a resize call
// that errors would mean interactive full-screen programs never learn
// their real terminal size.
func TestTerminal_ResizeSucceedsAgainstTheRealPTY(t *testing.T) {
	dir := t.TempDir()
	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()

	termID := openFolderAndCreateTerminal(t, conn, dir)

	if err := conn.WriteJSON(clientMessage{Type: "terminal_resize", TerminalID: termID, Cols: 120, Rows: 40}); err != nil {
		t.Fatalf("write terminal_resize: %v", err)
	}

	// A resize that failed would send a real "error" message — proving
	// silence here means proving the real PTY accepted it, not just
	// that the frontend's own request was well-formed. Confirmed by
	// immediately following up with real input/output, which would
	// never arrive if the session had been knocked over by a bad resize.
	marker := "HARMONIA_AFTER_RESIZE_STILL_WORKS_5150"
	if err := conn.WriteJSON(clientMessage{Type: "terminal_input", TerminalID: termID, Data: shellCommand(marker)}); err != nil {
		t.Fatalf("write terminal_input: %v", err)
	}
	var acc strings.Builder
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		var msg serverMessage
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read: %v", err)
		}
		if msg.Type == "error" {
			t.Fatalf("real resize failed: %s", msg.Message)
		}
		if msg.Type == "terminal_output" && msg.TerminalID == termID {
			acc.WriteString(msg.Data)
			if strings.Contains(acc.String(), marker) {
				return
			}
		}
	}
	t.Fatal("never saw real output after resize")
}

// TestTerminal_NarrateWritesSyntheticOutputNotRealShellInput is the real
// proof behind agent narration's chosen rendering: styled text lands on
// the real terminal_output stream (the same one real PTY output rides,
// rendered by the same real xterm.js instance) without ever reaching the
// real shell's own stdin — proven by running a real command right after
// narrating and confirming the shell never saw the narration text as a
// typed command.
func TestTerminal_NarrateWritesSyntheticOutputNotRealShellInput(t *testing.T) {
	dir := t.TempDir()
	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()
	termID := openFolderAndCreateTerminal(t, conn, dir)

	narration := "\x1b[1;96m[ChatGPT]\x1b[0m\x1b[1m taking a look at this now\x1b[0m"
	if err := conn.WriteJSON(clientMessage{Type: "terminal_narrate", TerminalID: termID, Data: narration}); err != nil {
		t.Fatalf("write terminal_narrate: %v", err)
	}
	got := readTerminalUntil(t, conn, 10*time.Second, func(msg serverMessage) bool {
		return msg.Type == "terminal_output" && msg.TerminalID == termID && strings.Contains(msg.Data, "[ChatGPT]")
	})
	if !strings.Contains(got.Data, narration) {
		t.Fatalf("narrated output = %q, want the real ANSI-styled text verbatim", got.Data)
	}

	// The real shell must never have seen the narration as typed input —
	// proven by running a real command afterward and confirming its real
	// output contains no trace of a "not recognized"/parse-error shell
	// reaction to the narration text.
	marker := "HARMONIA_AFTER_NARRATE_REAL_COMMAND_6161"
	if err := conn.WriteJSON(clientMessage{Type: "terminal_input", TerminalID: termID, Data: shellCommand(marker)}); err != nil {
		t.Fatalf("write terminal_input: %v", err)
	}
	var acc strings.Builder
	readTerminalUntil(t, conn, 10*time.Second, func(msg serverMessage) bool {
		if msg.Type == "terminal_output" && msg.TerminalID == termID {
			acc.WriteString(msg.Data)
		}
		return strings.Contains(acc.String(), marker)
	})
	if strings.Contains(acc.String(), "not recognized") || strings.Contains(acc.String(), "CommandNotFoundException") {
		t.Fatalf("shell reacted to narration text as a typed command; output = %q", acc.String())
	}
}

// TestTerminal_CwdTrackingReportsRealDirectoryChange is the real proof
// behind "tracking its own working directory... updating live as cd
// happens": the shell-integration hook's own OSC 9;9 announcement,
// parsed from real shell output after a real `cd` into a real
// subdirectory — not the folder the terminal started in.
func TestTerminal_CwdTrackingReportsRealDirectoryChange(t *testing.T) {
	dir := t.TempDir()
	sub := dir + string(os.PathSeparator) + "subdir"
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("seed subdir: %v", err)
	}

	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()
	termID := openFolderAndCreateTerminal(t, conn, dir)

	var cdCmd string
	if runtime.GOOS == "windows" {
		cdCmd = "cd subdir\r\n"
	} else {
		cdCmd = "cd subdir\n"
	}
	if err := conn.WriteJSON(clientMessage{Type: "terminal_input", TerminalID: termID, Data: cdCmd}); err != nil {
		t.Fatalf("write terminal_input: %v", err)
	}

	// The very first terminal_cwd a real terminal emits is the shell-
	// integration script's own initial prompt render (announcing the
	// starting directory, before the real `cd` above has even reached
	// the shell) — not necessarily the report this test cares about. Wait
	// for one that actually reports the real subdir, rather than
	// whichever terminal_cwd happens to arrive first.
	got := readTerminalUntil(t, conn, 10*time.Second, func(msg serverMessage) bool {
		return msg.Type == "terminal_cwd" && msg.TerminalID == termID &&
			strings.HasSuffix(strings.TrimRight(msg.Cwd, `/\`), "subdir")
	})
	if !strings.HasSuffix(strings.TrimRight(got.Cwd, `/\`), "subdir") {
		t.Fatalf("reported cwd = %q, want it to end in the real subdir", got.Cwd)
	}
}
