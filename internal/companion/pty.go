package companion

// PTY is a real pseudo-terminal — Windows ConPTY (pty_windows.go, via
// github.com/UserExistsError/conpty) or a real Unix PTY (pty_unix.go,
// via github.com/creack/pty) behind one common interface, so the rest
// of this package manages terminal sessions without caring which
// platform it's actually running on. Both are real OS-level pseudo-
// terminals, not plain pipes — the thing plain stdin/stdout pipes
// (this package's own earlier implementation) can never give a real
// full-screen program: real resize signals and real terminal-mode I/O,
// so vim, htop, and less render correctly instead of corrupting.
type PTY interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	// Resize delivers a real resize signal to the running process, the
	// same as a human dragging a real terminal window's edge.
	Resize(cols, rows int) error
	// Close terminates the underlying process and releases the PTY.
	Close() error
	// Wait blocks until the process exits and returns its real exit
	// code.
	Wait() (exitCode int, err error)
}

// startPTY spawns a real shell inside a real PTY, cwd'd at dir, sized to
// cols x rows. Implemented per-platform — pty_windows.go (ConPTY) and
// pty_unix.go (creack/pty) — each behind its own build tag, so exactly
// one real definition compiles for any given target.
