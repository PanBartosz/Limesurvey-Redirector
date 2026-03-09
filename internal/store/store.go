package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"limesurvey_redirector/internal/models"
)

type Store struct {
	db *sql.DB
}

type CreateInstanceInput struct {
	Name              string
	SurveyBaseURL     string
	RemoteControlURL  string
	RPCTransport      models.RPCTransport
	Username          string
	EncryptedPassword string
	Enabled           bool
}

type CreateRouteInput struct {
	Slug              string
	Name              string
	Description       string
	OwnerUsername     string
	OwnerRole         string
	Algorithm         string
	FuzzyThreshold    int
	PendingEnabled    bool
	PendingTTLSeconds int
	PendingWeight     float64
	ForwardQueryMode  models.QueryForwardMode
	FallbackURL       string
	Enabled           bool
	StickinessEnabled bool
	StickinessMode    models.StickinessMode
	StickinessParam   string
	InstanceID        int64
	Targets           []RouteTargetInput
	SurveyIDs         []int64
}

type RouteTargetInput struct {
	SurveyID int64
	Weight   int
}

type CreateUserInput struct {
	Username     string
	PasswordHash string
	Role         string
	Enabled      bool
}

type AuthUser struct {
	User         models.User
	PasswordHash string
}

type RouteListItem struct {
	ID            int64
	Slug          string
	Name          string
	OwnerUsername string
	OwnerRole     string
	Algorithm     string
	Enabled       bool
	TargetCount   int
	UpdatedAt     time.Time
}

func Open(databasePath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Migrate(ctx context.Context) error {
	statements := []string{
		`PRAGMA foreign_keys = ON;`,
		`CREATE TABLE IF NOT EXISTS instances (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			survey_base_url TEXT NOT NULL,
			remotecontrol_url TEXT NOT NULL,
			rpc_transport TEXT NOT NULL,
			username TEXT NOT NULL,
			encrypted_password TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE COLLATE NOCASE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'user',
			enabled INTEGER NOT NULL DEFAULT 1,
			session_version INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS routes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			slug TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			owner_username TEXT NOT NULL DEFAULT '',
			owner_role TEXT NOT NULL DEFAULT '',
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
		`CREATE TABLE IF NOT EXISTS route_targets (
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
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(route_id, instance_id, survey_id),
			FOREIGN KEY(route_id) REFERENCES routes(id) ON DELETE CASCADE,
			FOREIGN KEY(instance_id) REFERENCES instances(id) ON DELETE RESTRICT
		);`,
		`CREATE TABLE IF NOT EXISTS target_stats (
			route_target_id INTEGER PRIMARY KEY,
			completed_responses INTEGER NOT NULL DEFAULT 0,
			incomplete_responses INTEGER NOT NULL DEFAULT 0,
			full_responses INTEGER NOT NULL DEFAULT 0,
			survey_active INTEGER NOT NULL DEFAULT 0,
			fetch_error TEXT NOT NULL DEFAULT '',
			fetched_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(route_target_id) REFERENCES route_targets(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS redirect_decisions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			route_id INTEGER NOT NULL,
			route_target_id INTEGER,
			request_id TEXT NOT NULL,
			decision_mode TEXT NOT NULL,
			request_query TEXT NOT NULL DEFAULT '',
			forwarded_query TEXT NOT NULL DEFAULT '',
			candidate_snapshot TEXT NOT NULL DEFAULT '',
			chosen_score REAL,
			status TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(route_id) REFERENCES routes(id) ON DELETE CASCADE,
			FOREIGN KEY(route_target_id) REFERENCES route_targets(id) ON DELETE SET NULL
		);`,
		`CREATE TABLE IF NOT EXISTS pending_assignments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			route_id INTEGER NOT NULL,
			route_target_id INTEGER NOT NULL,
			assignment_key TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(route_id) REFERENCES routes(id) ON DELETE CASCADE,
			FOREIGN KEY(route_target_id) REFERENCES route_targets(id) ON DELETE CASCADE
		);`,
	}

	for _, stmt := range statements {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}

	if err := s.ensureColumnExists(ctx, "routes", "owner_username", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumnExists(ctx, "routes", "owner_role", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumnExists(ctx, "users", "session_version", "INTEGER NOT NULL DEFAULT 1"); err != nil {
		return err
	}
	if err := s.ensureColumnExists(ctx, "instances", "encrypted_password", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}

	return nil
}

func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *Store) ListInstances(ctx context.Context) ([]models.Instance, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, survey_base_url, remotecontrol_url, rpc_transport, username, encrypted_password, enabled, created_at, updated_at
		FROM instances
		ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	instances := []models.Instance{}
	for rows.Next() {
		item, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		instances = append(instances, item)
	}
	return instances, rows.Err()
}

func (s *Store) ListEnabledInstances(ctx context.Context) ([]models.Instance, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, survey_base_url, remotecontrol_url, rpc_transport, username, encrypted_password, enabled, created_at, updated_at
		FROM instances
		WHERE enabled = 1
		ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	instances := []models.Instance{}
	for rows.Next() {
		item, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		instances = append(instances, item)
	}
	return instances, rows.Err()
}

func (s *Store) GetInstance(ctx context.Context, id int64) (models.Instance, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, survey_base_url, remotecontrol_url, rpc_transport, username, encrypted_password, enabled, created_at, updated_at
		FROM instances WHERE id = ?`, id)
	return scanInstance(row)
}

func (s *Store) CreateInstance(ctx context.Context, input CreateInstanceInput) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO instances (name, survey_base_url, remotecontrol_url, rpc_transport, username, encrypted_password, enabled)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		strings.TrimSpace(input.Name),
		strings.TrimSpace(input.SurveyBaseURL),
		strings.TrimSpace(input.RemoteControlURL),
		string(input.RPCTransport),
		strings.TrimSpace(input.Username),
		strings.TrimSpace(input.EncryptedPassword),
		boolToInt(input.Enabled),
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *Store) CreateUser(ctx context.Context, input CreateUserInput) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO users (username, password_hash, role, enabled)
		VALUES (?, ?, ?, ?)`,
		strings.ToLower(strings.TrimSpace(input.Username)),
		input.PasswordHash,
		strings.TrimSpace(input.Role),
		boolToInt(input.Enabled),
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *Store) GetUser(ctx context.Context, id int64) (models.User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, username, role, enabled, session_version, created_at, updated_at
		FROM users
		WHERE id = ?`, id)
	return scanUser(row)
}

