package unattended

import (
	"regexp"
	"strings"
)

// safeRelPackagePath matches a Go package pattern like "./..." or
// "./internal/foo/..." — explicitly relative (must start with "./"),
// and never containing a ".." segment regardless of what the character
// class alone would allow.
var safeRelPackagePath = regexp.MustCompile(`^\./[A-Za-z0-9_\-./]*$`)

func isSafeRelPackagePath(s string) bool {
	if !safeRelPackagePath.MatchString(s) {
		return false
	}
	// A ".." *segment* is traversal and must be rejected; Go's own
	// "..." wildcard suffix (as in "./...") is a completely different,
	// legitimate token that happens to contain ".." as a substring —
	// checking with strings.Contains(s, "..") would wrongly reject
	// "./...", the single most common real package pattern there is.
	// Splitting on "/" and comparing whole segments tells the two apart.
	for seg := range strings.SplitSeq(s, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}

// safeGoModulePath matches a real Go module path for `go get` — real
// registry resolution only (no scheme, so no file:// or a bare URL
// masquerading as a module path), optionally pinned to a version.
var safeGoModulePath = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~/-]*(@[A-Za-z0-9._+-]+)?$`)

func isSafeGoModulePath(s string) bool {
	return safeGoModulePath.MatchString(s) &&
		!strings.Contains(s, "..") &&
		!strings.Contains(s, "://")
}

// safeGoTestFlags are the only go test flags this allowlist accepts —
// none of them can redirect output, run arbitrary code, or reach the
// network; they only narrow or annotate what already-real tests do.
var safeGoTestFlags = map[string]bool{
	"-short":   true,
	"-race":    true,
	"-v":       true,
	"-count=1": true,
}

// safeTestNamePattern is deliberately narrow for -run's argument — a
// real Go test name / regexp built from identifier characters only.
var safeTestNamePattern = regexp.MustCompile(`^[A-Za-z0-9_./|^$*]+$`)

func checkGo(args []string) Decision {
	if len(args) == 0 {
		return deny("go: a subcommand is required")
	}
	switch args[0] {
	case "build":
		return checkGoBuildOrVet("build", args[1:])
	case "vet":
		return checkGoBuildOrVet("vet", args[1:])
	case "test":
		return checkGoTest(args[1:])
	case "mod":
		return checkGoMod(args[1:])
	case "get":
		return checkGoGet(args[1:])
	default:
		return deny("go %s is not on the unattended allowlist — only build, vet, test, mod tidy/download, and get are", args[0])
	}
}

func checkGoBuildOrVet(name string, rest []string) Decision {
	if len(rest) > 1 {
		return deny("go %s: only a single package pattern is allowed", name)
	}
	if len(rest) == 1 && !isSafeRelPackagePath(rest[0]) {
		return deny("go %s: %q is not a safe relative package pattern", name, rest[0])
	}
	return allow()
}

func checkGoTest(rest []string) Decision {
	pkgArgs := 0
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case safeGoTestFlags[a]:
			continue
		case a == "-run":
			if i+1 >= len(rest) {
				return deny("go test: -run requires a pattern")
			}
			i++
			if !safeTestNamePattern.MatchString(rest[i]) {
				return deny("go test: -run pattern %q is not allowed", rest[i])
			}
		case strings.HasPrefix(a, "-"):
			return deny("go test: flag %q is not on the unattended allowlist", a)
		default:
			pkgArgs++
			if pkgArgs > 1 {
				return deny("go test: only a single package pattern is allowed")
			}
			if !isSafeRelPackagePath(a) {
				return deny("go test: %q is not a safe relative package pattern", a)
			}
		}
	}
	return allow()
}

func checkGoMod(rest []string) Decision {
	if len(rest) == 1 && (rest[0] == "tidy" || rest[0] == "download") {
		return allow()
	}
	return deny("go mod: only tidy and download are allowed in unattended mode")
}

func checkGoGet(rest []string) Decision {
	if len(rest) != 1 {
		return deny("go get: exactly one module path is required")
	}
	if !isSafeGoModulePath(rest[0]) {
		return deny("go get: %q is not a safe module path against the default registry", rest[0])
	}
	return allow()
}

// safeNpmScripts is the closed set of package.json script names this
// allowlist will run — real build/test/lint tooling by name, not
// whatever a package.json happens to define (a malicious "postinstall"-
// style script under an unexpected name is exactly what naming a fixed
// set instead of "any script" defends against).
var safeNpmScripts = map[string]bool{
	"build":        true,
	"test":         true,
	"lint":         true,
	"typecheck":    true,
	"format":       true,
	"format:check": true,
}

// safeNpmPackageSpec matches a real npm package name — optionally
// scoped (@scope/name), optionally version-pinned (name@1.2.3) — never
// a git/URL install spec, which npm also accepts and this deliberately
// excludes.
var safeNpmPackageSpec = regexp.MustCompile(`^(@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*(@[A-Za-z0-9^~.\-]+)?$`)

func checkNpm(args []string) Decision {
	if len(args) == 0 {
		return deny("npm: a subcommand is required")
	}
	switch args[0] {
	case "ci":
		if len(args) != 1 {
			return deny("npm ci: no extra arguments are allowed in unattended mode")
		}
		return allow()
	case "install":
		rest := args[1:]
		if len(rest) == 0 {
			return allow()
		}
		if len(rest) != 1 {
			return deny("npm install: only a single package spec is allowed")
		}
		if !safeNpmPackageSpec.MatchString(rest[0]) {
			return deny("npm install: %q is not a safe package spec against the default registry", rest[0])
		}
		return allow()
	case "test":
		if len(args) != 1 {
			return deny("npm test: no extra arguments are allowed")
		}
		return allow()
	case "run":
		if len(args) != 2 {
			return deny("npm run: exactly one script name is required")
		}
		if !safeNpmScripts[args[1]] {
			return deny("npm run %s is not on the unattended allowlist", args[1])
		}
		return allow()
	default:
		return deny("npm %s is not allowed in unattended mode — install, ci, test, and the enumerated run scripts only", args[0])
	}
}

// safePipPackageSpec matches a real PyPI package name, optionally with
// extras and a single version specifier — never a VCS ref (git+...) or
// a URL, which pip also accepts and this deliberately excludes.
var safePipPackageSpec = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(\[[A-Za-z0-9,_-]+\])?((==|>=|<=|~=|!=|>|<)[A-Za-z0-9.\-]+)?$`)

