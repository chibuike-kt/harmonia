// Package httpapi builds the chi router wiring every HTTP handler in this
// API together. Extracted out of cmd/server/main.go, whose package main
// can't be imported, so the Milestone 1 acceptance test can spin up the
// real router in an httptest.Server and drive it over actual HTTP — the
// same router the production binary serves, not a second copy of the
// wiring that could quietly drift from it.
package httpapi

import (
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/contextengine"
	"github.com/chibuike-kt/harmonia/internal/credentials"
	"github.com/chibuike-kt/harmonia/internal/event"
	"github.com/chibuike-kt/harmonia/internal/handoff"
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/store"
	"github.com/chibuike-kt/harmonia/internal/task"
	"github.com/chibuike-kt/harmonia/internal/user"
)

// NewRouter builds the full Milestone 1 HTTP surface bound to st.
func NewRouter(st *store.Store) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	rooms := room.NewStore(st.Pool)
	// No auth on room creation yet — needs human-session auth once the OAuth/BYOK phase lands; intentionally open for now, not an oversight.
	r.Post("/v1/rooms", rooms.CreateHandler())

	agents := agent.NewStore(st.Pool)
	// No auth on agent registration yet — needs human-session auth once the OAuth/BYOK phase lands; intentionally open for now, not an oversight.
	r.Post("/v1/rooms/{room_id}/agents", agents.RegisterHandler())

	tasks := task.NewStore(st.Pool)
	events := event.NewStore(st.Pool)
	beginner := store.PoolBeginner{Pool: st.Pool}
	r.Group(func(pr chi.Router) {
		pr.Use(agent.Authenticate(agents))
		pr.Post("/v1/tasks", tasks.CreateHandler(beginner))
		pr.Post("/v1/tasks/{id}/claim", tasks.ClaimHandler(beginner))
		pr.Post("/v1/tasks/{id}/complete", tasks.CompleteHandler(beginner))
	})

	contexts := contextengine.NewStore(st.Pool)
	r.Group(func(pr chi.Router) {
		pr.Use(agent.Authenticate(agents))
		pr.Get("/v1/context/tasks/{task_id}", contexts.TaskHandler(events))
	})

	handoffs := handoff.NewStore(st.Pool)
	r.Group(func(pr chi.Router) {
		pr.Use(agent.Authenticate(agents))
		pr.Post("/v1/handoffs", handoffs.RequestHandler(tasks, agents, beginner))
		pr.Post("/v1/handoffs/{id}/accept", handoffs.AcceptHandler(beginner))
	})

	r.Group(func(pr chi.Router) {
		pr.Use(agent.Authenticate(agents))
		pr.Use(agent.RequireRoom("id"))
		pr.Get("/v1/rooms/{id}/events", events.ListByRoomHandler())
	})

	users := user.NewStore(st.Pool)
	githubCfg := user.NewGitHubConfig(
		os.Getenv("GITHUB_CLIENT_ID"),
		os.Getenv("GITHUB_CLIENT_SECRET"),
		os.Getenv("GITHUB_REDIRECT_URI"),
	)
	r.Get("/v1/auth/github/login", users.GitHubLoginHandler(githubCfg, st.Redis))
	r.Get("/v1/auth/github/callback", users.GitHubCallbackHandler(githubCfg, st.Redis))

	googleCfg := user.NewGoogleConfig(
		os.Getenv("GOOGLE_CLIENT_ID"),
		os.Getenv("GOOGLE_CLIENT_SECRET"),
		os.Getenv("GOOGLE_REDIRECT_URI"),
	)
	r.Get("/v1/auth/google/login", users.GoogleLoginHandler(googleCfg, st.Redis))
	r.Get("/v1/auth/google/callback", users.GoogleCallbackHandler(googleCfg, st.Redis))

	r.Post("/v1/auth/logout", users.LogoutHandler())

	r.Group(func(pr chi.Router) {
		pr.Use(user.Authenticate(users))
		pr.Get("/v1/sessions", users.SessionsHandler())
		pr.Delete("/v1/sessions/{id}", users.RevokeSessionHandler())
	})

	// cipher is nil when HARMONIA_CREDENTIAL_ENCRYPTION_KEY is unset or
	// malformed; ConnectHandler then fails cleanly with a 500 rather than
	// panicking or storing something insecurely — same posture as an
	// unconfigured GoogleConfig/GitHubConfig.
	cipher, _ := credentials.NewCipher(os.Getenv("HARMONIA_CREDENTIAL_ENCRYPTION_KEY"))
	creds := credentials.NewStore(st.Pool, cipher)
	r.Group(func(pr chi.Router) {
		pr.Use(user.Authenticate(users))
		pr.Post("/v1/credentials", creds.ConnectHandler())
		pr.Get("/v1/credentials", creds.ListHandler())
		pr.Delete("/v1/credentials/{provider}", creds.DeleteHandler())
	})

	return r
}
