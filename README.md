# LimeSurvey Redirector

LimeSurvey Redirector is a survey traffic router for teams that run multiple cloned LimeSurvey questionnaires and want one stable public link per study.

Instead of sending respondents directly to a specific LimeSurvey survey, you publish one redirect URL. The app decides which target survey should receive the respondent based on configurable routing algorithms and current survey completion counts.

## Purpose

This app is a modern rewrite of the older Kognilab redirector. It keeps the same core workflow:

- an admin configures LimeSurvey instances
- route users create redirect routes for studies
- each route points to one or more target surveys
- the public route URL forwards respondents to the best target survey

It is designed for:

- parallel survey clones that should be balanced automatically
- mixed LimeSurvey environments such as LS3 and LS6
- non-technical study operators who should manage routes without touching server configuration

## Key Features

- Go backend with SQLite storage
- admin role and route-user role
- per-user route ownership
- support for LimeSurvey 3 via XML-RPC and LimeSurvey 6 via JSON-RPC
- configurable routing algorithms, including legacy-style and weighted strategies
- full query-string forwarding to the selected survey URL
- optional fallback URL, pending assignment buffer, and stickiness
- route simulation endpoint and in-app user tutorial

## Documentation

- [USER_TUTORIAL.md](USER_TUTORIAL.md) - short bilingual user guide
- [DESIGN.md](DESIGN.md) - product and technical design
- [IMPLEMENTATION_SPEC.md](IMPLEMENTATION_SPEC.md) - implementation scope
- [E2E_TESTS.md](E2E_TESTS.md) - end-to-end coverage plan

## Environment

Required:

- `ADMIN_USERNAME` default `admin`
- `ADMIN_PASSWORD`
- `SESSION_SECRET` minimum 32 characters

Common optional settings:

- `APP_ADDR` default `:8099`
- `DATABASE_PATH` default `./data/redirector.db`
- `IMAGE_TAG` default `latest`
- `SESSION_SECURE_COOKIE` default `false`, set `true` behind HTTPS
- `PUBLIC_BASE_URL` public URL of the app, used to display full route URLs
- `STATS_TTL_SECONDS` default `10`
- `REQUEST_TIMEOUT_SECONDS` default `15`
- `HOST_BIND` default `127.0.0.1`
- `HOST_PORT` default `18099`
- `LS3_RPC_PASSWORD` optional secret for LS3 instance config
- `LS6_RPC_PASSWORD` optional secret for LS6 instance config

## Local Run

Run directly:

```bash
go run ./cmd/server
```

Or with Docker Compose using a local build override:

```bash
cp .env.example .env
docker compose -f compose.yaml -f compose.dev.yaml up -d --build
```

Local entry points:

- app: `http://127.0.0.1:18099/admin/login`
- health check: `http://127.0.0.1:18099/healthz`

Stop the local stack:

```bash
docker compose -f compose.yaml -f compose.dev.yaml down
```

Remove the SQLite volume too:

```bash
docker compose -f compose.yaml -f compose.dev.yaml down -v
```

## Portainer Deployment

The included [compose.yaml](compose.yaml) is intended to be used directly as a Portainer stack file. It pulls the application image from GHCR:

- `ghcr.io/panbartosz/limesurvey-redirector:${IMAGE_TAG:-latest}`

### 1. Prepare environment values

You need at least:

- `ADMIN_USERNAME`
- `ADMIN_PASSWORD`
- `SESSION_SECRET`

Recommended production values:

- `HOST_BIND=127.0.0.1` if the app is only exposed through a reverse proxy on the same host
- `HOST_BIND=0.0.0.0` if you want the app reachable directly on the host port
- `HOST_PORT=8099` or another free port
- `SESSION_SECURE_COOKIE=true`
- `PUBLIC_BASE_URL=https://your-domain.example`

Optional instance secrets:

- `LS3_RPC_PASSWORD`
- `LS6_RPC_PASSWORD`

### 2. Create the stack in Portainer

Use one of these approaches:

- `Stacks` -> `Add stack` -> paste the contents of `compose.yaml`
- `Stacks` -> `Add stack` -> `Repository` and point Portainer at this GitHub repository

If you deploy from Git:

- repository URL: `https://github.com/PanBartosz/Limesurvey-Redirector.git`
- reference: `refs/heads/main`
- compose path: `compose.yaml`

Portainer will pull the GHCR image referenced by the stack file. It does not need to build the image locally.

### 3. Volumes

The stack creates one named Docker volume automatically:

- `redirector_data`

It stores the SQLite database at `/app/data/redirector.db`.

Back up this volume before upgrades if you need rollback safety.

### 4. First login

After the stack is up:

- open `https://your-domain.example/admin/login` or `http://host:port/admin/login`
- sign in with `ADMIN_USERNAME` and `ADMIN_PASSWORD`
- create LimeSurvey instances first
- then create route users
- then let route users create routes

### 5. Configuring LimeSurvey instances

When you add an instance in the admin UI:

- choose `xmlrpc` for LimeSurvey 3
- choose `jsonrpc` for LimeSurvey 6
- use `LS3_RPC_PASSWORD` or `LS6_RPC_PASSWORD` as the secret env name if you want passwords stored only in Portainer stack variables

### 6. Updating the stack

If Portainer deploys from Git:

- push to `main`
- redeploy the stack from Portainer
- the database volume stays intact

If you enable Portainer auto-update, new pushes can trigger automatic redeploy depending on your Portainer setup.

## CI

GitHub Actions is configured in `.github/workflows/build.yml`.

On pull requests it:

- checks out the repository
- sets up Go from `go.mod`
- runs `go test ./...`
- builds the Docker image from `Dockerfile`

On pushes to `main` it also publishes the Docker image to:

- `ghcr.io/panbartosz/limesurvey-redirector:latest`
