package user

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	defaultGitHubAuthorizeURL = "https://github.com/login/oauth/authorize"
	defaultGitHubTokenURL     = "https://github.com/login/oauth/access_token"
	defaultGitHubUserURL      = "https://api.github.com/user"

	githubOAuthScope          = "read:user user:email"
	githubOAuthStateTTL       = 10 * time.Minute
	githubOAuthStateKeyPrefix = "oauth_state:github:"
)

// GitHubConfig holds this deployment's GitHub OAuth App credentials and
// callback URL, plus the endpoints to call. ClientID/ClientSecret/
// RedirectURI come from GITHUB_CLIENT_ID/GITHUB_CLIENT_SECRET/
// GITHUB_REDIRECT_URI — see the Phase 2 build brief's prerequisite for
// how those get registered with GitHub. All three may be empty in an
// environment that hasn't done that yet; the handlers below fail cleanly
// with a 500 rather than send a broken redirect or panic.
//
// AuthorizeURL/TokenURL/UserURL default to the real GitHub hosts.
// Overriding them (and HTTPClient) points the client at a fake OAuth
// server for tests — the same technique used for the Anthropic client's
// structural test in Milestone 1.
type GitHubConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string

	AuthorizeURL string
	TokenURL     string
	UserURL      string

	HTTPClient *http.Client
}

// NewGitHubConfig builds a GitHubConfig pointed at the real GitHub hosts.
func NewGitHubConfig(clientID, clientSecret, redirectURI string) GitHubConfig {
	return GitHubConfig{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURI:  redirectURI,
		AuthorizeURL: defaultGitHubAuthorizeURL,
		TokenURL:     defaultGitHubTokenURL,
		UserURL:      defaultGitHubUserURL,
		HTTPClient:   http.DefaultClient,
	}
}

func (cfg GitHubConfig) configured() bool {
	return cfg.ClientID != "" && cfg.ClientSecret != "" && cfg.RedirectURI != ""
}

// GitHubLoginHandler returns the handler for GET /v1/auth/github/login.
// It redirects to GitHub's authorize URL with a CSRF state parameter,
// which is stored server-side in Redis with a short TTL rather than in a
// signed cookie — Redis is already this system's home for ephemeral,
// non-authoritative state (see internal/store's package doc), and this
// avoids introducing a second, cookie-signing mechanism for one value.
func (s *Store) GitHubLoginHandler(cfg GitHubConfig, redis *redis.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !cfg.configured() {
			writeError(w, http.StatusInternalServerError, "github oauth is not configured")
			return
		}

		state, err := generateOAuthState()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to start login")
			return
		}
		if err := redis.Set(r.Context(), githubOAuthStateKeyPrefix+state, "1", githubOAuthStateTTL).Err(); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to start login")
			return
		}

		authorizeURL := cfg.AuthorizeURL + "?" + url.Values{
			"client_id":    {cfg.ClientID},
			"redirect_uri": {cfg.RedirectURI},
			"scope":        {githubOAuthScope},
			"state":        {state},
		}.Encode()

		http.Redirect(w, r, authorizeURL, http.StatusFound)
	}
}

// GitHubCallbackHandler returns the handler for GET /v1/auth/github/callback.
// It verifies the CSRF state (one-time use — consumed from Redis on
// check, so a replayed callback fails), exchanges the code, fetches the
// GitHub user, upserts by github_id, issues a session, sets the cookie,
// and redirects into the app.
func (s *Store) GitHubCallbackHandler(cfg GitHubConfig, redis *redis.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !cfg.configured() {
			writeError(w, http.StatusInternalServerError, "github oauth is not configured")
			return
		}

		code := r.URL.Query().Get("code")
		state := r.URL.Query().Get("state")
		if code == "" || state == "" {
			writeError(w, http.StatusBadRequest, "missing code or state")
			return
		}

		ctx := r.Context()

		n, err := redis.Del(ctx, githubOAuthStateKeyPrefix+state).Result()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to verify state")
			return
		}
		if n == 0 {
			writeError(w, http.StatusBadRequest, "invalid or expired state")
			return
		}

		accessToken, err := exchangeGitHubCode(ctx, cfg, code)
		if err != nil {
			writeError(w, http.StatusBadGateway, "failed to exchange code with github")
			return
		}

		ghUser, err := fetchGitHubUser(ctx, cfg, accessToken)
		if err != nil {
			writeError(w, http.StatusBadGateway, "failed to fetch github user")
			return
		}

		githubID := strconv.FormatInt(ghUser.ID, 10)
		u, err := s.UpsertByGitHubID(ctx, githubID, ghUser.Login, nullableString(ghUser.Name), nullableString(ghUser.AvatarURL), nullableString(ghUser.Email))
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

func generateOAuthState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// appRedirectURL is where a callback sends the browser after issuing a
// session. There's no frontend built yet in this backend-only phase, so
// this defaults to "/" — override with HARMONIA_APP_URL once one exists.
func appRedirectURL() string {
	if u := os.Getenv("HARMONIA_APP_URL"); u != "" {
		return u
	}
	return "/"
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

type githubTokenResponse struct {
	AccessToken      string `json:"access_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func exchangeGitHubCode(ctx context.Context, cfg GitHubConfig, code string) (string, error) {
	form := url.Values{
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
		"code":          {code},
		"redirect_uri":  {cfg.RedirectURI},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("github: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := cfg.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("github: token request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("github: read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github: token exchange: %s: %s", resp.Status, body)
	}

	var parsed githubTokenResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("github: decode token response: %w", err)
	}
	if parsed.Error != "" {
		return "", fmt.Errorf("github: token exchange: %s: %s", parsed.Error, parsed.ErrorDescription)
	}
	if parsed.AccessToken == "" {
		return "", errors.New("github: token exchange: empty access token")
	}
	return parsed.AccessToken, nil
}

type githubUser struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
	Email     string `json:"email"`
}

func fetchGitHubUser(ctx context.Context, cfg GitHubConfig, accessToken string) (githubUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.UserURL, nil)
	if err != nil {
		return githubUser{}, fmt.Errorf("github: build user request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := cfg.HTTPClient.Do(req)
	if err != nil {
		return githubUser{}, fmt.Errorf("github: user request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return githubUser{}, fmt.Errorf("github: read user response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return githubUser{}, fmt.Errorf("github: fetch user: %s: %s", resp.Status, body)
	}

	var u githubUser
	if err := json.Unmarshal(body, &u); err != nil {
		return githubUser{}, fmt.Errorf("github: decode user response: %w", err)
	}
	if u.ID == 0 {
		return githubUser{}, errors.New("github: fetch user: missing id")
	}
	return u, nil
}
