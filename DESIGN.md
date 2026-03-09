# LimeSurvey Redirector Rewrite Design

## Purpose

Build a modern replacement for the old Kognilab survey redirector. The core idea stays the same:

- expose one stable public link per study or study cell
- map that link to a pool of equivalent LimeSurvey surveys
- redirect each incoming respondent to the best target survey
- balance traffic using live completion counts and configurable routing rules

The rewrite should keep the system small and operationally simple, but remove the old app's hardcoded assumptions and make routing observable, configurable, and safe.

## What the Old App Does

The old implementation lives mainly in:

- `/home/bartosz/Dropbox/projekty/kognilab/go_redirector/redirector.go`
- `/home/bartosz/Dropbox/projekty/kognilab/go_redirector/redirections.db`
- `/home/bartosz/Dropbox/projekty/kognilab/scripts/tasks.py`

Recovered behavior:

- An operator creates a short link in a very small HTML form at `/add_redirection`.
- The form stores:
  - a shortcut slug
  - a LimeSurvey instance/version
  - a list of survey IDs
- The app stores the mapping in SQLite in two tables:
  - `shortcuts(shortcut, version)`
  - `surveys(id, shortcut, sid)`
- Public traffic hits `/r/{shortcut}`.
- On each request the app:
  - loads the surveys attached to the shortcut
  - calls LimeSurvey RemoteControl for each survey
  - reads summary stats from `get_summary`
  - chooses a survey with the lowest number of completed responses, with a small fuzzy threshold
  - redirects with HTTP 303 to the chosen survey URL
- It also checks survey activation with `get_survey_properties` when creating a route.

Important details from the old logic:

- the default routing method is effectively `least completed + fuzzy random`
- all credentials and endpoints are hardcoded
- there is no audit log, metrics, or admin authentication
- there is no fallback behavior when all targets are bad
- route metadata is minimal; the algorithm is not configurable per route in storage
- the redirect decision is computed from live stats only, so repeated refreshes can send a user to different surveys

## Problems With the Old Design

The rewrite should explicitly fix these issues:

- Hardcoded admin credentials and API endpoints
- Support for multiple historical LimeSurvey versions mixed directly into runtime logic
- No real admin model or permissions
- No reliable way to inspect why a user was sent to survey A instead of B
- No query-param policy, so source tracking and token forwarding are fragile
- No protection against refresh churn or concurrent bursts
- No health model, no fallback target, and no graceful degradation
- No distinction between public redirect traffic and internal admin traffic
- No deployment story aligned with the current Docker-based LimeSurvey stack

## Product Vision

The new app is a small survey routing service for LimeSurvey installations.

It should let an operator define a "route" like:

- public slug: `memory-study`
- target pool:
  - survey `123456`
  - survey `123457`
  - survey `123458`
- routing algorithm: `balanced_fuzzy`
- optional caps/weights/fallbacks

When a respondent opens `https://example.org/r/memory-study`, the app should:

1. resolve the route
2. load fresh or cached stats for candidate surveys
3. exclude invalid targets
4. score the remaining targets
5. choose one target
6. preserve allowed query params
7. issue a redirect
8. log the decision

## Goals

- Preserve the old short-link-to-survey-pool workflow
- Support modern LimeSurvey instances through the official RemoteControl 2 API
- Keep routing low-latency and safe under moderate concurrency
- Make algorithms configurable per route
- Provide an internal admin UI for operators
- Add auditability, metrics, and health visibility
- Deploy cleanly next to the current LimeSurvey stack

## Non-Goals

- Replacing LimeSurvey itself
- Editing survey content inside this app
- Deep respondent analytics or BI
- Running large ETL or reporting pipelines
- Becoming a generic marketing redirector unrelated to surveys

## Core Concepts

### Instance

A configured LimeSurvey installation:

- base survey URL
- RemoteControl URL
- API transport, preferably JSON-RPC
- credentials
- status and health settings

### Survey Target

A single LimeSurvey survey that can receive respondents.

Stored metadata should include at least:

- instance
- survey ID
- title
- active flag
- start/end dates if available
- optional hard cap
- optional weight
- optional tags such as language, lab, panel, or variant

### Route

A public redirect entry point that points to a pool of targets.

Route configuration should include:

- slug
- human-readable name
- enabled/disabled flag
- default algorithm
- fallback URL or fallback behavior
- query-param forwarding policy
- optional stickiness settings
- optional segment rules

### Decision Log

