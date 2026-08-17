# Operator Runbook

## Start And Verify

```powershell
docker compose config --quiet
docker compose up -d --build
docker compose ps

Invoke-WebRequest -UseBasicParsing http://127.0.0.1:18081/health
Invoke-WebRequest -UseBasicParsing http://127.0.0.1:8889/
Invoke-WebRequest -UseBasicParsing http://127.0.0.1:8889/runtime-config.js
```

The default Compose file binds the independent frontend to `127.0.0.1:8889` and the Go API to `127.0.0.1:18081`. Local runtime configuration connects them directly. Production uses the deployment's Nginx, Caddy, Traefik, or equivalent; see [reverse-proxy.md](reverse-proxy.md). SMTP remains public on port 25.

The Admin Key is written to `data/admin.key`. Do not print the full key in logs, tickets, screenshots, or shell history. API Tokens are created and rotated in the owner console and are only shown once.

## Required Configuration

- `POSTGRES_PASSWORD`, `API_DB_DSN`: must contain the same database password.
- `REDIS_PASSWORD`, `API_REDIS_PASSWORD`: must contain the same Redis password.
- `INTERNAL_SYNC_KEY`: required private API/Postfix sync secret; generate with `openssl rand -hex 32`.
- `SMTP_SERVER_IP`: public mail-server IP used for MX/SPF checks.
- `SMTP_HOSTNAME`: mail host such as `mail.example.com`.
- `CONSOLE_ORIGIN`: exact browser origin allowed by CORS.
- `PUBLIC_API_ORIGIN`: browser-visible API origin; leave empty for a same-origin production proxy.
- `EDGE_NETWORK_NAME`: stable Docker network shared only by frontend, API, and an optional edge proxy.
- `TRUSTED_PROXY_CIDRS`: comma-separated CIDRs for reverse proxies whose `X-Forwarded-For` is trusted; keep only explicitly managed proxy ranges in production.
- `GOPROXY`, `NPM_REGISTRY`: Docker build dependency mirrors; override these when the deployment network requires another registry.
- `DISABLE_CLEANUP_LOOP`, `DISABLE_DOMAIN_HEALTH_LOOP`, `DISABLE_MX_VERIFIER_LOOP`: emergency maintenance switches; keep all three `false` during normal operation.
- `AUTO_CLEAN_INTERVAL_SECONDS`, `AUTO_CLEAN_EMAIL_MAX_AGE_MINUTES`, `AUTO_CLEAN_EMAIL_MAX_COUNT`: automatic cleanup cadence and retention limits; preview impact in the console before changing retention.

For local Compose access, `PUBLIC_API_ORIGIN=http://127.0.0.1:18081` connects the frontend directly to the API. When `CONSOLE_ORIGIN` uses `localhost:8889` (or `127.0.0.1:8889`), the API accepts the equivalent loopback origin on the same port. For Vite development use port `5173`; production hostnames remain exact-origin only.

SMTP values may also be saved in Owner Console settings. Non-empty settings override environment values; Operations shows whether the effective value came from settings, environment, or both.

## Operational Checks

The Operations page reports real runtime state:

- PostgreSQL and Redis ping status.
- SMTP effective host/IP, configuration source, and port-25 reachability.
- Go LMTP running state, address, queue depth, workers, connection count, failures, parse time, and database time.
- Domain root/wildcard MX health and latest health snapshot.
- Database-backed mailbox and email totals.
- `/api/v1` request totals, errors, average/P95 latency, hourly activity, top routes and recent Request IDs.
- Maintenance impact before cleanup: expired/empty mailboxes, old email rows and estimated stored bytes.

The header action copies a diagnostic snapshot assembled in the browser from these authorized summaries. It excludes Admin Keys, API Tokens, message content, request bodies and query strings.

