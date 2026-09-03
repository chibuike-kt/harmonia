package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

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

	// TODO: register room/task/agent/handoff/context HTTP handlers here as
	// each package's HTTP layer is built. Scaffold intentionally stops at
	// wiring — routes are Milestone 1 build work, not scaffold work.

	log.Printf("harmonia listening on %s", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatal(err)
	}
}
