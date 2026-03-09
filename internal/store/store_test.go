package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"limesurvey_redirector/internal/models"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestMigrateAddsRouteOwnershipColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	legacy := []string{
		`CREATE TABLE routes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			slug TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			algorithm TEXT NOT NULL,
			fuzzy_threshold INTEGER NOT NULL DEFAULT 0,
			pending_enabled INTEGER NOT NULL DEFAULT 0,
			pending_ttl_seconds INTEGER NOT NULL DEFAULT 1800,
			pending_weight REAL NOT NULL DEFAULT 1.0,
			forward_query_mode TEXT NOT NULL DEFAULT 'all',
			fallback_url TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			stickiness_enabled INTEGER NOT NULL DEFAULT 0,
			stickiness_mode TEXT NOT NULL DEFAULT '',
			stickiness_param TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE instances (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			survey_base_url TEXT NOT NULL,
			remotecontrol_url TEXT NOT NULL,
			rpc_transport TEXT NOT NULL,
			username TEXT NOT NULL,
			secret_ref TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE route_targets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			route_id INTEGER NOT NULL,
			instance_id INTEGER NOT NULL,
			survey_id INTEGER NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			weight INTEGER NOT NULL DEFAULT 1,
			hard_cap INTEGER,
			priority INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
	}
	for _, stmt := range legacy {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("legacy schema exec failed: %v", err)
		}
	}
	_ = db.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open store failed: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}

	rows, err := st.db.Query(`PRAGMA table_info(routes)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info failed: %v", err)
	}
	defer rows.Close()

	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notNull int
		var defaultVal sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultVal, &pk); err != nil {
			t.Fatalf("scan table_info failed: %v", err)
		}
		columns[name] = true
	}
	if !columns["owner_username"] || !columns["owner_role"] {
		t.Fatalf("expected ownership columns, got %+v", columns)
	}
}

func TestMigrateAddsUserSessionVersionColumn(t *testing.T) {
	st := openTestStore(t)
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}

	rows, err := st.db.Query(`PRAGMA table_info(users)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info(users) failed: %v", err)
	}
	defer rows.Close()

	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notNull int
		var defaultVal sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultVal, &pk); err != nil {
			t.Fatalf("scan table_info(users) failed: %v", err)
		}
		columns[name] = true
	}
	if !columns["session_version"] {
		t.Fatalf("expected session_version column, got %+v", columns)
	}
}

func TestListRoutesByOwner(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	instanceID, err := st.CreateInstance(ctx, CreateInstanceInput{
		Name:             "LS6",
		SurveyBaseURL:    "http://127.0.0.1:19080/surveys",
		RemoteControlURL: "http://mock-ls:19080/jsonrpc",
		RPCTransport:     models.RPCTransportJSON,
		Username:         "api",
		SecretRef:        "LS6_RPC_PASSWORD",
		Enabled:          true,
	})
	if err != nil {
		t.Fatalf("CreateInstance failed: %v", err)
	}
	if _, err := st.CreateRoute(ctx, CreateRouteInput{
		Slug:             "alpha-route",
		Name:             "Alpha Route",
		Description:      "owned by alpha",
		OwnerUsername:    "alpha",
		OwnerRole:        "user",
		Algorithm:        "least_completed",
		ForwardQueryMode: models.QueryForwardAll,
		Enabled:          true,
		InstanceID:       instanceID,
		SurveyIDs:        []int64{101},
	}); err != nil {
		t.Fatalf("CreateRoute alpha failed: %v", err)
	}
	if _, err := st.CreateRoute(ctx, CreateRouteInput{
		Slug:             "beta-route",
		Name:             "Beta Route",
		Description:      "owned by beta",
		OwnerUsername:    "beta",
		OwnerRole:        "user",
		Algorithm:        "least_completed",
		ForwardQueryMode: models.QueryForwardAll,
		Enabled:          true,
		InstanceID:       instanceID,
		SurveyIDs:        []int64{102},
	}); err != nil {
		t.Fatalf("CreateRoute beta failed: %v", err)
	}

	routes, err := st.ListRoutesByOwner(ctx, "alpha", "user")
	if err != nil {
		t.Fatalf("ListRoutesByOwner failed: %v", err)
	}
	if len(routes) != 1 || routes[0].Slug != "alpha-route" {
		t.Fatalf("unexpected routes for owner alpha: %+v", routes)
	}
}

func TestCreateRouteStoresTargetWeights(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	instanceID, err := st.CreateInstance(ctx, CreateInstanceInput{
		Name:             "Weighted LS",
		SurveyBaseURL:    "http://127.0.0.1:19080/surveys",
		RemoteControlURL: "http://mock-ls:19080/jsonrpc",
		RPCTransport:     models.RPCTransportJSON,
		Username:         "api",
		SecretRef:        "LS6_RPC_PASSWORD",
		Enabled:          true,
	})
	if err != nil {
		t.Fatalf("CreateInstance failed: %v", err)
	}
	routeID, err := st.CreateRoute(ctx, CreateRouteInput{
		Slug:             "weighted-route",
		Name:             "Weighted Route",
		Description:      "weighted targets",
		OwnerUsername:    "alpha",
		OwnerRole:        "user",
		Algorithm:        "weighted_completed",
		ForwardQueryMode: models.QueryForwardAll,
		Enabled:          true,
		InstanceID:       instanceID,
		Targets: []RouteTargetInput{
			{SurveyID: 101, Weight: 1},
			{SurveyID: 102, Weight: 4},
		},
	})
	if err != nil {
		t.Fatalf("CreateRoute failed: %v", err)
	}

	route, err := st.GetRoute(ctx, routeID)
	if err != nil {
		t.Fatalf("GetRoute failed: %v", err)
	}
	if len(route.Targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(route.Targets))
	}
	if route.Targets[0].Weight != 1 || route.Targets[1].Weight != 4 {
		t.Fatalf("unexpected weights: %+v", route.Targets)
	}
}
