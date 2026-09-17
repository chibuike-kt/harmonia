//go:build !windows

package companion

import (
	"os"
	"os/exec"

	creackpty "github.com/creack/pty"
)

// unixPTY wraps a real Unix PTY (github.com/creack/pty, MIT licensed) —
// this package's cross-platform build/CI path (this project's own dev
// and live-test environment is Windows; this side is real, not a stub,
// but hasn't had the same live verification the Windows path has).
type unixPTY struct {
	f   *os.File
	cmd *exec.Cmd
}

func startPTY(dir string, cols, rows int) (PTY, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell)
	cmd.Dir = dir
	f, err := creackpty.StartWithSize(cmd, &creackpty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
	if err != nil {
		return nil, err
	}
	return &unixPTY{f: f, cmd: cmd}, nil
}

func (u *unixPTY) Read(p []byte) (int, error)  { return u.f.Read(p) }
func (u *unixPTY) Write(p []byte) (int, error) { return u.f.Write(p) }

func (u *unixPTY) Resize(cols, rows int) error {
	return creackpty.Setsize(u.f, &creackpty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (u *unixPTY) Close() error {
	_ = u.f.Close()
	if u.cmd.Process != nil {
		_ = u.cmd.Process.Kill()
	}
	return nil
}

func (u *unixPTY) Wait() (int, error) {
	err := u.cmd.Wait()
	if err == nil {
		return 0, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), nil
	}
	return -1, err
}

// shellIntegrationScript emits the same real OSC 9;9 "current working
// directory" convention as the Windows path — see pty_windows.go's own
// doc comment — via a bash/zsh PROMPT_COMMAND hook instead of wrapping
// PowerShell's prompt function.
func shellIntegrationScript() string {
	return `__harmonia_prompt_cmd() { printf '\033]9;9;%s\007' "$PWD"; }
if [ -n "$ZSH_VERSION" ]; then
  precmd_functions+=(__harmonia_prompt_cmd)
else
  PROMPT_COMMAND="__harmonia_prompt_cmd${PROMPT_COMMAND:+;$PROMPT_COMMAND}"
fi`
}
