package companion

import (
	"bufio"
	"io"
	"os/exec"
	"runtime"
)

// startShell spawns one real shell process, cwd'd at the open folder,
// and streams its combined stdout+stderr back as shell_output messages
// as they arrive. Deliberately plain stdin/stdout pipes, not a real
// PTY — the honest, provable first slice: real commands genuinely
// execute and their real output genuinely comes back, just without full
// TTY semantics (no ANSI cursor control, no live terminal resize, some
// interactive-prompt CLIs won't render as they would in a real
// terminal). A real PTY (ConPTY on Windows, since this session's own
// dev machine is Windows — github.com/creack/pty doesn't support it) is
// the natural next real upgrade once this baseline round-trip is
// proven, not a compromise pretending to be the finished thing.
func (s *session) startShell() {
	s.mu.Lock()
	root := s.rootDir
	s.mu.Unlock()
	if root == "" {
		s.sendError("start shell: no folder is open")
		return
	}

	s.shellMu.Lock()
	defer s.shellMu.Unlock()
	if s.shellCmd != nil {
		s.sendError("start shell: a shell is already running")
		return
	}

	cmd := exec.Command(shellExecutable(), shellExecutableArgs()...)
	cmd.Dir = root
	stdin, err := cmd.StdinPipe()
	if err != nil {
		s.sendError("start shell: %v", err)
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		s.sendError("start shell: %v", err)
		return
	}
	cmd.Stderr = cmd.Stdout // combined stream, same as a real terminal shows both interleaved
	if err := cmd.Start(); err != nil {
		s.sendError("start shell: %v", err)
		return
	}

	s.shellCmd = cmd
	s.shellIn = stdin

	go s.pumpShellOutput(stdout)
	go s.waitShell(cmd)

	s.send(serverMessage{Type: "shell_started"})
}

func (s *session) pumpShellOutput(r io.Reader) {
	buf := make([]byte, 4096)
	reader := bufio.NewReader(r)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			s.send(serverMessage{Type: "shell_output", Data: string(buf[:n])})
		}
		if err != nil {
			return
		}
	}
}

func (s *session) waitShell(cmd *exec.Cmd) {
	err := cmd.Wait()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			code = -1
		}
	}
	s.shellMu.Lock()
	s.shellCmd = nil
	s.shellIn = nil
	s.shellMu.Unlock()
	s.send(serverMessage{Type: "shell_exited", ExitCode: &code})
}

func (s *session) writeShellInput(data string) {
	s.shellMu.Lock()
	in := s.shellIn
	s.shellMu.Unlock()
	if in == nil {
		s.sendError("shell input: no shell is running")
		return
	}
	if _, err := io.WriteString(in, data); err != nil {
		s.sendError("shell input: %v", err)
	}
}

func (s *session) stopShell() {
	s.shellMu.Lock()
	cmd := s.shellCmd
	s.shellMu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func shellExecutable() string {
	if runtime.GOOS == "windows" {
		return "powershell.exe"
	}
	return "/bin/sh"
}

func shellExecutableArgs() []string {
	if runtime.GOOS == "windows" {
		// -NoLogo/-NoExit: a real, persistent interactive session, not
		// one-shot command execution — this process stays alive for the
		// whole terminal session, reading further commands from stdin
		// exactly like a human typing into a real PowerShell window.
		return []string{"-NoLogo", "-NoExit", "-Command", "-"}
	}
	return []string{"-i"}
}