func (s *Store) ListUsers(ctx context.Context) ([]models.User, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, username, role, enabled, session_version, created_at, updated_at
		FROM users
		ORDER BY username ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := []models.User{}
	for rows.Next() {
		item, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, item)
	}
	return users, rows.Err()
}

func (s *Store) UpdateUserEnabled(ctx context.Context, id int64, enabled bool) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE users
		SET enabled = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		boolToInt(enabled),
		id,
	)
	if err != nil {
		return err
	}
	return ensureRowsAffected(result)
}

func (s *Store) UpdateUserPasswordHash(ctx context.Context, id int64, passwordHash string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE users
		SET password_hash = ?, session_version = session_version + 1, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		passwordHash,
		id,
	)
	if err != nil {
		return err
	}
	return ensureRowsAffected(result)
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return ensureRowsAffected(result)
}

func (s *Store) CountRoutesByOwner(ctx context.Context, ownerUsername, ownerRole string) (int, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM routes
		WHERE lower(owner_username) = lower(?) AND owner_role = ?`,
		strings.TrimSpace(ownerUsername),
		strings.TrimSpace(ownerRole),
	)
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) GetAuthUserByUsername(ctx context.Context, username string) (AuthUser, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, role, enabled, session_version, created_at, updated_at
		FROM users
		WHERE username = ?`, strings.ToLower(strings.TrimSpace(username)))

	var item AuthUser
	var enabled int
	var createdAt, updatedAt string
	if err := row.Scan(&item.User.ID, &item.User.Username, &item.PasswordHash, &item.User.Role, &enabled, &item.User.SessionVersion, &createdAt, &updatedAt); err != nil {
		return AuthUser{}, err
	}
	item.User.Enabled = enabled == 1
	item.User.CreatedAt = parseDBTime(createdAt)
	item.User.UpdatedAt = parseDBTime(updatedAt)
	return item, nil
}

