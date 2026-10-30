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
├── components/
│   ├── app-shell.tsx        layout, sidebar, health status
│   ├── failures/            list, detail, KPI cards
│   └── ui/                  shadcn-style primitives (button, badge, card, input)
├── data/mock.ts             sample failures until the API is wired
├── lib/utils.ts             cn()
├── pages/failures.tsx       the Failures screen
├── routeTree.tsx            TanStack Router tree
└── main.tsx                 QueryClient + Router
```

The next PR replaces `data/mock.ts` with `GET /api/groups` and adds the login cookie.
