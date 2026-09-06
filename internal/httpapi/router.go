// Package httpapi builds the chi router wiring every HTTP handler in this
// API together. Extracted out of cmd/server/main.go, whose package main
// can't be imported, so the Milestone 1 acceptance test can spin up the
// real router in an httptest.Server and drive it over actual HTTP — the
// same router the production binary serves, not a second copy of the
// wiring that could quietly drift from it.
package httpapi

import (
	"context"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/contextengine"
	"github.com/chibuike-kt/harmonia/internal/credentials"
	"github.com/chibuike-kt/harmonia/internal/event"
	"github.com/chibuike-kt/harmonia/internal/handoff"
	"github.com/chibuike-kt/harmonia/internal/message"
	"github.com/chibuike-kt/harmonia/internal/realtime"
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
	// CORS must wrap every route it applies to, preflight included, so it
	// goes on before any route is registered — see cors.go for why a
	// missing HARMONIA_APP_URL means no CORS middleware at all rather
	// than an insecure default.
	if corsMiddleware := newCORSMiddleware(); corsMiddleware != nil {
		r.Use(corsMiddleware)
	}

	r.Get("/healthz", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	users := user.NewStore(st.Pool)

	rooms := room.NewStore(st.Pool)
	agents := agent.NewStore(st.Pool)
	r.Group(func(pr chi.Router) {
		pr.Use(user.Authenticate(users))
		pr.Post("/v1/rooms", rooms.CreateHandler())
		pr.Get("/v1/rooms", rooms.ListHandler())
		pr.Patch("/v1/rooms/{id}", rooms.UpdateHandler())
		pr.Delete("/v1/rooms/{id}", rooms.DeleteHandler())
		pr.Post("/v1/rooms/{room_id}/agents", agents.RegisterHandler(rooms))
		pr.Get("/v1/rooms/{room_id}/agents", agents.ListByRoomHandler(rooms))
	})

	// hub is the single in-process realtime fan-out point for this server —
	// see ADR-003. Shared across every handler below that publishes an
	// event or presence transition, and by the SSE endpoint that
	// subscribes to it.
	hub := realtime.NewHub()

	tasks := task.NewStore(st.Pool)
	events := event.NewStore(st.Pool)
	beginner := store.PoolBeginner{Pool: st.Pool}
	r.Group(func(pr chi.Router) {
		pr.Use(agent.Authenticate(agents))
		pr.Post("/v1/tasks", tasks.CreateHandler(beginner, hub))
		pr.Post("/v1/tasks/{id}/claim", tasks.ClaimHandler(beginner, hub, st.Redis))
		pr.Post("/v1/tasks/{id}/complete", tasks.CompleteHandler(beginner, hub, st.Redis))
	})

	// cipher is nil when HARMONIA_CREDENTIAL_ENCRYPTION_KEY is unset or
	// malformed; built here (rather than down by the /v1/credentials
	// routes) since the message orchestrator needs the same creds store
	// to resolve a mentioned agent's provider client via Resolve —
	// credentials.Store.Resolve's first real production caller.
	cipher, _ := credentials.NewCipher(os.Getenv("HARMONIA_CREDENTIAL_ENCRYPTION_KEY"))
	creds := credentials.NewStore(st.Pool, cipher)

	messages := message.NewStore(st.Pool)
	orchestrator := message.NewOrchestrator(messages, agents, creds, hub, st.Redis)
	titleGen := message.NewTitleGenerator(rooms, creds, hub)
	r.Group(func(pr chi.Router) {
		pr.Use(user.Authenticate(users))
		pr.Post("/v1/rooms/{room_id}/messages", messages.CreateHandler(rooms, agents, beginner, hub, orchestrator, titleGen))
	})

	contexts := contextengine.NewStore(st.Pool)
	r.Group(func(pr chi.Router) {
		pr.Use(agent.Authenticate(agents))
		pr.Get("/v1/context/tasks/{task_id}", contexts.TaskHandler(events))
	})

	handoffs := handoff.NewStore(st.Pool)
	r.Group(func(pr chi.Router) {
		pr.Use(agent.Authenticate(agents))
		pr.Post("/v1/handoffs", handoffs.RequestHandler(tasks, agents, beginner, hub))
		pr.Post("/v1/handoffs/{id}/accept", handoffs.AcceptHandler(beginner, hub))
	})

	r.Group(func(pr chi.Router) {
		pr.Use(agent.Authenticate(agents))
		pr.Use(agent.RequireRoom("id"))
		pr.Get("/v1/rooms/{id}/events", events.ListByRoomHandler())
	})

	// listMessages adapts message.Store.ListByRoom to realtime.MessageLister
	// so the SSE snapshot can include recent chat history without
	// realtime importing internal/message back (see MessageLister's own
	// doc comment for why that would be a cycle).
	listMessages := func(ctx context.Context, roomID uuid.UUID) ([]realtime.ChatMessage, error) {
		msgs, err := messages.ListByRoom(ctx, roomID)
		if err != nil {
			return nil, err
		}
		out := make([]realtime.ChatMessage, len(msgs))
		for i, m := range msgs {
			out[i] = realtime.ChatMessage{
				ID:               m.ID,
				RoomID:           m.RoomID,
				SenderKind:       string(m.SenderKind),
				UserID:           m.UserID,
				AgentID:          m.AgentID,
				MentionedAgentID: m.MentionedAgentID,
				ReplyToMessageID: m.ReplyToMessageID,
				Content:          m.Content,
				CreatedAt:        m.CreatedAt,
			}
		}
		return out, nil
	}
	r.Group(func(pr chi.Router) {
		pr.Use(user.Authenticate(users))
		pr.Get("/v1/rooms/{room_id}/stream", realtime.StreamHandler(rooms, agents, events, listMessages, hub, st.Redis))
	})

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

	// creds (built above, alongside the message orchestrator) fails
	// cleanly with a 500 rather than panicking or storing something
	// insecurely when its cipher is nil — same posture as an
	// unconfigured GoogleConfig/GitHubConfig.
	r.Group(func(pr chi.Router) {
		pr.Use(user.Authenticate(users))
		pr.Post("/v1/credentials", creds.ConnectHandler())
		pr.Get("/v1/credentials", creds.ListHandler())
		pr.Delete("/v1/credentials/{provider}", creds.DeleteHandler())
	})

	r.Group(func(pr chi.Router) {
		pr.Use(user.Authenticate(users))
		pr.Get("/v1/users/me", users.MeHandler())
		pr.Patch("/v1/users/me", users.UpdateMeHandler())
	})

	return r
}
