# partner-demo

Demo **multi-tenant partner facade** for [Texas-hold-em](https://github.com/AdrianLYQ1216/Texas-hold-em).

Partners call this small HTTP service; it mints Partner JWTs, exchanges them against the game gateway, and wraps ledger credit/debit. Launch modes **A** (client-side exchange) and **B** (server exchange → session in URL, sandbox only).

## One command

```bash
cp .env.example .env   # edit secrets to match Texas env
go run .
```

Listens on `LISTEN_ADDR` (default `:18088` — avoids Texas local `wallet` on `:8088`).

Requires a reachable Texas gateway (`GAME_API_BASE`, default `http://127.0.0.1:4000`) with matching `PARTNER_*_SECRET` and `SERVICE_API_KEY`.

## Docker (alongside local Texas)

Texas compose only runs infra (MySQL/Redis/etcd/RabbitMQ); gateway/web stay on the host. The facade container reaches them via `host.docker.internal`.

```bash
cp .env.example .env   # align PARTNER_*_SECRET + SERVICE_API_KEY with Texas
docker compose up -d --build
```

- Published port: **18088**
- Health: `GET http://127.0.0.1:18088/healthz`
- Optional join Texas network:  
  `docker compose -f docker-compose.yml -f docker-compose.texas-net.yml up -d --build`

From the Texas repo (sibling `../partner-demo`):

```bash
docker compose -f docker-compose.yml -f docker-compose.partner-demo.yml up -d --build
```

Override in-container gateway/web with `DOCKER_GAME_API_BASE` / `DOCKER_WEB_BASE` (do not rely on `.env` `GAME_API_BASE=http://127.0.0.1:…` inside Docker).

## Env

| Variable | Purpose |
|----------|---------|
| `LISTEN_ADDR` | Bind address (default `:18088`) |
| `GAME_API_BASE` | Texas gateway, e.g. `http://127.0.0.1:4000` |
| `WEB_BASE` | Texas web origin for launch URLs, e.g. `http://127.0.0.1:5173` |
| `TENANT_<ID>_DEMO_KEY` | Demo facade login key for tenant |
| `PARTNER_<ID>_SECRET` | HS256 secret (must match Texas) |
| `SERVICE_API_KEY` | Ledger `X-API-Key` (or `SERVICE_API_KEY_<ID>`) |
| `TENANTS_JSON` | Optional JSON array of `{id,demo_key,partner_secret,service_api_key}` |
| `DEMO_TOKEN_TTL` | Facade Bearer TTL (default `1h`) |
| `PARTNER_JWT_TTL` | Minted Partner JWT TTL (default `5m`) |

See `.env.example`.

## API (6 endpoints)

All money / user / launch routes require `Authorization: Bearer <access_token>` from `/v1/auth`. The token is **scoped to that tenant** — it cannot act on another `tenant_id`.

### 1. Auth — `POST /v1/auth`

```bash
curl -sS -X POST http://127.0.0.1:18088/v1/auth \
  -H 'Content-Type: application/json' \
  -d '{"tenant_id":"acme","api_key":"demo-acme-key"}'
# → { "access_token":"…", "expires_in":3600, "token_type":"Bearer", "tenant_id":"acme" }
```

### 2. Create / upsert user — `POST /v1/users`

Mints Partner JWT → `POST $GAME_API_BASE/api/v1/partner/exchange` → stores `external_uid` → `platform_uid`.

```bash
TOKEN=…   # from /v1/auth
curl -sS -X POST http://127.0.0.1:18088/v1/users \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"external_uid":"player-1"}'
```

### 3. Credit (bring-in) — `POST /v1/users/:external_uid/credit`

```bash
curl -sS -X POST http://127.0.0.1:18088/v1/users/player-1/credit \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"amount":10000,"idempotency_key":"acme:topup:player-1:1"}'
```

### 4. Debit (bring-out) — `POST /v1/users/:external_uid/debit`

```bash
curl -sS -X POST http://127.0.0.1:18088/v1/users/player-1/debit \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"amount":2500,"idempotency_key":"acme:cashout:player-1:1"}'
```

### 5. Launch A (client exchange) — `POST /v1/users/:external_uid/launch`

Returns `{ "url": "$WEB_BASE/partner/enter?token=<partner-jwt>" }`. The Texas page exchanges the token and sets the session.

```bash
curl -sS -X POST http://127.0.0.1:18088/v1/users/player-1/launch \
  -H "Authorization: Bearer $TOKEN"
```

### 6. Launch B (server session) — `POST /v1/users/:external_uid/launch/session`

Server exchanges, then returns `{ "url": "$WEB_BASE/partner/enter?session=<session-jwt>" }`.

**Sandbox only** — session JWTs in query strings can leak via Referer, proxy logs, and browser history.

```bash
curl -sS -X POST http://127.0.0.1:18088/v1/users/player-1/launch/session \
  -H "Authorization: Bearer $TOKEN"
```

## Smoke against local gateway

```bash
# Terminal A — Texas stack with PARTNER_IDS=acme + matching secrets
# Terminal B:
cp .env.example .env && go run .

export F=http://127.0.0.1:18088
TOKEN=$(curl -sS -X POST $F/v1/auth -H 'Content-Type: application/json' \
  -d '{"tenant_id":"acme","api_key":"demo-acme-key"}' | python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])')
curl -sS -X POST $F/v1/users -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"external_uid":"demo-smoke-1"}'
curl -sS -X POST $F/v1/users/demo-smoke-1/credit -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"amount":1000,"idempotency_key":"acme:smoke:1"}'
curl -sS -X POST $F/v1/users/demo-smoke-1/launch -H "Authorization: Bearer $TOKEN"
```

If the gateway is down, start Texas first (see Texas `scripts/partner-sandbox/`), then re-run the curls above.

## Health

`GET /healthz` → `{ ok, tenants, game_api, web_base }`.
