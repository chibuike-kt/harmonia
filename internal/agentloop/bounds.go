// Package agentloop implements ADR-011's sustained autonomous agentic
// loop, Batch A only (the loop mechanism itself — see the ADR and
// docs/sustained-agentic-loops-build-brief.md for what Batch B, the
// in-loop delegation exception, still needs and deliberately isn't here
// yet). A Session cycles an agent through tool-call -> observe -> decide
// -> tool-call again against the exact IDE tools already real elsewhere
// in this codebase (the companion relay's write_file, and this package's
// own new read_file/run_command actions built the same way), checking
// before every cycle that a human is still watching (ADR-010's presence
// gate, extended here from one action to a whole session) and that none
// of the session's own required bounds have been exceeded.
package agentloop

import (
	"fmt"
	"time"
)

// Bounds are the three real numbers ADR-011 requires a human to set
// before any session starts — deliberately no default (see the ADR's own
// "Revisit When"): a loop cannot begin without all three.
type Bounds struct {
	MaxCycles    int
	DollarCapUSD float64
	WallClock    time.Duration
}

// Validate reports the first real problem with b, if any — Start calls
// this before doing anything else, so a malformed or missing bound never
// gets as far as resolving a client or opening a terminal.
func (b Bounds) Validate() error {
	if b.MaxCycles <= 0 {
		return fmt.Errorf("agentloop: max cycles must be a positive number, got %d", b.MaxCycles)
	}
	if b.DollarCapUSD <= 0 {
		return fmt.Errorf("agentloop: dollar cap must be a positive number, got %v", b.DollarCapUSD)
	}
	if b.WallClock <= 0 {
		return fmt.Errorf("agentloop: wall-clock limit must be a positive duration, got %v", b.WallClock)
	}
	return nil
}
