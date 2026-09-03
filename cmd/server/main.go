package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/store"
)

func main() {
	ctx := context.Background()

	dbURL := os.Getenv("HARMONIA_DATABASE_URL")
	redisAddr := os.Getenv("HARMONIA_REDIS_ADDR")
	addr := os.Getenv("HARMONIA_HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	st, err := store.New(ctx, dbURL, redisAddr)
	if err != nil {
		log.Fatalf("store init: %v", err)
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

	// TODO: register task/handoff/context HTTP handlers here as each
	// package's HTTP layer is built.

	log.Printf("harmonia listening on %s", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatal(err)
	}
}
