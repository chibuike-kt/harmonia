// Command companion is the local process a human runs once on their own
// machine so the standalone Harmonia IDE (apps/web's own /ide page) can
// reach a real local project — open a folder, browse/read/write its
// files, run a real shell against it. See internal/companion's own
// package doc for the actual security model (localhost-only bind,
// Origin-checked WebSocket upgrade).
package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/chibuike-kt/harmonia/internal/companion"
)

func main() {
	addr := os.Getenv("HARMONIA_COMPANION_ADDR")
	if addr == "" {
		addr = "127.0.0.1:47821"
	}
	// Loopback-only, checked plainly rather than trusted implicitly —
	// HARMONIA_COMPANION_ADDR is a real operator-facing override
	// (matching every other *_ADDR in this codebase's own convention),
	// and a mistyped 0.0.0.0 here would turn "a local dev tool" into a
	// real shell reachable from anywhere on the network.
	if !strings.HasPrefix(addr, "127.0.0.1:") && !strings.HasPrefix(addr, "localhost:") {
		log.Fatalf("companion: refusing to bind %q — must be 127.0.0.1:<port> or localhost:<port>, never a network-reachable address", addr)
	}

	origins := os.Getenv("HARMONIA_COMPANION_ALLOWED_ORIGINS")
	if origins == "" {
		// The Next.js dev server's own real origin — the only sane
		// default for a companion a developer is running locally against
		// a locally-running Harmonia frontend. A real deployment sets
		// this explicitly to its own real web origin.
		origins = "http://localhost:3000"
	}
	allowed := companion.NewAllowedOrigins(strings.Split(origins, ",")...)

	consentPath := os.Getenv("HARMONIA_COMPANION_CONSENT_PATH")
	if consentPath == "" {
		var err error
		consentPath, err = companion.DefaultConsentPath()
		if err != nil {
			log.Fatalf("companion: couldn't determine consent record path: %v", err)
		}
	}
	consent := companion.NewConsentStore(consentPath)

	mux := http.NewServeMux()
	mux.HandleFunc("/consent", companion.ConsentHandler(consent, allowed))
	mux.HandleFunc("/ws", companion.Handler(allowed, consent))

	log.Printf("harmonia companion listening on %s (allowed origins: %s, consent record: %s)", addr, origins, consentPath)
	log.Fatal(http.ListenAndServe(addr, mux))
}