func checkPip(args []string) Decision {
	if len(args) == 0 || args[0] != "install" {
		return deny("pip: only install is allowed in unattended mode")
	}
	rest := args[1:]
	if len(rest) == 2 && rest[0] == "-r" {
		if rest[1] != "requirements.txt" {
			return deny("pip install -r: only requirements.txt is allowed")
		}
		return allow()
	}
	if len(rest) == 1 {
		if !safePipPackageSpec.MatchString(rest[0]) {
			return deny("pip install: %q is not a safe package spec against the default registry", rest[0])
		}
		return allow()
	}
	return deny("pip install: unrecognized arguments")
}

// safeMakeTargets is a small, conventional set of target names real
// Makefiles across many real projects use for exactly this purpose —
// not this project's own Makefile specifically, since an unattended
// session can run against any opened folder.
var safeMakeTargets = map[string]bool{
	"build":   true,
	"test":    true,
	"lint":    true,
	"check":   true,
	"fmt":     true,
	"format":  true,
	"vet":     true,
	"tidy":    true,
	"install": true,
}

func checkMake(args []string) Decision {
	if len(args) != 1 {
		return deny("make: exactly one target is required")
	}
	if !safeMakeTargets[args[0]] {
		return deny("make %s is not on the unattended target allowlist", args[0])
	}
	return allow()
}

func checkGolangciLint(args []string) Decision {
	if len(args) == 1 && args[0] == "run" {
		return allow()
	}
	if len(args) == 2 && args[0] == "run" && args[1] == "./..." {
		return allow()
	}
	return deny("golangci-lint: only `run` or `run ./...` is allowed")
}

// allowedPrograms is the whole allowlist: a closed set of program
// names, each with its own checker validating the full remaining
// argument shape. A program not present here is denied unconditionally
// by CheckCommand — this map is the entire policy, not a starting
// point a blocklist then narrows.
var allowedPrograms = map[string]func([]string) Decision{
	"go":            checkGo,
	"npm":           checkNpm,
	"pip":           checkPip,
	"make":          checkMake,
	"golangci-lint": checkGolangciLint,
}
