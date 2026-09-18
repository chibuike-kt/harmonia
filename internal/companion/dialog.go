package companion

import (
	"os/exec"
	"runtime"
	"strings"
)

// folderPicker opens a real native OS folder-selection dialog and
// returns the chosen absolute path, or ("", nil) if the human canceled
// it. A package-level var, not a plain function call, so tests can
// substitute a fake without actually popping a native dialog during
// `go test` — the same injection-point shape this codebase already uses
// wherever a real OS-level side effect needs to stay testable.
var folderPicker func() (string, error) = defaultFolderPicker

// pickFolder drives the "Open Folder" flow the IDE's design overhaul
// requires: no more browser-side raw-path typing — the companion itself,
// a real local process, is what's actually positioned to show a real
// native folder picker. Reuses openFolder's own validation once a path
// comes back, so a canceled dialog and a validation failure both produce
// the same honest serverMessage shapes callers already handle.
func (s *session) pickFolder() {
	path, err := folderPicker()
	if err != nil {
		s.sendError("pick folder: %v", err)
		return
	}
	if path == "" {
		// A human closing or canceling the dialog is a normal, silent
		// outcome — not an error. The frontend stays on whatever it was
		// showing before (the empty state, most likely).
		return
	}
	s.openFolder(path)
}

// defaultFolderPicker is the real, OS-native implementation. Windows
// only for now — this session's own dev machine, same honest scoping
// shell.go's own shellExecutable already applies (ConPTY there, this
// here: the real thing on the one platform actually exercised, not a
// speculative cross-platform abstraction nothing has run).
func defaultFolderPicker() (string, error) {
	switch runtime.GOOS {
	case "windows":
		return windowsFolderPicker()
	default:
		return "", errUnsupportedPlatform
	}
}

var errUnsupportedPlatform = folderPickerUnsupportedError{}

type folderPickerUnsupportedError struct{}

func (folderPickerUnsupportedError) Error() string {
	return "native folder picker isn't implemented for " + runtime.GOOS + " yet"
}

// windowsFolderPicker shells out to a real folder-selection dialog via
// PowerShell — the same real-process pattern shell.go already uses for
// the terminal itself, not a new mechanism. Blocks until the human
// closes the dialog, which is the correct behavior for a modal folder
// picker; the WebSocket read loop this runs on is per-session and
// per-connection, so blocking it here just means this one browser tab's
// other requests wait for the same real dialog a human is already
// looking at.
//
// Deliberately System.Windows.Forms.OpenFileDialog, not
// FolderBrowserDialog: FolderBrowserDialog is .NET Framework's original
// SHBrowseForFolder dialog — a small tree view with none of a real File
// Explorer window's UI (no address bar, no search, no Quick Access, no
// right-click "New folder"). Found live: a human expecting "Open Folder"
// to actually open something that looks like File Explorer, getting this
// instead. OpenFileDialog, on the other hand, really is the modern
// Explorer-style common item dialog — the same window Explorer itself
// uses — just meant for picking a file; setting CheckFileExists false
// and priming FileName with a placeholder is the standard, well-known
// way to use that same real dialog to pick a folder instead: the human
// navigates to the folder they want using the real Explorer UI and hits
// Open without needing to type or select an actual file, and this reads
// the folder back via the chosen path's own directory.
func windowsFolderPicker() (string, error) {
	script := `Add-Type -AssemblyName System.Windows.Forms
$f = New-Object System.Windows.Forms.OpenFileDialog
$f.Title = "Open a folder in Harmonia"
$f.CheckFileExists = $false
$f.CheckPathExists = $true
$f.ValidateNames = $false
$f.AddExtension = $false
$f.FileName = "Select this folder"
if ($f.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) {
  [Console]::Out.Write([System.IO.Path]::GetDirectoryName($f.FileName))
}`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
