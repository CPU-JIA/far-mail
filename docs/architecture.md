# Architecture

## Runtime Topology

```text
Browser -> user-selected edge proxy -> frontend static service
                            |          -> Go HTTP API -> PgBouncer -> PostgreSQL
                            |                         -> Redis
Internet SMTP -> Postfix -> Go LMTP :2527 -> PostgreSQL
```

- `frontend`: Vite + Vue 3 + TypeScript SPA served by a small Go static server; it never proxies API traffic.
- `api`: the only application backend; owns HTTP, domain verification, maintenance loops and LMTP ingestion.
- `postfix`: public SMTP edge, forwarding accepted mail to `api:2527` over LMTP.
- `redis`: API Token RPM/day counters and public endpoint rate limits.
- `postgres`: durable configuration, domains, mailboxes, email, tokens, donations and operational telemetry.

The Compose `edge` network contains only `frontend` and `api`. A user-selected Nginx, Caddy, or other proxy routes browser paths to those independent services; database and queue services remain unreachable from the edge network.
Gin trusts forwarded client IPs only from `TRUSTED_PROXY_CIDRS` (the Compose default is the Docker private range); direct API callers cannot spoof `X-Forwarded-For` to bypass public rate limits.

## Route Boundaries

| Namespace | Authentication | Owner |
|---|---|---|
| `/console/v1/*` | `X-Admin-Key` | private owner console |
| `/api/v1/*` | `Authorization: Bearer` API Token | automation clients |
| `/public/v1/*` | none, or claim secret in request body | public donation/settings |
| `/internal/domains.txt` | `X-Internal-Sync-Key` | API/Postfix only |
| `/health` | none | health probe |

There is no credential fallback. The Go API returns 404 for unknown routes, and the frontend static server rejects API, internal, `/v1/*`, `/auth/*`, `/register*`, and `/accounts*` prefixes before SPA fallback.

The browser API origin is supplied at container start through `PUBLIC_API_ORIGIN`. It is emitted as a no-store runtime script rather than compiled into the Vite bundle. Empty means same-origin routing through the edge proxy; the local default points directly at `127.0.0.1:18081`.

## Credential Stores

- Admin Key is stored in `accounts.api_key`, uses `sk-<custom>-<16|32 hex>`, and is only checked by console middleware.
- API Tokens are 32-character lowercase hex secrets hashed in `account_tokens`; only their prefix and metadata are returned after creation.
- Standard and donation reward Tokens share Bearer transport but are separated with `token_kind` and scope checks.
- API Token minute/day enforcement uses Redis and fails closed when a configured limit cannot be checked.

The API deletes legacy primary Token rows and settings during idempotent schema initialization. New API Tokens are always explicit owner-created records.

## Mail Ingestion

Postfix synchronizes active domains through `/internal/domains.txt`, then delivers messages to the bounded Go LMTP ingress. Workers parse a bounded body, create a mailbox on first delivery when needed, store the email, and update `mailbox_state` with the latest code/link projection in PostgreSQL.

`mailbox_state` keeps lookup endpoints from scanning message bodies on every request. LMTP queue capacity, worker count, body limits and timeouts come from environment variables.

## Domain Donation

Public submission creates a pending `domain_donations` record, a hashed claim secret and either a new donation reward Token or a link to an existing one. MX plus `_far-mail-donate` TXT verification activates both the domain and its per-domain grant. The verifier accepts only the current FAR Mail TXT label. A failed recheck removes only that domain's grant; token-level manual adjustments remain in `donation_reward_events`.

See [domain-donation-rewards.md](domain-donation-rewards.md) for the API and state model.

## Operations Data

Requests authenticated through `/api/v1` enqueue a compact event after the response:

```text
token id, route template, method, status, latency, request id, timestamp
```

The recorder has a 4096-event bounded queue, writes batches of up to 256 once per second, and never blocks the API request path. It does not store credentials, query strings, bodies or responses. `api_request_events` retains 14 days; hourly cleanup removes older rows.

`GET /console/v1/operations/api-usage` returns totals, errors, average/P95 latency, hourly buckets, top routes and recent errors. `GET /console/v1/operations/maintenance/preview` estimates expired/empty mailboxes and old email rows/bytes before the owner runs cleanup. The frontend diagnostic snapshot combines already-authorized system, ingress, domain and usage summaries and copies redacted JSON locally.

## Core Tables

| Table | Purpose |
|---|---|
| `accounts` | single owner record and Admin Key |
| `account_tokens` | standard and donation API Token hashes/limits |
| `domains` | active/pending receive domains |
| `domain_donations` | public donation claims and per-domain grants |
| `donation_reward_events` | reward adjustment ledger |
| `mailboxes` | mailbox lifecycle and creating Token reference |
| `emails` | bounded message content and parsed signals |
| `mailbox_state` | latest code/link projection |
| `api_request_daily` | API Token daily counters |
| `api_request_events` | 14-day API observability events |
| `integration_audit_events` | Redacted Cloudflare and integration operation audit trail |
| `app_settings` | owner-configurable deployment and product settings |

Schema creation is idempotent in `store.EnsureAuxiliarySchema`; new deployments also start from `sql/init.sql`.
Existing installations should run `sql/migrate_v16.sql` once after upgrading to
rebuild `mailbox_state` from the authoritative `emails` rows. The runtime also
rejects a dangling `latest_email_id` and falls back to the mailbox email index,
so a partially upgraded node never serves a deleted code or link.
For an existing large database, run `sql/migrate_v14.sql` outside a transaction so the
measured mailbox/email time indexes can be built concurrently. The mailbox-state recency
indexes intentionally remain absent: LMTP updates that projection for every delivered
message, and the extra indexes were measured as write amplification without a current
hot read path.
