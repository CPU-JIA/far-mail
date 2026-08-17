# Project Constraints

- Backend runtime is Go only. Do not add Rust services, crates, build stages, or compatibility code.
- Frontend is Vite + Vue 3 + TypeScript. Preserve the calm black-and-white owner-console visual system and use Lucide icons for interface actions.
- The product has one site owner, not tenants or self-service user accounts. Do not reintroduce tenant, registration, account switching, or per-user ownership flows.
- Preserve public domain donation and reward tokens. Public submit/status routes live under `/public/v1`; existing reward tokens add domains through `POST /api/v1/donations`. Reward tokens never authenticate `/console/v1`.
- Admin Key and API Token are different credentials and stores:
  - Admin Key: `X-Admin-Key`, `/console/v1`, format `sk-<custom>-<16|32 hex>`.
  - API Token: `Authorization: Bearer`, `/api/v1`, 32 hex secret.
  - Never add fallback between them, `X-API-Key`, query-string keys, or shared signing/rotation flows.
- Public routes are limited to `/public/v1`; obsolete API prefixes must return 404 instead of falling through to the SPA.
- Deployment values such as SMTP IP/hostname come from `.env` or owner settings. Never hard-code a real server address.
- Keep secrets out of logs and responses. `data/admin.key`, `.env`, runtime mail data, screenshots, and dumps stay untracked.

## Commands

```powershell
cd api
go test ./...

cd ..\frontend
npm run typecheck
npm run build

cd server
go test ./...

cd ..\..
docker compose config --quiet
docker compose up -d --build
docker compose ps
```

## Runtime Notes

- Local console: `http://127.0.0.1:8889`
- Local API health: `http://127.0.0.1:18081/health`
- Postfix delivers to the Go LMTP ingress at `api:2527`.
- `INTERNAL_SYNC_KEY` is required and shared only by API/Postfix for `/internal/domains.txt`.
- The frontend static server never proxies API traffic. User-selected edge proxies route versioned API paths to `api:8080` and all other browser paths to `frontend:8080` over the dedicated edge network.
- Route pages are lazy-loaded. Polling must pause in hidden tabs, prevent overlapping requests, and remove listeners/timers on unmount.
- `/api/v1` observability retains 14 days in `api_request_events`; record only Token ID, route template, method, status, latency, Request ID and timestamp. Never capture credentials, query strings, bodies or responses.
- Donation rewards require both MX and per-claim TXT verification. Reward tokens only access mailboxes they created through `mailboxes.creator_token_id`.
