package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/chibuike-kt/harmonia/internal/httpapi"
	"github.com/chibuike-kt/harmonia/internal/store"
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

	r := httpapi.NewRouter(st)

	log.Printf("harmonia listening on %s", addr)
	return http.ListenAndServe(addr, r)
}