func (s *Store) ListRoutes(ctx context.Context) ([]RouteListItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id, r.slug, r.name, r.owner_username, r.owner_role, r.algorithm, r.enabled, COUNT(rt.id) AS target_count, r.updated_at
		FROM routes r
		LEFT JOIN route_targets rt ON rt.route_id = r.id
		GROUP BY r.id, r.slug, r.name, r.owner_username, r.owner_role, r.algorithm, r.enabled, r.updated_at
		ORDER BY r.updated_at DESC, r.name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []RouteListItem{}
	for rows.Next() {
		var item RouteListItem
		var enabled int
		var updatedAt string
		if err := rows.Scan(&item.ID, &item.Slug, &item.Name, &item.OwnerUsername, &item.OwnerRole, &item.Algorithm, &enabled, &item.TargetCount, &updatedAt); err != nil {
			return nil, err
		}
		item.Enabled = enabled == 1
		item.UpdatedAt = parseDBTime(updatedAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) RouteSlugTaken(ctx context.Context, slug string, excludeID int64) (bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM routes
		WHERE lower(slug) = lower(?) AND id <> ?`,
		strings.TrimSpace(slug),
		excludeID,
	)
	var count int
	if err := row.Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Store) ListRoutesByOwner(ctx context.Context, ownerUsername, ownerRole string) ([]RouteListItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id, r.slug, r.name, r.owner_username, r.owner_role, r.algorithm, r.enabled, COUNT(rt.id) AS target_count, r.updated_at
		FROM routes r
		LEFT JOIN route_targets rt ON rt.route_id = r.id
		WHERE lower(r.owner_username) = lower(?) AND r.owner_role = ?
		GROUP BY r.id, r.slug, r.name, r.owner_username, r.owner_role, r.algorithm, r.enabled, r.updated_at
		ORDER BY r.updated_at DESC, r.name ASC`,
		strings.TrimSpace(ownerUsername),
		strings.TrimSpace(ownerRole),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []RouteListItem{}
	for rows.Next() {
		var item RouteListItem
		var enabled int
		var updatedAt string
		if err := rows.Scan(&item.ID, &item.Slug, &item.Name, &item.OwnerUsername, &item.OwnerRole, &item.Algorithm, &enabled, &item.TargetCount, &updatedAt); err != nil {
			return nil, err
		}
		item.Enabled = enabled == 1
		item.UpdatedAt = parseDBTime(updatedAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) CreateRoute(ctx context.Context, input CreateRouteInput) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		INSERT INTO routes (
			slug, name, description, owner_username, owner_role, algorithm, fuzzy_threshold,
			pending_enabled, pending_ttl_seconds, pending_weight,
			forward_query_mode, fallback_url, enabled,
			stickiness_enabled, stickiness_mode, stickiness_param
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		strings.TrimSpace(input.Slug),
		strings.TrimSpace(input.Name),
		strings.TrimSpace(input.Description),
		strings.TrimSpace(input.OwnerUsername),
		strings.TrimSpace(input.OwnerRole),
		strings.TrimSpace(input.Algorithm),
		input.FuzzyThreshold,
		boolToInt(input.PendingEnabled),
		input.PendingTTLSeconds,
		input.PendingWeight,
		string(input.ForwardQueryMode),
		strings.TrimSpace(input.FallbackURL),
		boolToInt(input.Enabled),
		boolToInt(input.StickinessEnabled),
		string(input.StickinessMode),
		strings.TrimSpace(input.StickinessParam),
	)
	if err != nil {
		return 0, err
	}
	routeID, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}

	seen := map[int64]struct{}{}
	for _, target := range normalizeRouteTargets(input) {
		if _, ok := seen[target.SurveyID]; ok {
			continue
		}
		seen[target.SurveyID] = struct{}{}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO route_targets (route_id, instance_id, survey_id, display_name, weight, enabled)
			VALUES (?, ?, ?, ?, ?, 1)`, routeID, input.InstanceID, target.SurveyID, fmt.Sprintf("Survey %d", target.SurveyID), maxInt(target.Weight, 1)); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return routeID, nil
}

func (s *Store) UpdateRoute(ctx context.Context, routeID int64, input CreateRouteInput) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		UPDATE routes
		SET
			slug = ?,
			name = ?,
			description = ?,
			owner_username = ?,
			owner_role = ?,
			algorithm = ?,
			fuzzy_threshold = ?,
			pending_enabled = ?,
			pending_ttl_seconds = ?,
			pending_weight = ?,
			forward_query_mode = ?,
			fallback_url = ?,
			enabled = ?,
			stickiness_enabled = ?,
			stickiness_mode = ?,
			stickiness_param = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		strings.TrimSpace(input.Slug),
		strings.TrimSpace(input.Name),
		strings.TrimSpace(input.Description),
		strings.TrimSpace(input.OwnerUsername),
		strings.TrimSpace(input.OwnerRole),
		strings.TrimSpace(input.Algorithm),
		input.FuzzyThreshold,
		boolToInt(input.PendingEnabled),
		input.PendingTTLSeconds,
		input.PendingWeight,
		string(input.ForwardQueryMode),
		strings.TrimSpace(input.FallbackURL),
		boolToInt(input.Enabled),
		boolToInt(input.StickinessEnabled),
		string(input.StickinessMode),
		strings.TrimSpace(input.StickinessParam),
		routeID,
	)
	if err != nil {
		return err
	}
	if err := ensureRowsAffected(result); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM route_targets WHERE route_id = ?`, routeID); err != nil {
		return err
	}

	seen := map[int64]struct{}{}
	for _, target := range normalizeRouteTargets(input) {
		if _, ok := seen[target.SurveyID]; ok {
			continue
		}
		seen[target.SurveyID] = struct{}{}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO route_targets (route_id, instance_id, survey_id, display_name, weight, enabled)
			VALUES (?, ?, ?, ?, ?, 1)`,
			routeID,
			input.InstanceID,
			target.SurveyID,
			fmt.Sprintf("Survey %d", target.SurveyID),
			maxInt(target.Weight, 1),
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) DeleteRoute(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM routes WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return ensureRowsAffected(result)
}

func (s *Store) GetRoute(ctx context.Context, id int64) (models.Route, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, slug, name, description, owner_username, owner_role, algorithm, fuzzy_threshold,
			pending_enabled, pending_ttl_seconds, pending_weight,
			forward_query_mode, fallback_url, enabled,
			stickiness_enabled, stickiness_mode, stickiness_param,
			created_at, updated_at
		FROM routes WHERE id = ?`, id)
	return s.scanRoute(ctx, row)
}

func (s *Store) GetRouteBySlug(ctx context.Context, slug string) (models.Route, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, slug, name, description, owner_username, owner_role, algorithm, fuzzy_threshold,
			pending_enabled, pending_ttl_seconds, pending_weight,
			forward_query_mode, fallback_url, enabled,
			stickiness_enabled, stickiness_mode, stickiness_param,
			created_at, updated_at
		FROM routes WHERE slug = ?`, slug)
	return s.scanRoute(ctx, row)
}

func (s *Store) scanRoute(ctx context.Context, row *sql.Row) (models.Route, error) {
	var route models.Route
	var pendingEnabled, enabled, stickinessEnabled int
	var createdAt, updatedAt string
	if err := row.Scan(
		&route.ID,
		&route.Slug,
		&route.Name,
		&route.Description,
		&route.OwnerUsername,
		&route.OwnerRole,
		&route.Algorithm,
		&route.FuzzyThreshold,
		&pendingEnabled,
		&route.PendingTTLSeconds,
		&route.PendingWeight,
		&route.ForwardQueryMode,
		&route.FallbackURL,
		&enabled,
		&stickinessEnabled,
		&route.StickinessMode,
		&route.StickinessParam,
		&createdAt,
		&updatedAt,
	); err != nil {
		return models.Route{}, err
	}
	route.PendingEnabled = pendingEnabled == 1
	route.Enabled = enabled == 1
	route.StickinessEnabled = stickinessEnabled == 1
	route.CreatedAt = parseDBTime(createdAt)
	route.UpdatedAt = parseDBTime(updatedAt)

	targets, err := s.listRouteTargets(ctx, route.ID)
	if err != nil {
		return models.Route{}, err
	}
	route.Targets = targets
	return route, nil
}

func (s *Store) listRouteTargets(ctx context.Context, routeID int64) ([]models.RouteTarget, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			rt.id, rt.route_id, rt.instance_id, rt.survey_id, rt.display_name, rt.weight, rt.hard_cap, rt.priority, rt.enabled, rt.created_at, rt.updated_at,
			i.id, i.name, i.survey_base_url, i.remotecontrol_url, i.rpc_transport, i.username, i.encrypted_password, i.enabled, i.created_at, i.updated_at,
			ts.completed_responses, ts.incomplete_responses, ts.full_responses, ts.survey_active, ts.fetch_error, ts.fetched_at
		FROM route_targets rt
		JOIN instances i ON i.id = rt.instance_id
		LEFT JOIN target_stats ts ON ts.route_target_id = rt.id
		WHERE rt.route_id = ?
		ORDER BY rt.priority ASC, rt.survey_id ASC`, routeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	targets := []models.RouteTarget{}
	for rows.Next() {
		var target models.RouteTarget
		var targetEnabled int
		var targetCreatedAt, targetUpdatedAt string
		var hardCap sql.NullInt64

		var instanceEnabled int
		var instanceCreatedAt, instanceUpdatedAt string

		var completed, incomplete, full sql.NullInt64
		var surveyActive sql.NullInt64
		var fetchError sql.NullString
		var fetchedAt sql.NullString

		if err := rows.Scan(
			&target.ID, &target.RouteID, &target.InstanceID, &target.SurveyID, &target.DisplayName, &target.Weight, &hardCap, &target.Priority, &targetEnabled, &targetCreatedAt, &targetUpdatedAt,
			&target.Instance.ID, &target.Instance.Name, &target.Instance.SurveyBaseURL, &target.Instance.RemoteControlURL, &target.Instance.RPCTransport, &target.Instance.Username, &target.Instance.EncryptedPassword, &instanceEnabled, &instanceCreatedAt, &instanceUpdatedAt,
			&completed, &incomplete, &full, &surveyActive, &fetchError, &fetchedAt,
		); err != nil {
			return nil, err
		}

		target.Enabled = targetEnabled == 1
		target.CreatedAt = parseDBTime(targetCreatedAt)
		target.UpdatedAt = parseDBTime(targetUpdatedAt)
		target.Instance.CredentialConfigured = strings.TrimSpace(target.Instance.EncryptedPassword) != ""
		target.Instance.Enabled = instanceEnabled == 1
		target.Instance.CreatedAt = parseDBTime(instanceCreatedAt)
		target.Instance.UpdatedAt = parseDBTime(instanceUpdatedAt)
		if hardCap.Valid {
			value := int(hardCap.Int64)
			target.HardCap = &value
		}
		if completed.Valid || incomplete.Valid || full.Valid || fetchedAt.Valid || fetchError.Valid {
			target.Stats = &models.TargetStats{
				RouteTargetID:       target.ID,
				CompletedResponses:  int(completed.Int64),
				IncompleteResponses: int(incomplete.Int64),
				FullResponses:       int(full.Int64),
				SurveyActive:        surveyActive.Int64 == 1,
				FetchError:          fetchError.String,
				FetchedAt:           parseDBTime(fetchedAt.String),
			}
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func (s *Store) UpsertTargetStats(ctx context.Context, targetID int64, stats models.TargetStats) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO target_stats (
			route_target_id, completed_responses, incomplete_responses, full_responses, survey_active, fetch_error, fetched_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(route_target_id) DO UPDATE SET
			completed_responses = excluded.completed_responses,
			incomplete_responses = excluded.incomplete_responses,
			full_responses = excluded.full_responses,
			survey_active = excluded.survey_active,
			fetch_error = excluded.fetch_error,
			fetched_at = excluded.fetched_at`,
		targetID,
		stats.CompletedResponses,
		stats.IncompleteResponses,
		stats.FullResponses,
		boolToInt(stats.SurveyActive),
		stats.FetchError,
		stats.FetchedAt.UTC().Format(time.RFC3339),
	)
	return err
}

func (s *Store) InsertRedirectDecision(ctx context.Context, decision models.RedirectDecision) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO redirect_decisions (
			route_id, route_target_id, request_id, decision_mode,
			request_query, forwarded_query, candidate_snapshot, chosen_score, status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		decision.RouteID,
		nullableInt64(decision.RouteTargetID),
		decision.RequestID,
		decision.DecisionMode,
		decision.RequestQuery,
		decision.ForwardedQuery,
		decision.CandidateSnapshot,
		nullableFloat64(decision.ChosenScore),
		decision.Status,
	)
	return err
}

func (s *Store) ListRecentDecisions(ctx context.Context, routeID int64, limit int) ([]models.RedirectDecision, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, route_id, route_target_id, request_id, decision_mode, request_query, forwarded_query, candidate_snapshot, chosen_score, status, created_at
		FROM redirect_decisions
		WHERE route_id = ?
		ORDER BY id DESC
		LIMIT ?`, routeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	decisions := []models.RedirectDecision{}
	for rows.Next() {
		var item models.RedirectDecision
		var targetID sql.NullInt64
		var score sql.NullFloat64
		var createdAt string
		if err := rows.Scan(&item.ID, &item.RouteID, &targetID, &item.RequestID, &item.DecisionMode, &item.RequestQuery, &item.ForwardedQuery, &item.CandidateSnapshot, &score, &item.Status, &createdAt); err != nil {
			return nil, err
		}
		if targetID.Valid {
			value := targetID.Int64
			item.RouteTargetID = &value
		}
		if score.Valid {
			value := score.Float64
			item.ChosenScore = &value
		}
		item.CreatedAt = parseDBTime(createdAt)
		decisions = append(decisions, item)
	}
	return decisions, rows.Err()
}

func scanInstance(scanner interface{ Scan(dest ...any) error }) (models.Instance, error) {
	var item models.Instance
	var enabled int
	var createdAt, updatedAt string
	if err := scanner.Scan(&item.ID, &item.Name, &item.SurveyBaseURL, &item.RemoteControlURL, &item.RPCTransport, &item.Username, &item.EncryptedPassword, &enabled, &createdAt, &updatedAt); err != nil {
		return models.Instance{}, err
	}
	item.CredentialConfigured = strings.TrimSpace(item.EncryptedPassword) != ""
	item.Enabled = enabled == 1
	item.CreatedAt = parseDBTime(createdAt)
	item.UpdatedAt = parseDBTime(updatedAt)
	return item, nil
}

func scanUser(scanner interface{ Scan(dest ...any) error }) (models.User, error) {
	var item models.User
	var enabled int
	var createdAt, updatedAt string
	if err := scanner.Scan(&item.ID, &item.Username, &item.Role, &enabled, &item.SessionVersion, &createdAt, &updatedAt); err != nil {
		return models.User{}, err
	}
	item.Enabled = enabled == 1
	item.CreatedAt = parseDBTime(createdAt)
	item.UpdatedAt = parseDBTime(updatedAt)
	return item, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func normalizeRouteTargets(input CreateRouteInput) []RouteTargetInput {
	if len(input.Targets) > 0 {
		targets := make([]RouteTargetInput, 0, len(input.Targets))
		for _, target := range input.Targets {
			if target.SurveyID <= 0 {
				continue
			}
			targets = append(targets, RouteTargetInput{
				SurveyID: target.SurveyID,
				Weight:   maxInt(target.Weight, 1),
			})
		}
		return targets
	}

	targets := make([]RouteTargetInput, 0, len(input.SurveyIDs))
	for _, surveyID := range input.SurveyIDs {
		if surveyID <= 0 {
			continue
		}
		targets = append(targets, RouteTargetInput{SurveyID: surveyID, Weight: 1})
	}
	return targets
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableFloat64(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

func ensureRowsAffected(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) ensureColumnExists(ctx context.Context, tableName, columnName, columnDef string) error {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", tableName))
	if err != nil {
		return fmt.Errorf("pragma table_info(%s): %w", tableName, err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("scan table_info(%s): %w", tableName, err)
		}
		if strings.EqualFold(name, columnName) {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", tableName, columnName, columnDef)); err != nil {
		return fmt.Errorf("add column %s.%s: %w", tableName, columnName, err)
	}
	return nil
}

func parseDBTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	formats := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"}
	for _, format := range formats {
		if parsed, err := time.Parse(format, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}
