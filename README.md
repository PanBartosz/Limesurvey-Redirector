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
- `INSTANCE_CREDENTIALS_KEY` encryption key for stored LimeSurvey instance passwords; if unset, the app falls back to `SESSION_SECRET`
- `SESSION_SECURE_COOKIE` default `false`, set `true` behind HTTPS
- `PUBLIC_BASE_URL` public URL of the app, used to display full route URLs
- `STATS_TTL_SECONDS` default `10`
- `REQUEST_TIMEOUT_SECONDS` default `15`
- `HOST_BIND` default `127.0.0.1`
- `HOST_PORT` default `18099`

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

### 1. Make sure the image exists in GHCR

The Portainer stack pulls a prebuilt image. Before the first deployment:

- push to `main`
- wait for GitHub Actions to finish
- confirm that `ghcr.io/panbartosz/limesurvey-redirector:latest` exists

If the package is private in GHCR:

- add a Portainer registry for `ghcr.io`
- use your GitHub username
- use a GitHub PAT with at least `read:packages`

### 2. Prepare environment values

You need at least:

- `ADMIN_USERNAME`
- `ADMIN_PASSWORD`
- `SESSION_SECRET`
- `INSTANCE_CREDENTIALS_KEY`

Recommended production values:

- `HOST_BIND=127.0.0.1` if the app is only exposed through a reverse proxy on the same host
- `HOST_BIND=0.0.0.0` if you want the app reachable directly on the host port
- `HOST_PORT=8099` or another free port
- `SESSION_SECURE_COOKIE=true`
- `PUBLIC_BASE_URL=https://your-domain.example`

Optional instance secrets:
- none; LimeSurvey instance passwords are entered in the admin UI and stored encrypted in the app database

Paste-ready example for Portainer:

```env
IMAGE_TAG=latest
HOST_BIND=127.0.0.1
HOST_PORT=8099
ADMIN_USERNAME=admin
ADMIN_PASSWORD=replace-with-a-long-random-password
SESSION_SECRET=replace-with-a-32-char-minimum-session-secret
INSTANCE_CREDENTIALS_KEY=replace-with-a-separate-32-char-minimum-encryption-key
SESSION_SECURE_COOKIE=true
PUBLIC_BASE_URL=https://redirector.your-domain.example
STATS_TTL_SECONDS=10
REQUEST_TIMEOUT_SECONDS=15
```

Notes:

- keep `INSTANCE_CREDENTIALS_KEY` stable; changing it later makes stored LimeSurvey passwords unreadable
- if the app is behind Nginx/Caddy/Traefik on the same host, keep `HOST_BIND=127.0.0.1`
- if you want direct access on the host port without a reverse proxy, use `HOST_BIND=0.0.0.0`

### 3. Create the stack in Portainer

Use one of these approaches:

- `Stacks` -> `Add stack` -> paste the contents of `compose.yaml`
- `Stacks` -> `Add stack` -> `Repository` and point Portainer at this GitHub repository

If you deploy from Git:

- repository URL: `https://github.com/PanBartosz/Limesurvey-Redirector.git`
- reference: `refs/heads/main`
- compose path: `compose.yaml`

Portainer will pull the GHCR image referenced by the stack file. It does not need to build the image locally.

Recommended Portainer setup:

- stack name: `limesurvey-redirector`
- deployment method: `Repository`
- repository URL: `https://github.com/PanBartosz/Limesurvey-Redirector.git`
- repository reference: `refs/heads/main`
- compose path: `compose.yaml`

Then add the environment variables above in the stack environment section.

### 4. Volumes

The stack creates one named Docker volume automatically:

- `redirector_data`

It stores the SQLite database at `/app/data/redirector.db`.

Back up this volume before upgrades if you need rollback safety.

What this means operationally:

- deleting and recreating the container does not remove app data
- deleting the stack without deleting volumes keeps the database
- deleting the `redirector_data` volume wipes the application state

### 5. Networking and exposure

The container listens on port `8099` internally.

The stack publishes:

- `${HOST_BIND}:${HOST_PORT}->8099`

Typical setups:

- reverse proxy on the same host:
  - `HOST_BIND=127.0.0.1`
  - `HOST_PORT=8099`
  - proxy `https://redirector.your-domain.example` to `http://127.0.0.1:8099`
- direct host exposure:
  - `HOST_BIND=0.0.0.0`
  - `HOST_PORT=8099`
  - open the firewall for that port if needed

If the public URL is HTTPS, set:

- `SESSION_SECURE_COOKIE=true`
- `PUBLIC_BASE_URL=https://redirector.your-domain.example`

### 6. Deploy the stack

In Portainer:

1. open `Stacks`
2. create or edit the `limesurvey-redirector` stack
3. point it at this GitHub repository or paste `compose.yaml`
4. add the environment variables
5. deploy the stack
6. confirm the container is healthy and listening on the expected host port

Quick checks after deploy:

- `http://host:port/healthz` should return `ok`
- the login page should load at `/admin/login`

### 7. First login

After the stack is up:

- open `https://your-domain.example/admin/login` or `http://host:port/admin/login`
- sign in with `ADMIN_USERNAME` and `ADMIN_PASSWORD`
- create LimeSurvey instances first
- then create route users
- then let route users create routes

### 8. Configuring LimeSurvey instances

When you add an instance in the admin UI:

- choose `xmlrpc` for LimeSurvey 3
- choose `jsonrpc` for LimeSurvey 6
- enter the LimeSurvey API username and password directly in the form
- the password is encrypted before it is written to SQLite

### 9. Updating the stack

If Portainer deploys from Git:

- push to `main`
- redeploy the stack from Portainer
- the database volume stays intact

If you enable Portainer auto-update, new pushes can trigger automatic redeploy depending on your Portainer setup.

For a safer rollout, pin a specific image instead of `latest`:

- set `IMAGE_TAG=sha-<commit>`

Then redeploy the stack. This lets you control exactly which GitHub-built image is running.

## CI

GitHub Actions is configured in `.github/workflows/build.yml`.

On pull requests it:

- checks out the repository
- sets up Go from `go.mod`
- runs `go test ./...`
- builds the Docker image from `Dockerfile`

On pushes to `main` it also publishes the Docker image to:

- `ghcr.io/panbartosz/limesurvey-redirector:latest`