Cloudflare DNS actions are always previewed before apply. The apply operation only writes records marked as safe or explicitly confirmed; if a later write fails, records already changed in that operation are automatically restored when possible. Operational metadata is available in `GET /console/v1/operations/audit?limit=50` and contains only integration, action, domain, result, detail and timestamp—never API credentials or provider response bodies.

Useful commands:

```powershell
docker compose logs --tail 100 api frontend postfix
docker stats --no-stream
docker compose exec -T postfix postqueue -p
docker compose exec -T postgres pg_isready -U far_mail -d far_mail
$redisPassword = (Get-Content .env | Where-Object { $_ -match '^REDIS_PASSWORD=' } | Select-Object -First 1).Split('=', 2)[1].Trim().Trim('"').Trim("'")
docker compose exec -T -e REDISCLI_AUTH=$redisPassword redis redis-cli ping
```

The API usage range accepts `hours=1..336`:

```powershell
$headers = @{ 'X-Admin-Key' = '<admin-key>' }
Invoke-RestMethod -Headers $headers 'http://127.0.0.1:18081/console/v1/operations/api-usage?hours=24'
Invoke-RestMethod -Headers $headers 'http://127.0.0.1:18081/console/v1/operations/maintenance/preview?older_than_minutes=240'
```

Keep real credentials out of shared shell history. Prefer the console for routine checks.

## Authentication Boundary

| Channel | Credential | Valid path |
|---|---|---|
| Owner Console | `X-Admin-Key` | `/console/v1/*` |
| Automation API | `Authorization: Bearer` | `/api/v1/*` |
| Donation continuation | donation reward Bearer Token | `POST /api/v1/donations` only; this call does not consume reward quota |
| Public | none | `/public/v1/*` |

Expected negative tests: missing credentials return 401; an Admin Key used as Bearer returns 401; an API Token used as `X-Admin-Key` returns 401; obsolete `/api/*`, `/v1/*`, `/auth/*`, `/register*`, and `/accounts*` paths return 404.

API Token minute/day counters use Redis fixed windows and fail closed with `503 redis_unavailable` if Redis cannot enforce configured limits. Daily counters reset on the Asia/Shanghai calendar day.

## Donation Rewards

- `POST /public/v1/domains/submit` creates a pending donation, claim secret, and one-time 32-character lowercase-hex reward token.
- `POST /public/v1/domains/status` requires `donation_id` and `claim_secret` in the JSON body. The obsolete enumerable GET status route must remain 404.
- Activation requires the configured receiving MX and `_far-mail-donate` TXT challenge. Pending claims are retried at short intervals; active claims use `donation_recheck_minutes` (30 by default).
- Definitive DNS mismatch removes that domain's reward immediately. Temporary resolver errors use `donation_dns_failure_tolerance` (3 by default). A successful recheck restores the same grant.
- Reward-token mailboxes are isolated by `mailboxes.creator_token_id`; the owner console can still inspect the entire site.
- Use Console > Donation Plan to inspect reward Tokens, contributed domains and the quota ledger; recheck, revoke/restore, adjust Token quotas, change pool policy, and explicitly apply the latest policy to existing non-revoked donations.

Schema check after an API rebuild:

```powershell
docker compose exec -T postgres psql -U far_mail -d far_mail -c "\d domain_donations"
docker compose exec -T postgres psql -U far_mail -d far_mail -c "\d donation_reward_events"
docker compose exec -T postgres psql -U far_mail -d far_mail -c "\d api_request_events"
```

## Performance And Resource Notes