A record of one redirect decision.

This is essential for debugging and later analysis.

## Primary Use Cases

1. A study has several cloned surveys and traffic should be balanced by completions.
2. Some survey clones should receive more traffic than others because target sample sizes differ.
3. A route should stop sending traffic to surveys that are inactive, unhealthy, or already full.
4. Operators need a simple UI to add a new route without touching code.
5. Researchers need to understand how many respondents each slug sent to each survey.
6. A participant refreshing the route should not bounce unpredictably between equivalent surveys.

## Functional Requirements

### Route Management

- Create, edit, enable, disable, archive, and delete routes
- Attach one or more survey targets to a route
- Reorder targets manually for fallback purposes
- Store algorithm and per-target overrides in the database
- Support draft routes before activation

### Survey Discovery and Validation

- Validate each configured LimeSurvey instance on save
- Discover surveys from LimeSurvey using `list_surveys`
- Validate target details with `get_survey_properties`
- Fetch summary counts with `get_summary`
- Optionally inspect quota metadata with `list_quotas` and `get_quota_properties`

### Redirect Execution

- Resolve route by slug
- Reject disabled or unknown slugs with a controlled response
- Evaluate candidate surveys
- Choose the best eligible target
- Redirect with HTTP 302 or 303, configurable globally
- Forward allowlisted query parameters
- Optionally append internal tracking params like route slug and decision ID

### Health and Fallbacks

- Mark a target ineligible when:
  - survey is inactive
  - survey is past end date
  - instance API is unavailable beyond tolerance
  - hard cap is reached
  - quota-aware mode says the target is closed
- If no target is eligible, use one of:
  - fallback URL
  - maintenance page
  - `503 Service Unavailable`

### Observability

- Log every redirect decision
- Show current route health in the admin UI
- Export metrics for:
  - requests
  - decisions per route
  - decisions per target
  - API latency
  - cache hit ratio
  - ineligible target reasons

### Operator Tools

- Route simulation screen that shows:
  - current candidate targets
  - raw stats
  - computed scores
  - chosen target
- Dry-run endpoint that returns JSON instead of redirecting
- Manual sync or refresh button per route and per instance

## Recommended Routing Model

The old app had several discrete algorithms. The rewrite should keep that behavior available, but implement routing through a scoring engine.

Each target gets:

- eligibility: true or false
- a score: lower is better
- a tie-break rule

Recommended built-in strategies:

### `least_completed`

Send traffic to the eligible target with the lowest `completed_responses`.

This is the direct replacement for the old core behavior.

### `least_full`

Send traffic to the target with the lowest `full_responses`.

Useful only if this metric proves meaningful in your LimeSurvey setup.

### `balanced_fuzzy`

Find the lowest score, then randomly choose among targets within a configured threshold.

This is the best default because it:

- preserves balancing pressure
- avoids everyone hitting exactly one survey during ties
- matches the old design closely

### `weighted_balanced`

Use normalized load:

`score = completed_responses / weight`

This is the preferred modern algorithm when survey clones have different sample targets.

### `capacity_aware`

Ignore targets at or above their configured cap, then apply balanced routing to the rest.

### `sticky_balanced`

If the respondent already has an assignment cookie and the target is still eligible, reuse it. Otherwise compute a new target.

This protects against refresh churn and partially completed journeys.

## Smart Improvements Over the Old App

The rewrite should improve the old logic in the following ways:

### 1. Local Pending Assignment Buffer

Completions in LimeSurvey update only after respondents finish. During bursts, pure `completed_responses` balancing can oversend to one target.

The new app should optionally track short-lived local assignments:

- when a redirect happens, create a pending assignment
- expire it after a configurable window, such as 30 minutes
- include pending assignments in the score

Example:

`effective_load = completed + pending * pending_weight`

This is a practical improvement that keeps balancing stable without modifying LimeSurvey internals.

### 2. Weight-Based Sampling

Instead of cloning surveys evenly forever, allow targets to have weights:

- `1, 1, 1` for equal split
- `2, 1` if one clone should collect roughly twice as many completes

### 3. Segment Rules

Allow optional rule-based target filtering before scoring, for example:

- language from `lang`
- panel source from `utm_source`
- lab/site code from `site`

This should be a v1.1 feature, not required on day one.

### 4. Optional Event-Assisted Freshness

Polling `get_summary` is enough for v1. LimeSurvey also exposes plugin events such as `afterSurveyComplete` and `afterSurveyQuota`.

