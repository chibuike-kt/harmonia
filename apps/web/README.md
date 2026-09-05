# Harmonia web

Next.js frontend for Harmonia. Talks directly to the Go backend
(`internal/httpapi`, served by `cmd/server`) — no API routes of its own.

## Setup

```bash
cp .env.example .env.local   # set NEXT_PUBLIC_API_BASE_URL, e.g. http://localhost:8080
npm install
npm run dev
```

The backend must be running separately (`make run` from the repo root)
for anything beyond the static shell to work.

## Scripts

- `npm run dev` / `npm run build` / `npm run start` — Next.js dev/build/serve.
- `npm run lint` — ESLint.
- `npm run typecheck` — `tsc --noEmit`.
- `npm run format` / `npm run format:check` — Prettier.

All four checks (lint, typecheck, format, build) run in CI as the `web`
job, independently of the Go backend's job.
