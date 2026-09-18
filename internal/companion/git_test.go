package companion

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// requireGit skips the test outright when a real git binary isn't on
// PATH — the exact same "skip cleanly, don't fail" posture this codebase
// already uses for HARMONIA_DATABASE_URL-gated integration tests when a
// real external dependency isn't available in the environment running
// them.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH; skipping")
	}
}

// initRealGitRepo creates a genuine git repository in dir — real `git
// init`, with a repo-local identity so `git commit` works regardless of
// this machine's own global git config.
func initRealGitRepo(t *testing.T, dir string) {
	t.Helper()
	requireGit(t)
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// TestGitStatus_NotARepo_ReportsRealGitRepoFalse proves the one real
// distinction git_status makes between "not a real repository" (the
// overwhelmingly common case for a plain folder) and a genuine error —
// the former is a real, honest false, never an error message a human
// would read as something broken.
func TestGitStatus_NotARepo_ReportsRealGitRepoFalse(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()
	if err := conn.WriteJSON(clientMessage{Type: "open_folder", Path: dir}); err != nil {
		t.Fatalf("write open_folder: %v", err)
	}
	_ = readMsg(t, conn)

	if err := conn.WriteJSON(clientMessage{Type: "git_status"}); err != nil {
		t.Fatalf("write git_status: %v", err)
	}
	resp := readMsg(t, conn)
	if resp.Type != "git_status" || resp.GitRepo {
		t.Fatalf("resp = %+v, want git_status with GitRepo=false", resp)
	}
}

// TestGitStatus_StageUnstageCommit_AllTakeRealEffect is Source Control's
// own end-to-end proof: a real git repo, a real untracked file, staged,
// unstaged, staged again, and committed for real — verified independently
// against the real repository's own log and status, not just the
// companion's own claims.
func TestGitStatus_StageUnstageCommit_AllTakeRealEffect(t *testing.T) {
	dir := t.TempDir()
	initRealGitRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()
	if err := conn.WriteJSON(clientMessage{Type: "open_folder", Path: dir}); err != nil {
		t.Fatalf("write open_folder: %v", err)
	}
	_ = readMsg(t, conn)

	// Real status: one real untracked file.
	if err := conn.WriteJSON(clientMessage{Type: "git_status"}); err != nil {
		t.Fatalf("write git_status: %v", err)
	}
	status := readMsg(t, conn)
	if !status.GitRepo || len(status.GitFiles) != 1 || status.GitFiles[0].Path != "main.go" || status.GitFiles[0].Worktree != "?" {
		t.Fatalf("status = %+v, want one untracked main.go", status)
	}

	// Real stage.
	if err := conn.WriteJSON(clientMessage{Type: "git_stage", Path: "main.go"}); err != nil {
		t.Fatalf("write git_stage: %v", err)
	}
	staged := readMsg(t, conn)
	if len(staged.GitFiles) != 1 || staged.GitFiles[0].Staged != "A" {
		t.Fatalf("staged = %+v, want main.go staged as A", staged)
	}
	// Independent proof staging really happened, not just that the
	// companion's own status claims it did.
	diffCmd := exec.Command("git", "diff", "--cached", "--name-only")
	diffCmd.Dir = dir
	realStagedNames, err := diffCmd.Output()
	if err != nil {
		t.Fatalf("verify real index: %v", err)
	}
	if got := string(realStagedNames); got != "main.go\n" {
		t.Fatalf("real staged files = %q, want %q", got, "main.go\n")
	}

	// Real unstage.
	if err := conn.WriteJSON(clientMessage{Type: "git_unstage", Path: "main.go"}); err != nil {
		t.Fatalf("write git_unstage: %v", err)
	}
	unstaged := readMsg(t, conn)
	if len(unstaged.GitFiles) != 1 || unstaged.GitFiles[0].Staged != "?" || unstaged.GitFiles[0].Worktree != "?" {
		t.Fatalf("unstaged = %+v, want main.go back to untracked", unstaged)
	}

	// Stage again, then real commit.
	if err := conn.WriteJSON(clientMessage{Type: "git_stage", Path: "main.go"}); err != nil {
		t.Fatalf("write git_stage: %v", err)
	}
	_ = readMsg(t, conn)
	if err := conn.WriteJSON(clientMessage{Type: "git_commit", Data: "real first commit"}); err != nil {
		t.Fatalf("write git_commit: %v", err)
	}
	afterCommit := readMsg(t, conn)
	if afterCommit.Type != "git_status" || len(afterCommit.GitFiles) != 0 {
		t.Fatalf("status after commit = %+v, want a clean tree", afterCommit)
	}

	// Independent proof: a real commit really exists in this real repo's
	// own log, not just implied by an empty status.
	logCmd := exec.Command("git", "log", "--oneline", "-1", "--format=%s")
	logCmd.Dir = dir
	out, err := logCmd.Output()
	if err != nil {
		t.Fatalf("read real git log: %v", err)
	}
	if got := string(out); got != "real first commit\n" {
		t.Fatalf("real git log message = %q, want %q", got, "real first commit\n")
	}
}

// TestGitCommit_RefusesAnEmptyMessage proves the one real client-side
// guard git_commit applies before ever shelling out.
func TestGitCommit_RefusesAnEmptyMessage(t *testing.T) {
	dir := t.TempDir()
	initRealGitRepo(t, dir)
	conn, cleanup := dialTestServer(t, NewAllowedOrigins("http://localhost:3000"), "http://localhost:3000")
	defer cleanup()
	if err := conn.WriteJSON(clientMessage{Type: "open_folder", Path: dir}); err != nil {
		t.Fatalf("write open_folder: %v", err)
	}
	_ = readMsg(t, conn)

	if err := conn.WriteJSON(clientMessage{Type: "git_commit", Data: "   "}); err != nil {
		t.Fatalf("write git_commit: %v", err)
	}
	resp := readMsg(t, conn)
	if resp.Type != "error" {
		t.Fatalf("response = %+v, want a real error for an empty message", resp)
	}
}
