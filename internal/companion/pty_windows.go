//go:build windows

package companion

import (
	"fmt"

	"github.com/UserExistsError/conpty"
	"golang.org/x/sys/windows"
)

// windowsPTY wraps a real Windows ConPTY session (github.com/
// UserExistsError/conpty, MIT licensed) — the real pseudo-console API
// Windows Terminal and VS Code's own integrated terminal are built on,
// not a hand-rolled substitute.
//
// processHandle is our own independent real handle to the child process,
// opened via OpenProcess right after Start — deliberately separate from
// whatever handle conpty.ConPty holds internally. conpty.Close() closes
// its own internal process handle as part of tearing down the pseudo
// console; if Wait() used that same handle, a Wait() call already
// blocked in WaitForSingleObject when Close() runs on another goroutine
// (exactly this package's own kill-terminal path: killTerminal calls
// Close() while waitTerminal's own goroutine is already waiting) races
// a real Windows hazard — CloseHandle on a handle another thread is
// blocked on invalidates it out from under that wait, so
// WaitForSingleObject returns almost immediately with "the handle is
// invalid" and a meaningless STILL_ACTIVE code, not a real confirmation
// the process actually exited. Found live: this false-positive "it
// exited" signal was firing within ~1s of Close() regardless of how
// long the real child process actually took to terminate, so every
// terminal's real teardown (session close, kill_terminal) returned long
// before its real powershell.exe process was gone — a real, silent
// process leak (confirmed live via Get-Process: dozens of orphaned
// powershell.exe processes accumulated over one session). Waiting on
// our own separate handle sidesteps the race entirely: Close() never
// touches it, so Wait() always blocks for the process's real exit.
type windowsPTY struct {
	cpty          *conpty.ConPty
	processHandle windows.Handle
}

func startPTY(dir string, cols, rows int) (PTY, error) {
	cpty, err := conpty.Start(
		shellCommandLine(),
		conpty.ConPtyDimensions(cols, rows),
		conpty.ConPtyWorkDir(dir),
	)
	if err != nil {
		return nil, err
	}
	h, err := windows.OpenProcess(
		windows.PROCESS_QUERY_INFORMATION|windows.SYNCHRONIZE|windows.PROCESS_TERMINATE,
		false, uint32(cpty.Pid()),
	)
	if err != nil {
		_ = cpty.Close()
		return nil, fmt.Errorf("open real process handle: %w", err)
	}
	return &windowsPTY{cpty: cpty, processHandle: h}, nil
}

func (w *windowsPTY) Read(p []byte) (int, error)  { return w.cpty.Read(p) }
func (w *windowsPTY) Write(p []byte) (int, error) { return w.cpty.Write(p) }
func (w *windowsPTY) Resize(cols, rows int) error { return w.cpty.Resize(cols, rows) }

// Close tears down the pseudo console (freeing its real pipes/conhost)
// and, on our own independent handle, directly force-terminates the
// real child process — real "kill", not a negotiated/graceful shutdown
// this package waits on ClosePseudoConsole to eventually get around to.
// Found live: under real contention on a loaded machine, relying on
// ClosePseudoConsole's own asynchronous teardown alone let real
// termination lag tens of seconds behind Close() returning; a direct
// TerminateProcess on our own handle makes "kill" actually mean kill,
// independent of however long the pseudo console's own internal
// teardown happens to take under load.
func (w *windowsPTY) Close() error {
	closeErr := w.cpty.Close()
	// ERROR_ACCESS_DENIED here just means the process had already
	// exited on its own (e.g. the shell was closed with `exit`) — real,
	// expected, not a failure to report.
	if err := windows.TerminateProcess(w.processHandle, 1); err != nil && err != windows.ERROR_ACCESS_DENIED {
		if closeErr == nil {
			closeErr = fmt.Errorf("terminate real process: %w", err)
		}
	}
	return closeErr
}

// Wait blocks on this PTY's own independent process handle (see the
// windowsPTY doc comment for why it's not the library's internal one)
// until the real child process actually exits, then reads its real exit
// code — a genuine confirmation, not a guess from a handle invalidated
// by a concurrent Close().
//
// Genuinely infinite, not a bounded poll: waitTerminal calls this once,
// immediately on creation, and it isn't supposed to return until the
// real process actually exits — which, for a terminal nobody has killed
// yet, is correctly "not for a long time," possibly hours. A bounded
// wait here was tried and found live to be a serious regression: any
// terminal a human hadn't touched in the bound's own window (30s) got
// silently, falsely reported as terminal_exited and removed, while its
// real shell was still alive and well underneath — WaitForSingleObject
// legitimately returns WAIT_TIMEOUT for a healthy, still-running
// process once its deadline passes, and there is no way to tell that
// apart from "stuck mid-kill" from inside Wait() alone. Close()'s own
// real TerminateProcess call (see above) is what makes an infinite wait
// here safe: kill_terminal now makes the real process die within
// milliseconds, so this never actually blocks the whole session's
// teardown on a process that isn't going to exit on its own.
func (w *windowsPTY) Wait() (int, error) {
	defer func() { _ = windows.CloseHandle(w.processHandle) }()
	if _, err := windows.WaitForSingleObject(w.processHandle, windows.INFINITE); err != nil {
		return 0, fmt.Errorf("wait for real process exit: %w", err)
	}
	var exitCode uint32
	if err := windows.GetExitCodeProcess(w.processHandle, &exitCode); err != nil {
		return 0, fmt.Errorf("get real process exit code: %w", err)
	}
	return int(exitCode), nil
}

// shellCommandLine is a real Win32 command-line string (CreateProcess's
// own convention, not an argv slice) — -NoExit keeps this a real,
// persistent interactive session for the PTY's whole lifetime, exactly
// like a human's own PowerShell window.
//
// Deliberately no `-Command -`: that mode tells PowerShell to read a
// script from a *redirected* stdin pipe, which is exactly what this
// process's stdin is not — a real ConPTY gives the child process a real
// console, the same as any other real terminal (Windows Terminal, VS
// Code's own integrated terminal). PowerShell genuinely refuses to
// start with `-Command -` against a real console ("standard input has
// not been redirected for this process") — found live, the whole
// reason real terminal output never arrived before this. Running with
// no -Command at all puts PowerShell in its own real interactive mode,
// reading real console input the same way a human's own keystrokes
// would arrive — the correct, and more capable, way to drive a real PTY
// (real line editing, history, tab completion all now genuinely work,
// none of which the old plain-pipe implementation could ever support).
func shellCommandLine() string {
	return "powershell.exe -NoLogo -NoExit"
}

// shellIntegrationScript is sent as the PTY's first real input, right
// after it starts — a real shell-integration hook (the exact OSC 9;9
// "current working directory" convention iTerm2 and VS Code's own
// terminal already use), wrapping PowerShell's own prompt function
// rather than replacing it, so the visible prompt is unchanged and the
// real cwd is announced after every command. This is what lets a
// terminal's own tab/selector label track `cd` live instead of only
// showing the folder it started in.
func shellIntegrationScript() string {
	return "$global:__harmoniaPrompt = $function:prompt; " +
		"function prompt { " +
		"[Console]::Out.Write(\"$([char]27)]9;9;$((Get-Location).Path)$([char]7)\"); " +
		"& $global:__harmoniaPrompt " +
		"}"
}
