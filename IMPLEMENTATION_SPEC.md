# Implementation Spec

## Confirmed Decisions

This file freezes the product decisions confirmed after the initial design pass.

### Deployment

- Single service instance
- Deployed with Docker and Portainer
- SQLite is acceptable for v1

### Admin Access

- Public redirect endpoint remains public
- Admin area uses very light protection
- v1 implementation: one password-protected admin area with a signed session cookie

### LimeSurvey Compatibility Scope

v1 should support two instance types:

- legacy LS3 configuration using XML-RPC
- modern LS6 configuration using JSON-RPC

The app must support per-instance RPC transport selection so both can coexist.

### Query Parameters

- Forward all incoming query parameters to the final survey URL
- Keep this configurable in storage, but default new routes to `all`

### Stickiness

- Stickiness is not enabled by default
- It is configurable per route via a toggle in the route editor
- Supported modes in the data model:
  - `cookie`
  - `query_param`

### Algorithm Scope

v1 should include both legacy and improved routing algorithms.

Legacy algorithms to port:

- `random`
- `least_completed`
- `least_full`
- `completed_fuzzy`
- `full_fuzzy`

New algorithms to include in the model and UI:

- `weighted_completed`
- `weighted_fuzzy`

The admin UI must explain each algorithm and its intended use case.

## v1 Product Boundaries

### In Scope

- Configure LimeSurvey instances
- Configure redirect routes
- Attach multiple survey IDs to a route
- Select algorithm and parameters per route
- Enable or disable stickiness per route
- Forward all query params
- Resolve a public slug to a target survey URL
- Fetch live summary data from LimeSurvey APIs
- Log redirect decisions
- Expose a simulation endpoint for debugging

### Deferred

- Fine-grained user accounts and roles
- LimeSurvey plugin-based push updates
- Quota-aware routing
- Bulk import UI
- Multi-node deployment

## Data Model Choices

### Route-Level Configuration

Each route stores:

- slug
- name
- description
- algorithm
- fuzzy threshold
- pending buffer settings
- query forwarding mode
- fallback URL
- enabled flag
- stickiness toggle
- stickiness mode
- stickiness parameter key

### Target-Level Configuration

Each target stores:

- instance
- survey ID
- optional display name
- weight
- hard cap
- enabled flag

## Implementation Plan

### Phase A

- project scaffold
- config loading
- SQLite migrations
- password-based admin auth
- instances CRUD
- routes CRUD
- algorithm definitions
- redirect and simulate endpoints

### Phase B

- live LimeSurvey RPC integration
- target status caching
- decision logging
- basic route detail view

### Phase C

- richer admin editing flow
- import helpers for legacy LS3 routes
- more operator diagnostics

## Working Assumptions

- A route is restricted to a single LimeSurvey instance in the initial create form, matching the old app's behavior
- The underlying schema keeps `instance_id` on each target so multi-instance routes can be added later without a migration
- The final survey URL is built from the configured `survey_base_url` plus `/survey_id`
- LS3 instances will use XML-RPC RemoteControl 2
- LS6 instances will use JSON-RPC RemoteControl 2 unless proven otherwise in real integration testing