- Route components are lazy-loaded; the main frontend bundle is about 56 KB gzip (about 145 KB uncompressed; verify exact output with `npm run build`).
- Inbox and donated-domain polling pause while the tab is hidden and prevent overlapping requests.
- Public-settings/API requests have a 10-second browser timeout.
- System Summary is cached for 2 seconds and SMTP reachability for 10 seconds.
- API observability uses a bounded 4096-event queue, 256-row batches and a one-second flush interval. Events older than 14 days are deleted hourly.
- Long mailbox, email, Token and donation lists use off-screen rendering containment.
- Go keeps a small PgBouncer-facing database pool (32 connections max, 45-second idle reap); mailbox pagination returns count and rows in one SQL round trip. Redis uses a 16-connection ceiling but retains at most 4 idle connections, reducing post-burst sockets on low-memory hosts.
- Docker builds cache Go/npm dependencies before copying source. Compose defaults to `GOPROXY=https://goproxy.cn,direct` and `NPM_REGISTRY=https://registry.npmmirror.com`; both are build args configurable through `.env`.

## Common Failures

- `502` from the external proxy: verify that it joined `EDGE_NETWORK_NAME`, can resolve `api` and `frontend`, and routes the versioned API prefixes before the frontend fallback. Then check `docker compose logs api frontend`.
- Console loads but API calls fail: inspect `/runtime-config.js`, confirm `PUBLIC_API_ORIGIN`, and ensure `CONSOLE_ORIGIN` exactly matches the browser origin.
- API refuses to start: ensure `INTERNAL_SYNC_KEY` is present and is not a template/default value.
- SMTP configured but unreachable: verify port 25, firewall/security-group rules, DNS, and the effective host/IP shown in Operations.
- Domain remains pending: verify the displayed MX target, then inspect API/Postfix logs. The verifier checks pending domains every 30 seconds.
- Donation remains pending: verify both MX and the exact `_far-mail-donate` TXT value. TXT propagation failures are reported separately from MX mismatch.
- Reward Token returns `429 donation_reward_inactive`: it has no active verified domain grant, or its effective total/RPM was reduced to zero. Recheck or adjust it in Donation Plan.
- API usage remains empty: make a successful or failing `/api/v1` request with an API Token, wait at least one second for the batch flush, then check API logs for `[api-observability]` errors and inspect `api_request_events`.

## Runtime Data

`data/` and `backups/` are runtime/operator-owned and intentionally excluded from Git. The current Go ingress stores bounded message content in PostgreSQL and does not create new files under `data/raw`; old files there may belong to the retired implementation. Review and back them up before any manual cleanup—do not delete them automatically without an explicit retention decision.

## Backup And Restore

PostgreSQL is the authoritative store for owner settings, domains, Tokens, donations, mailboxes, messages and API observability. Create a compressed custom-format backup plus SHA-256 checksum and manifest:

```powershell
.\scripts\backup.ps1
```

```sh
sh ./scripts/backup.sh
```

Backup files contain private operational data and credentials such as the Admin Key. Keep `backups/` access-restricted and encrypted off-host; it is intentionally ignored by Git. A backup is not accepted by the restore scripts unless its adjacent `.sha256` file matches.

Always rehearse into a temporary database first. The confirmation value must exactly equal the target database; the example verifies the required schema and deletes only the temporary database after success:

```powershell
$backup = Get-ChildItem .\backups\*.dump | Sort-Object LastWriteTime -Descending | Select-Object -First 1
.\scripts\restore.ps1 -BackupPath $backup.FullName -TargetDatabase far_mail_restore_check -ConfirmDatabase far_mail_restore_check -DropAfterVerify
```

```sh
backup=$(ls -1t ./backups/*.dump | head -n 1)
sh ./scripts/restore.sh "$backup" far_mail_restore_check far_mail_restore_check --drop-after-verify
```

Replacing the configured live database additionally requires `-AllowProductionRestore` or `--allow-production`. The scripts stop API, Postfix and PgBouncer around that operation and restart them afterward. Take a fresh backup first and schedule a maintenance window.

Redis stores only fixed-window rate-limit counters. AOF with one-second fsync keeps those counters across ordinary container restarts in the `redisdata` volume; PostgreSQL backups deliberately exclude Redis so a disaster restore cannot resurrect stale limiter windows. Losing the Redis volume resets current minute/day windows but does not lose mail, configuration, donation grants or durable API usage totals.
