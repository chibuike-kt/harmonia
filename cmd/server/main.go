package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/contextengine"
	"github.com/chibuike-kt/harmonia/internal/event"
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/store"
	"github.com/chibuike-kt/harmonia/internal/task"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run holds everything that needs to unwind through defers before the
// process exits — main itself never defers, so its log.Fatal is never in
// the same frame as a deferred cleanup that would get skipped.
func run() error {
	ctx := context.Background()

	dbURL := os.Getenv("HARMONIA_DATABASE_URL")
	redisAddr := os.Getenv("HARMONIA_REDIS_ADDR")
	addr := os.Getenv("HARMONIA_HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	st, err := store.New(ctx, dbURL, redisAddr)
	if err != nil {
		return err
	}
	defer st.Close()

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

	// TODO: register handoff HTTP handlers here as its HTTP layer is
	// built, and the room event-history handler.

	log.Printf("harmonia listening on %s", addr)
	return http.ListenAndServe(addr, r)
}