That enables an optional later enhancement:

- a small LimeSurvey plugin notifies the router when a completion or quota change occurs
- the router refreshes cached stats immediately

This should be optional, because the app must still work without custom LimeSurvey plugins.

## Proposed Architecture

### Components

1. Public redirect handler
2. Admin web UI
3. Internal JSON API for the admin UI
4. LimeSurvey client module
5. Stats cache and refresh worker
6. Decision engine
7. Database
8. Metrics/logging subsystem

### Deployment Shape

One dedicated service container, deployed next to the existing LimeSurvey stack:

- `limesurvey`
- `mariadb`
- `limesurvey-redirector`

The redirector should not read LimeSurvey's application database directly. It should integrate through official APIs only.

### Stack Recommendation

Recommended implementation:

- backend: Go
- admin UI: server-rendered HTML with light JavaScript
- database: SQLite for single-node deployment, with a clear path to PostgreSQL if needed later

Why this stack:

- the redirect path is simple and latency-sensitive
- Go matches the old implementation style and deploys as a single binary
- server-rendered admin pages keep the system smaller than a separate SPA
- SQLite is sufficient for one internal service instance and keeps operations simple

If you already know the service must scale horizontally from day one, switch the database choice to PostgreSQL.

## Data Model

Suggested initial schema:

### `instances`

- `id`
- `name`
- `base_url`
- `rpc_url`
- `rpc_transport`
- `username`
- `secret_ref`
- `enabled`
- `timeout_ms`
- `created_at`
- `updated_at`

### `routes`

- `id`
- `slug`
- `name`
- `description`
- `algorithm`
- `fuzzy_threshold`
- `stickiness_mode`
- `stickiness_ttl_seconds`
- `forward_query_mode`
- `fallback_url`
- `enabled`
- `created_at`
- `updated_at`

### `route_targets`

- `id`
- `route_id`
- `instance_id`
- `survey_id`
- `display_name`
- `weight`
- `hard_cap`
- `priority`
- `enabled`
- `created_at`
- `updated_at`

### `target_stats`

Cached latest observed stats per target:

- `id`
- `route_target_id`
- `completed_responses`
- `incomplete_responses`
- `full_responses`
- `quota_state`
- `survey_active`
- `fetched_at`
- `source`

### `redirect_decisions`

- `id`
- `route_id`
- `route_target_id`
- `request_id`
- `decision_mode`
- `request_query`
- `forwarded_query`
- `candidate_snapshot_json`
- `chosen_score`
- `status`
- `created_at`

### `pending_assignments`

- `id`
- `route_id`
- `route_target_id`
- `assignment_key`
- `expires_at`
- `created_at`

## Request Flow

### Public Redirect

1. Receive `GET /r/{slug}`
2. Generate request ID
3. Load route and eligible targets
4. Load cached stats; refresh if stale
5. Apply filtering:
   - route enabled
   - target enabled
   - instance healthy
   - survey active
   - not capped
6. Apply stickiness if configured
7. Compute scores
8. Select target
9. Save decision log
10. Create pending assignment if enabled
11. Redirect

### Admin Sync

1. Operator opens route
2. App loads current cached stats
3. Operator can trigger refresh
4. App fetches fresh summary and properties from LimeSurvey
5. UI shows decision preview

## API and UI Surface

### Public Endpoints

- `GET /r/{slug}`: redirect
- `GET /healthz`: service health
- `GET /readyz`: readiness

### Internal/Admin Endpoints

- `GET /admin/routes`
- `GET /admin/routes/{id}`
- `POST /admin/routes`
- `POST /admin/routes/{id}/refresh`
- `GET /api/routes/{id}/simulate`
- `GET /api/routes/{id}/decisions`
- `GET /api/instances/{id}/surveys`

### Admin Pages

- login or protected entry point
- routes list
- route editor
- route simulation/debug page
- instances page
- target health page
- decision log page

The route editor should be optimized for the real workflow:

- pick instance
- search survey IDs/titles
- add targets
- choose algorithm
- set fallback and query rules
- preview decision
- activate route

## LimeSurvey Integration Strategy

### Required API Methods

The rewrite should rely on official RemoteControl 2 methods:

- `get_session_key`
- `release_session_key`
- `list_surveys`
- `get_survey_properties`
- `get_summary`
- optionally `list_quotas`
- optionally `get_quota_properties`

### Transport Choice

Use JSON-RPC unless there is a hard compatibility reason not to. The current LimeSurvey manual recommends JSON-RPC over XML-RPC.

