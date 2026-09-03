# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS builder
WORKDIR /src

# Cache dependency downloads separately from source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/harmonia ./cmd/server

FROM alpine:3.24
RUN apk add --no-cache ca-certificates
COPY --from=builder /out/harmonia /usr/local/bin/harmonia
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/harmonia"]
