# Reverse Proxy Contract

FAR Mail does not require a specific edge proxy. The default Compose stack exposes two independent HTTP services:

| Service | Docker target | Host-only target | Responsibility |
|---|---|---|---|
| `frontend` | `frontend:8080` on `far-mail-edge` | `127.0.0.1:8889` | Static assets, runtime config, and SPA fallback only |
| `api` | `api:8080` on `far-mail-edge` | `127.0.0.1:18081` | HTTP API, Console API, public API, and health |

The edge proxy must route these paths to the Go API:

```text
/health
/console/v1/*
/api/v1/*
/public/v1/*
```

Every other browser path goes to `frontend:8080`. `/internal/*` must never be exposed. The frontend server independently rejects API and obsolete prefixes, so a routing mistake cannot turn an obsolete API URL into a successful SPA response.

For same-origin production deployment, set:

```dotenv
CONSOLE_ORIGIN=https://mail.example.com
PUBLIC_API_ORIGIN=
```

`PUBLIC_API_ORIGIN` is runtime configuration, not a Vite build value. An empty value uses the current browser origin. Local direct access keeps the default `http://127.0.0.1:18081` value.

## Docker Network

The application creates the stable `far-mail-edge` bridge by default. Only `frontend` and `api` join it; PostgreSQL, PgBouncer, Redis, and Postfix remain on the application network.

When the edge proxy is defined in another Compose project, join the existing network:

```yaml
services:
  caddy:
    networks:
      - far-mail-edge

networks:
  far-mail-edge:
    external: true
    name: far-mail-edge
```

Start the FAR Mail stack once so Compose creates the network, or create it explicitly before either stack with `docker network create far-mail-edge`. The name can be changed through `EDGE_NETWORK_NAME`.

The examples below assume the proxy itself is a Docker container attached to `far-mail-edge`. A proxy installed directly on the host should use `127.0.0.1:8889` for frontend and `127.0.0.1:18081` for API instead of Docker service names.

## Caddy

```caddyfile
mail.example.com {
	@far_mail_api path /health /console/v1/* /api/v1/* /public/v1/*

	handle @far_mail_api {
		reverse_proxy api:8080 {
			flush_interval -1
		}
	}

	handle {
		reverse_proxy frontend:8080
	}
}
```

Caddy automatically handles TLS. `flush_interval -1` keeps SSE delivery immediate.

## Nginx

```nginx
server {
    listen 443 ssl;
    server_name mail.example.com;

    resolver 127.0.0.11 valid=10s ipv6=off;
    set $far_mail_api http://api:8080;
    set $far_mail_frontend http://frontend:8080;

    location ~ ^/(console|api)/v1/mailboxes/[0-9a-fA-F-]+/events$ {
        proxy_pass $far_mail_api;
        proxy_http_version 1.1;
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 3600s;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    location /console/v1/ {
        proxy_pass $far_mail_api;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    location /api/v1/ {
        proxy_pass $far_mail_api;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    location /public/v1/ {
        proxy_pass $far_mail_api;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    location = /health {
        proxy_pass $far_mail_api;
        proxy_set_header Host $host;
    }

    location / {
        proxy_pass $far_mail_frontend;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

The TLS certificate directives are deployment-specific and intentionally omitted.

## Traefik

For a file provider, the same contract can be expressed as:

```yaml
http:
  routers:
    far-mail-api:
      rule: Host(`mail.example.com`) && (Path(`/health`) || PathPrefix(`/console/v1`) || PathPrefix(`/api/v1`) || PathPrefix(`/public/v1`))
      priority: 100
      service: far-mail-api
    far-mail-frontend:
      rule: Host(`mail.example.com`)
      service: far-mail-frontend
  services:
    far-mail-api:
      loadBalancer:
        servers:
          - url: http://api:8080
    far-mail-frontend:
      loadBalancer:
        servers:
          - url: http://frontend:8080
```

Attach Traefik to `far-mail-edge` when it runs in Docker. Configure its access-log and buffering settings so the mailbox events route remains a streaming response.

## Serving `dist` Directly

An edge proxy may serve `frontend/dist` directly instead of running the `frontend` container. Build it with `npm ci && npm run build`, preserve SPA fallback to `index.html`, keep `runtime-config.js` uncached, and route the API namespaces before the static-file fallback. In same-origin mode the generated default runtime config is already correct.
