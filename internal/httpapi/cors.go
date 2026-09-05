package httpapi

import (
	"net/http"
	"net/url"
	"os"

	"github.com/go-chi/cors"
)

// corsAllowedMethods and corsAllowedHeaders are scoped to exactly what
// the frontend's cross-origin fetch calls need as of Phase 3 step 7
// (the connect-agents screen: GET/POST/DELETE /v1/credentials, JSON
// bodies). Extend them when a later step's frontend work actually needs
// more — not preemptively.
var (
	corsAllowedMethods = []string{http.MethodGet, http.MethodPost, http.MethodDelete}
	corsAllowedHeaders = []string{"Content-Type"}
)

// newCORSMiddleware builds the CORS handler for the frontend's
// cross-origin fetch calls, or nil if HARMONIA_APP_URL isn't set to a
// real absolute URL. No frontend origin configured means no CORS
// middleware installed at all — never "allow every origin," which is
// what an empty AllowedOrigins list means to go-chi/cors's Options.
// This is the same fail-closed, honest-incompleteness posture as an
// unconfigured GoogleConfig or credentials cipher elsewhere in this API.
func newCORSMiddleware() func(http.Handler) http.Handler {
	origin := frontendOrigin()
	if origin == "" {
		return nil
	}
	return cors.Handler(cors.Options{
		AllowedOrigins:   []string{origin},
		AllowedMethods:   corsAllowedMethods,
		AllowedHeaders:   corsAllowedHeaders,
		AllowCredentials: true,
		// Session auth is a cookie, not an Authorization header — the
		// browser needs to be told it may send it cross-origin, and this
		// pairs with credentials: "include" on the frontend's apiFetch.
		MaxAge: 600,
	})
}

// frontendOrigin derives the scheme+host CORS should allow from
// HARMONIA_APP_URL — the same env var the OAuth callback redirect
// already uses (see internal/user's appRedirectURL) to send the browser
// back into the frontend after login. Reusing it means one env var
// names the frontend, not two. Empty, unset, or not a real absolute URL
// all mean "not configured" — never an origin that happens to be an
// empty string, which go-chi/cors would otherwise accept literally.
func frontendOrigin() string {
	raw := os.Getenv("HARMONIA_APP_URL")
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
