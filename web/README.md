# TemperCI dashboard (Vite + React + Tailwind CSS)

Operator console embedded into `temperci-control`.

Styling uses **Tailwind CSS v4** via `@tailwindcss/vite`. Theme tokens and shared component classes live in `src/styles.css` (`@theme` + `@layer components`).

## Scripts

```bash
npm install
npm run dev      # http://127.0.0.1:5173 — proxies /api to :8080
npm run build    # writes ../internal/webui/dist for go:embed
```

From repo root, prefer:

```bash
make build-ui    # npm ci (if needed) + vite build
make build       # UI + Go binaries
```

## Serving

`make build` produces `internal/webui/dist/`. Go embeds that directory (`internal/webui/embed.go`) and serves it at `/` with SPA fallback for client routes (`/hosts`, `/jobs`, …).