### Session Handling

- keep one cached session key per instance
- renew on auth failure
- isolate failures per instance
- never hardcode credentials in source code

### Cache Policy

Recommended defaults:

- route stats TTL: 5 to 15 seconds
- survey properties TTL: 60 seconds
- instance session TTL: based on actual API behavior, with forced refresh on auth errors

This keeps redirects fast while still reflecting new completions quickly.

## Query Parameter Policy

The app should not blindly forward every query parameter. It should support:

- `preserve_none`
- `preserve_all`
- `preserve_allowlist`

Recommended default: `preserve_allowlist`

Common allowlisted params:

- `token`
- `lang`
- `source`
- `utm_source`
- `utm_medium`
- `utm_campaign`

Optional app-added params:

- `lsr_route`
- `lsr_target`
- `lsr_decision`

## Security Requirements

- Store secrets outside source control
- Keep admin UI non-public by default
- Separate public redirect traffic from admin access
- Use CSRF protection for admin forms
- Rate-limit public redirect endpoints
- Sanitize and validate slugs and forwarded params
- Avoid open-redirect behavior by permitting only configured target URLs
- Log auth events and config changes

Recommended deployment posture:

- public route endpoints exposed through the reverse proxy
- admin endpoints restricted by reverse-proxy auth, VPN, or IP allowlist

## Reliability and Failure Handling

- If LimeSurvey is slow, use recent cached stats for a short grace period
- If an instance is down and another eligible target exists, skip the bad target
- If every target is bad, fail to fallback URL or a controlled error page
- Avoid process crashes on per-target API errors
- Use circuit-breaker style temporary suppression for repeatedly failing instances

## Migration From the Old App

### Inputs

- old SQLite mappings from `redirections.db`
- known LimeSurvey instances
- any current short links still in use

### Migration Plan

1. Export old shortcuts and survey mappings
2. Create matching routes in the new app
3. Set algorithm to `balanced_fuzzy` to preserve current behavior
4. Validate each target survey against the current LimeSurvey instance
5. Run side-by-side dry-run testing
6. Switch public traffic to the new service
7. Keep old redirector available briefly as rollback

## Suggested Delivery Phases

### Phase 1: Production-Capable Core

- instance configuration
- route CRUD
- survey discovery
- `least_completed` and `balanced_fuzzy`
- cached stats via RemoteControl 2
- public redirect endpoint
- decision logs
- fallback URL
- basic admin auth

### Phase 2: Operational Improvements

- pending assignment buffer
- weighted routing
- dry-run/simulation UI
- metrics dashboard
- route duplication/import tools

### Phase 3: Advanced Smart Routing

- quota-aware routing
- segment rules
- event-assisted freshness via LimeSurvey plugin
- sticky assignments with better respondent identity options

## Open Questions

- Will one public route ever need to target surveys across multiple LimeSurvey instances, or should that be disallowed at first?
- Do you want respondent stickiness only by cookie, or also by token/query parameter?
- Should the service support only internal operator auth, or do you want real user accounts and roles?
- Is single-node deployment enough, or should we design for multiple app replicas immediately?
- Do you want the first implementation to stay very small, or should weighted and quota-aware routing ship in v1?

## Recommended v1 Decision

To keep the rewrite focused, I recommend v1 ship with:

- one service instance
- Go backend with server-rendered admin UI
- SQLite
- JSON-RPC integration with LimeSurvey RemoteControl 2
- route CRUD
- survey discovery and validation
- `least_completed`
- `balanced_fuzzy`
- fallback URL
- query allowlist forwarding
- decision logs
- basic metrics

That delivers a clear improvement over the old tool without overbuilding the first release.

## References

Old implementation:

- `/home/bartosz/Dropbox/projekty/kognilab/go_redirector/redirector.go`
- `/home/bartosz/Dropbox/projekty/kognilab/go_redirector/redirections.db`
- `/home/bartosz/Dropbox/projekty/kognilab/scripts/tasks.py`

Current LimeSurvey documentation used to validate the design:

- RemoteControl 2 API: <https://www.limesurvey.org/manual/RemoteControl_2_API>
- RemoteControl API reference: <https://api.limesurvey.org/classes/remotecontrol-handle.html>
- Plugin events: <https://www.limesurvey.org/manual/Plugin_events>
- AfterSurveyComplete event: <https://www.limesurvey.org/manual/AfterSurveyComplete>
