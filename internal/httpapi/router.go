// Package httpapi builds the chi router wiring every HTTP handler in this
// API together. Extracted out of cmd/server/main.go, whose package main
// can't be imported, so the Milestone 1 acceptance test can spin up the
// real router in an httptest.Server and drive it over actual HTTP — the
// same router the production binary serves, not a second copy of the
// wiring that could quietly drift from it.
package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/contextengine"
	"github.com/chibuike-kt/harmonia/internal/event"
	"github.com/chibuike-kt/harmonia/internal/handoff"
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/store"
	"github.com/chibuike-kt/harmonia/internal/task"
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

	return r
}
