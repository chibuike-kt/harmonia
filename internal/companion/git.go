package companion

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
)

// GitFileStatus is one real entry from `git status --porcelain=v1` —
// Staged is the index column (what a real `git commit` would include
// right now), Worktree is the working-tree column (what's changed on
// disk since the index) — the exact same two-column status every real
// git porcelain tool (git status, most GUI clients) surfaces, not a
// simplified single "changed" bucket.
type GitFileStatus struct {
	Path     string `json:"path"`
	Staged   string `json:"staged"`
	Worktree string `json:"worktree"`
}

// runGit shells out to a real, locally-installed git — the same real
// binary a human's own terminal in this folder would use, with the exact
// same config, hooks, and credentials, rather than a reimplementation
// that could drift from real git's own behavior. cmd.Dir, not a `-C`
// flag, is what scopes every call to the open folder; git itself walks
// up from there to find the real repository root, exactly as it would
// from a human typing the same command in that directory.
func runGit(root string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return stdout.String(), nil
}

func (s *session) gitRoot() (string, error) {
	s.mu.Lock()
	root := s.rootDir
	s.mu.Unlock()
	if root == "" {
		return "", errors.New("no folder is open")
	}
	return root, nil
}

// gitStatus reports this open folder's real git state — branch plus
// every real changed/staged/untracked file, source control's own only
// real caller. Not being a real git repository at all (or git not being
// installed) is reported as a real, honest "no repo here" rather than an
// error a human would read as something broken — the overwhelmingly
// common case for a folder that just isn't one.
func (s *session) gitStatus() {
	root, err := s.gitRoot()
	if err != nil {
		s.sendError("git status: %v", err)
		return
	}
	// --is-inside-work-tree, not --abbrev-ref HEAD: the latter fails on a
	// real, freshly `git init`ed repo with no commits yet (HEAD has
	// nothing to resolve to), which would misreport a genuine, real repo
	// as "not a repo at all." This succeeds the instant a real .git
	// directory exists, commits or not.
	if _, err := runGit(root, "rev-parse", "--is-inside-work-tree"); err != nil {
		s.send(serverMessage{Type: "git_status", GitRepo: false})
		return
	}
	// symbolic-ref, not rev-parse --abbrev-ref: this reads HEAD's own
	// symbolic branch name directly and works the same whether or not
	// that branch has a real commit yet — "" (silently, best-effort) only
	// for the rare genuine detached-HEAD state.
	branchOut, _ := runGit(root, "symbolic-ref", "--short", "HEAD")

	out, err := runGit(root, "status", "--porcelain=v1")
	if err != nil {
		s.sendError("git status: %v", err)
		return
	}
	var files []GitFileStatus
	for line := range strings.SplitSeq(out, "\n") {
		if len(line) < 4 {
			continue
		}
		staged, worktree := string(line[0]), string(line[1])
		path := line[3:]
		// A rename/copy entry reads "old -> new" — the path a human
		// would actually click to open or stage is the new one.
		if idx := strings.Index(path, " -> "); idx >= 0 {
			path = path[idx+4:]
		}
		files = append(files, GitFileStatus{Path: path, Staged: staged, Worktree: worktree})
	}
	s.send(serverMessage{
		Type:      "git_status",
		GitRepo:   true,
		GitBranch: strings.TrimSpace(branchOut),
		GitFiles:  files,
	})
}

// gitStage runs a real `git add` on one real path — Source Control's own
// real Stage action. Re-reports real status afterward rather than a bare
// ack, so the panel's list moves the file from Changes to Staged from a
// single real round trip.
func (s *session) gitStage(rel string) {
	root, err := s.gitRoot()
	if err != nil {
		s.sendError("git add: %v", err)
		return
	}
	if _, err := runGit(root, "add", "--", rel); err != nil {
		s.sendError("git add: %v", err)
		return
	}
	s.gitStatus()
}

// gitUnstage runs a real `git reset HEAD -- <path>` — the real inverse of
// Stage, moving a file back from the index to the working tree without
// touching its actual content.
func (s *session) gitUnstage(rel string) {
	root, err := s.gitRoot()
	if err != nil {
		s.sendError("git reset: %v", err)
		return
	}
	if _, err := runGit(root, "reset", "HEAD", "--", rel); err != nil {
		s.sendError("git reset: %v", err)
		return
	}
	s.gitStatus()
}

// gitCommit runs a real `git commit` against whatever is really staged
// right now — Source Control's own real Commit action. A human's real
// commit message, a real commit object, using this machine's own real
// git identity and hooks; nothing here stages anything itself.
func (s *session) gitCommit(message string) {
	root, err := s.gitRoot()
	if err != nil {
		s.sendError("git commit: %v", err)
		return
	}
	message = strings.TrimSpace(message)
	if message == "" {
		s.sendError("git commit: a commit message is required")
		return
	}
	if _, err := runGit(root, "commit", "-m", message); err != nil {
		s.sendError("git commit: %v", err)
		return
	}
	s.gitStatus()
}
