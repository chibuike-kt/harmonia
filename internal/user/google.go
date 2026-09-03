package user

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/redis/go-redis/v9"
)

const (
	defaultGoogleAuthorizeURL = "https://accounts.google.com/o/oauth2/v2/auth"
	defaultGoogleTokenURL     = "https://oauth2.googleapis.com/token"
	defaultGoogleIssuer       = "https://accounts.google.com"

	googleOAuthScope          = "openid email profile"
	googleOAuthStateTTL       = 10 * time.Minute
	googleOAuthStateKeyPrefix = "oauth_state:google:"
)

// GoogleConfig holds this deployment's Google OAuth Client credentials
// and callback URL, plus the endpoints to call. Mirrors GitHubConfig's
// shape. ClientID/ClientSecret/RedirectURI come from GOOGLE_CLIENT_ID/
// GOOGLE_CLIENT_SECRET/GOOGLE_REDIRECT_URI — see the Phase 2 build
// brief's prerequisite for how those get registered with Google. All
// three may be empty in an environment that hasn't done that yet; the
// handlers below fail cleanly with a 500 rather than send a broken
// redirect or panic.
//
// AuthorizeURL/TokenURL/Issuer default to the real Google values, with
// verification going through OIDC discovery against Issuer. Overriding
// them (and setting JWKSURL, and HTTPClient) points the client at a fake
// OIDC provider for tests — the same technique used for the GitHub
// client and the Anthropic client's structural test in Milestone 1.
type GoogleConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string

	AuthorizeURL string
	TokenURL     string
	// Issuer is passed to go-oidc for provider discovery, which supplies
	// the JWKS URI and validates the ID token's `iss` claim against it.
	Issuer string
	// JWKSURL, when set, bypasses OIDC discovery and verifies directly
	// against this JWKS endpoint — used by tests to stand in for Google
	// without serving a full discovery document.
	JWKSURL string

	HTTPClient *http.Client

	discovery *googleDiscoveryCache
}

// googleDiscoveryCache memoizes the result of OIDC provider discovery so
// it happens once per process rather than on every callback request.
// GoogleConfig is copied by value into each handler closure, but this
// field is a pointer, so every copy shares the same cache.
type googleDiscoveryCache struct {
	once     sync.Once
	verifier *oidc.IDTokenVerifier
	err      error
}

// NewGoogleConfig builds a GoogleConfig pointed at the real Google hosts.
// JWKSURL is left unset so verification goes through OIDC discovery
// against Issuer, as it should for the real Google integration.
func NewGoogleConfig(clientID, clientSecret, redirectURI string) GoogleConfig {
	return GoogleConfig{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURI:  redirectURI,
		AuthorizeURL: defaultGoogleAuthorizeURL,
		TokenURL:     defaultGoogleTokenURL,
		Issuer:       defaultGoogleIssuer,
		HTTPClient:   http.DefaultClient,
		discovery:    &googleDiscoveryCache{},
	}
}

func (cfg GoogleConfig) configured() bool {
	return cfg.ClientID != "" && cfg.ClientSecret != "" && cfg.RedirectURI != ""
}

// GoogleLoginHandler returns the handler for GET /v1/auth/google/login.
// Same CSRF-state handling as GitHubLoginHandler — server-side in Redis,
// short TTL, one-time use.
func (s *Store) GoogleLoginHandler(cfg GoogleConfig, redis *redis.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !cfg.configured() {
			writeError(w, http.StatusInternalServerError, "google oauth is not configured")
			return
		}

		state, err := generateOAuthState()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to start login")
			return
		}
		if err := redis.Set(r.Context(), googleOAuthStateKeyPrefix+state, "1", googleOAuthStateTTL).Err(); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to start login")
			return
		}

		authorizeURL := cfg.AuthorizeURL + "?" + url.Values{
			"client_id":     {cfg.ClientID},
			"redirect_uri":  {cfg.RedirectURI},
			"response_type": {"code"},
			"scope":         {googleOAuthScope},
			"state":         {state},
		}.Encode()

		http.Redirect(w, r, authorizeURL, http.StatusFound)
	}
}

