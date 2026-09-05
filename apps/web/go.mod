// This file exists only to mark apps/web as a separate module boundary,
// so the root Go module's `./...` pattern (go build, go vet, go test)
// doesn't descend into apps/web/node_modules — some npm packages (e.g.
// "flatted") ship their own vendored .go files, which `./...` would
// otherwise pick up and try to build/test as part of this project.
// apps/web has no Go code of its own; this module is never built.
module github.com/chibuike-kt/harmonia/apps/web/_unused

go 1.26
