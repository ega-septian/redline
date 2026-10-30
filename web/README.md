# Redline UI

React + TypeScript + Vite for the Redline dashboard. The Go server embeds `dist/` with
`go:embed` and serves it at `/ui/`, so production is still one binary.

## Development

Two terminals:

```bash
# 1. the API and database
docker compose up -d
go run ./cmd/server          # http://localhost:8787

# 2. the UI
cd web
npm install
npm run dev                  # http://localhost:5173/ui/
```

Vite proxies `/api`, `/healthz` and `/readyz` to `localhost:8787`, so the UI uses the same
origin rules as production.

If the server has `REDLINE_API_KEY` set, `/ui/` redirects to `/ui/login`; sign in with that key.
The resulting cookie is HttpOnly, SameSite=Lax and lasts 12 hours. Without a key (the loopback
default), the UI opens directly.

## Build

```bash
cd web
npm ci
npm run build                # type-check, then write dist/
```

Then `go build ./cmd/server` embeds the new `dist/`. Without a build, `/ui/` shows a
"UI is not built" page instead of a blank screen.

## Structure

```
src/
├── api/                     typed client for /api/groups, /runs and /impact
├── components/
│   ├── app-shell.tsx        layout, sidebar, health status
│   ├── failures/            list, detail, label form, KPI cards
│   └── ui/                  shadcn-style primitives (button, badge, card, input)
├── lib/failure.ts           API group -> view model, labels and formatting
├── pages/
│   ├── failures.tsx         Failures (TanStack Query + Router)
│   ├── runs.tsx             recent runs, filters and results
│   └── impact.tsx           file/endpoint impact lookup
├── routeTree.tsx            TanStack Router tree
└── main.tsx                 QueryClient + Router
```

The Failures screen reads `/api/groups`; labels are written from the detail panel with the
`X-CSRF-Token` double-submit cookie. Runs and Impact are read-only. Analysis and rule decisions
stay in the CLI/API.