// GoogleCallbackHandler returns the handler for GET /v1/auth/google/callback.
// It verifies the CSRF state (one-time use, same as GitHub's), exchanges
// the code for an ID token, verifies that ID token's signature against
// Google's published JWKS plus its issuer/audience/expiry, upserts by
// google_id, issues a session, sets the cookie, and redirects into the
// app.
func (s *Store) GoogleCallbackHandler(cfg GoogleConfig, redis *redis.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !cfg.configured() {
			writeError(w, http.StatusInternalServerError, "google oauth is not configured")
			return
		}

		code := r.URL.Query().Get("code")
		state := r.URL.Query().Get("state")
		if code == "" || state == "" {
			writeError(w, http.StatusBadRequest, "missing code or state")
			return
		}

		ctx := r.Context()

		n, err := redis.Del(ctx, googleOAuthStateKeyPrefix+state).Result()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to verify state")
			return
		}
		if n == 0 {
			writeError(w, http.StatusBadRequest, "invalid or expired state")
			return
		}

		idToken, err := exchangeGoogleCode(ctx, cfg, code)
		if err != nil {
			writeError(w, http.StatusBadGateway, "failed to exchange code with google")
			return
		}

		claims, err := verifyGoogleIDToken(ctx, cfg, idToken)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "failed to verify id token")
			return
		}

		var email *string
		if claims.EmailVerified {
			email = nullableString(claims.Email)
		}

		u, err := s.UpsertByGoogleID(ctx, claims.Subject, googleUsername(claims), nullableString(claims.Name), nullableString(claims.Picture), email)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create user")
			return
		}

		if err := s.issueSessionAndSetCookie(w, r, u.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to issue session")
			return
		}

		http.Redirect(w, r, appRedirectURL(), http.StatusFound)
	}
}

// googleUsername derives a username from an ID token's claims — Google
// has no handle/login concept the way GitHub does, only name and email.
// The email's local part is the closest human-friendly stand-in; sub
// (Google's stable numeric account ID) is the fallback if email is
// somehow absent despite the openid+email scope.
func googleUsername(claims googleIDTokenClaims) string {
	if claims.Email != "" {
		if i := strings.Index(claims.Email, "@"); i > 0 {
			return claims.Email[:i]
		}
		return claims.Email
	}
	return claims.Subject
}

type googleTokenResponse struct {
	IDToken          string `json:"id_token"`
	AccessToken      string `json:"access_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func exchangeGoogleCode(ctx context.Context, cfg GoogleConfig, code string) (string, error) {
	form := url.Values{
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
		"code":          {code},
		"redirect_uri":  {cfg.RedirectURI},
		"grant_type":    {"authorization_code"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("google: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := cfg.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("google: token request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("google: read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("google: token exchange: %s: %s", resp.Status, body)
	}

	var parsed googleTokenResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("google: decode token response: %w", err)
	}
	if parsed.Error != "" {
		return "", fmt.Errorf("google: token exchange: %s: %s", parsed.Error, parsed.ErrorDescription)
	}
	if parsed.IDToken == "" {
		return "", errors.New("google: token exchange: empty id_token")
	}
	return parsed.IDToken, nil
}

type googleIDTokenClaims struct {
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
}

// verifyGoogleIDToken verifies idToken against Google's published JWKS —
// signature, issuer, audience, and expiry — using go-oidc rather than
// hand-rolled crypto. JWT/OIDC verification has enough subtle failure
// modes (algorithm confusion, JWKS caching/rotation, Google's dual issuer
// form) that it belongs to a maintained library, unlike the plain REST
// calls this codebase otherwise avoids adding SDKs for.
func verifyGoogleIDToken(ctx context.Context, cfg GoogleConfig, idToken string) (googleIDTokenClaims, error) {
	verifier, err := cfg.verifier(ctx)
	if err != nil {
		return googleIDTokenClaims{}, err
	}

	token, err := verifier.Verify(ctx, idToken)
	if err != nil {
		return googleIDTokenClaims{}, fmt.Errorf("google: verify id_token: %w", err)
	}

	var claims googleIDTokenClaims
	if err := token.Claims(&claims); err != nil {
		return googleIDTokenClaims{}, fmt.Errorf("google: parse id_token claims: %w", err)
	}
	if claims.Subject == "" {
		return googleIDTokenClaims{}, errors.New("google: id_token missing sub")
	}

	return claims, nil
}

// verifier returns the go-oidc verifier for cfg, either against a
// directly-configured JWKS endpoint (the test path) or via cached OIDC
// discovery against cfg.Issuer (the production path). GoogleConfig is
// copied by value into each handler closure, but discovery is a pointer,
// so the sync.Once still runs only once across those copies.
func (cfg GoogleConfig) verifier(ctx context.Context) (*oidc.IDTokenVerifier, error) {
	ctx = oidc.ClientContext(ctx, cfg.HTTPClient)

	if cfg.JWKSURL != "" {
		keySet := oidc.NewRemoteKeySet(ctx, cfg.JWKSURL)
		return oidc.NewVerifier(cfg.Issuer, keySet, &oidc.Config{ClientID: cfg.ClientID}), nil
	}

	if cfg.discovery == nil {
		provider, err := oidc.NewProvider(ctx, cfg.Issuer)
		if err != nil {
			return nil, fmt.Errorf("google: oidc discovery: %w", err)
		}
		return provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}), nil
	}

	cfg.discovery.once.Do(func() {
		provider, err := oidc.NewProvider(ctx, cfg.Issuer)
		if err != nil {
			cfg.discovery.err = fmt.Errorf("google: oidc discovery: %w", err)
			return
		}
		cfg.discovery.verifier = provider.Verifier(&oidc.Config{ClientID: cfg.ClientID})
	})
	return cfg.discovery.verifier, cfg.discovery.err
}
