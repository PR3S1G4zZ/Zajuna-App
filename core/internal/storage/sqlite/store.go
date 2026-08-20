package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zajuna-app/core/internal/checklist"
	"github.com/zajuna-app/core/internal/coursemaps"
	"github.com/zajuna-app/core/internal/evidence"
	"github.com/zajuna-app/core/internal/jobs"
	"github.com/zajuna-app/core/internal/reports"
	"github.com/zajuna-app/core/internal/scheduler"
	"github.com/zajuna-app/core/internal/zajuna"
	_ "modernc.org/sqlite"
)

const currentSchemaVersion = 12

type Store struct {
	db *sql.DB
}

type FichaRecord struct {
	ID         string
	ExternalID string
	Name       string
	CourseID   string
	Status     string
	SyncedAt   *time.Time
	UpdatedAt  time.Time
}

type ChecklistItemRecord struct {
	ID            int
	FichaID       string
	CategoryCode  string
	CategoryLabel string
	ItemCode      string
	Description   string
	GroupName     string
	MaxEvidences  int
	Status        string
	EvidenceCount int
	Evidences     []evidence.Record
	Position      int
	UpdatedAt     time.Time
}

type ChecklistItemEventRecord struct {
	ID         int64
	FichaID    string
	ItemCode   string
	FromStatus string
	ToStatus   string
	Source     string
	Note       string
	JobID      string
	CreatedAt  time.Time
}

type ChecklistItemDetail struct {
	Item   ChecklistItemRecord
	Events []ChecklistItemEventRecord
}

// NotificationRecord is a local, non-sensitive user-facing event. It stores
// only a safe job reference and error code; worker messages can contain data
// from an external page and are intentionally not copied here.
type NotificationRecord struct {
	ID        string
	Kind      string
	Title     string
	Message   string
	JobID     string
	ReadAt    *time.Time
	CreatedAt time.Time
}

type ChecklistCategoryProgress struct {
	Code       string
	Label      string
	Total      int
	Yes        int
	No         int
	Pending    int
	Percentage int
}

type ChecklistSummary struct {
	Total      int
	Yes        int
	No         int
	Pending    int
	Percentage int
}

type ChecklistDashboard struct {
	Ficha      FichaRecord
	ActiveID   string
	Categories []ChecklistCategoryProgress
	Items      []ChecklistItemRecord
	Summary    ChecklistSummary
}

func Open(dataDir string) (*Store, error) {
	if dataDir == "" {
		return nil, errors.New("data directory is required")
	}
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "zajuna.db"))
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	store := &Store{db: db}
	store.db.SetMaxOpenConns(1)
	store.db.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := store.configure(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) configure(ctx context.Context) error {
	statements := []string{
		`PRAGMA foreign_keys = ON`,
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA journal_mode = WAL`,
		`PRAGMA synchronous = NORMAL`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure sqlite: %w", err)
		}
	}
	return nil
}

func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("create schema migrations table: %w", err)
	}

	var version int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version >= currentSchemaVersion {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer tx.Rollback()
	if version < 1 {
		if err := applyV1(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(1, ?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("record schema version: %w", err)
		}
	}
	if version < 2 {
		if err := applyV2(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(2, ?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("record schema version: %w", err)
		}
	}
	if version < 3 {
		if err := applyV3(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(3, ?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("record schema version: %w", err)
		}
	}
	if version < 4 {
		if err := applyV4(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(4, ?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("record schema version: %w", err)
		}
	}
	if version < 5 {
		if err := applyV5(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(5, ?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("record schema version: %w", err)
		}
	}
	if version < 6 {
		if err := applyV6(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(6, ?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("record schema version: %w", err)
		}
	}
	if version < 7 {
		if err := applyV7(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(7, ?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("record schema version: %w", err)
		}
	}
	if version < 8 {
		if err := applyV8(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(8, ?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("record schema version: %w", err)
		}
	}
	if version < 9 {
		if err := applyV9(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(9, ?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("record schema version: %w", err)
		}
	}
	if version < 10 {
		if err := applyV10(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(10, ?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("record schema version: %w", err)
		}
	}
	if version < 11 {
		if err := applyV11(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(11, ?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("record schema version: %w", err)
		}
	}
	if version < 12 {
		if err := applyV12(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(12, ?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("record schema version: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}

func applyV2(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS schedules (
			id TEXT PRIMARY KEY,
			worker_type TEXT NOT NULL,
			input_json TEXT NOT NULL DEFAULT '{}',
			interval_seconds INTEGER NOT NULL CHECK(interval_seconds > 0),
			enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0, 1)),
			next_run_at TEXT NOT NULL,
			last_run_at TEXT,
			last_job_id TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_schedules_due ON schedules(enabled, next_run_at)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply schema v2: %w", err)
		}
	}
	return nil
}

func applyV3(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE evidences_v3 (
			id TEXT PRIMARY KEY,
			ficha_id TEXT REFERENCES fichas(id) ON DELETE CASCADE,
			item_code TEXT NOT NULL DEFAULT '',
			slot_number INTEGER NOT NULL DEFAULT 1,
			name TEXT NOT NULL,
			file_path TEXT NOT NULL,
			format TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT 'manual',
			sha256 TEXT NOT NULL,
			metadata_json TEXT NOT NULL DEFAULT '{}',
			captured_at TEXT NOT NULL,
			UNIQUE(ficha_id, item_code, slot_number, sha256)
		)`,
		`INSERT INTO evidences_v3(id, ficha_id, item_code, slot_number, name, file_path, format, source, sha256, metadata_json, captured_at)
		 SELECT id, ficha_id, item_code, slot_number, name, file_path, format, source, sha256, metadata_json, captured_at FROM evidences`,
		`DROP TABLE evidences`,
		`ALTER TABLE evidences_v3 RENAME TO evidences`,
		`CREATE INDEX IF NOT EXISTS idx_evidences_captured_at ON evidences(captured_at DESC)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply schema v3: %w", err)
		}
	}
	return nil
}

func applyV4(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS reports (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			file_path TEXT NOT NULL,
			format TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'completed',
			sha256 TEXT NOT NULL,
			metadata_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_reports_updated_at ON reports(updated_at DESC)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply schema v4: %w", err)
		}
	}
	return nil
}

func applyV5(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS course_capture_maps (
			course_id TEXT PRIMARY KEY,
			map_json TEXT NOT NULL,
			link_count INTEGER NOT NULL DEFAULT 0,
			source TEXT NOT NULL DEFAULT 'discover-local-http',
			discovered_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_course_capture_maps_updated_at ON course_capture_maps(updated_at DESC)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply schema v5: %w", err)
		}
	}
	return nil
}

func applyV6(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS checklist_catalog_categories (
			id TEXT PRIMARY KEY,
			code TEXT NOT NULL UNIQUE,
			label TEXT NOT NULL,
			sort_order INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS checklist_catalog_items (
			id TEXT PRIMARY KEY,
			category_code TEXT NOT NULL REFERENCES checklist_catalog_categories(code),
			item_code TEXT NOT NULL UNIQUE,
			description TEXT NOT NULL,
			max_evidences INTEGER NOT NULL CHECK(max_evidences > 0),
			sort_order INTEGER NOT NULL,
			group_name TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_checklist_items_ficha_position ON checklist_items(ficha_id, position)`,
		`CREATE INDEX IF NOT EXISTS idx_checklist_catalog_items_category ON checklist_catalog_items(category_code, sort_order)`,
		`UPDATE checklist_items SET status = 'PENDIENTE' WHERE status IS NULL OR status = '' OR status = 'pending'`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply schema v6: %w", err)
		}
	}
	if err := seedChecklistCatalog(ctx, tx); err != nil {
		return err
	}
	return nil
}

func applyV7(ctx context.Context, tx *sql.Tx) error {
	// A checklist slot represents one current capture. Older versions included
	// the file hash in the evidence ID, so retries could leave several rows for
	// the same slot. Keep the newest row before the worker switches to a stable
	// slot ID.
	_, err := tx.ExecContext(ctx, `
		DELETE FROM evidences
		WHERE id IN (
			SELECT older.id
			FROM evidences older
			WHERE older.source = 'capture-checklist'
			  AND older.ficha_id IS NOT NULL
			  AND EXISTS (
					SELECT 1
					FROM evidences newer
					WHERE newer.source = older.source
					  AND newer.ficha_id = older.ficha_id
					  AND newer.item_code = older.item_code
					  AND newer.slot_number = older.slot_number
					  AND (newer.captured_at > older.captured_at OR (newer.captured_at = older.captured_at AND newer.id > older.id))
				)
		)
	`)
	if err != nil {
		return fmt.Errorf("apply schema v7: %w", err)
	}
	return nil
}

func applyV8(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS evidence_groups (
			id TEXT PRIMARY KEY,
			ficha_id TEXT NOT NULL REFERENCES fichas(id) ON DELETE CASCADE,
			group_key TEXT NOT NULL,
			title TEXT NOT NULL,
			confidence TEXT NOT NULL DEFAULT 'suggested',
			reason TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(ficha_id, group_key)
		)`,
		`CREATE TABLE IF NOT EXISTS evidence_group_members (
			group_id TEXT NOT NULL REFERENCES evidence_groups(id) ON DELETE CASCADE,
			evidence_id TEXT NOT NULL REFERENCES evidences(id) ON DELETE CASCADE,
			item_code TEXT NOT NULL,
			slot_number INTEGER NOT NULL DEFAULT 1,
			PRIMARY KEY(group_id, evidence_id, item_code, slot_number)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_evidence_groups_ficha ON evidence_groups(ficha_id, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_evidence_group_members_evidence ON evidence_group_members(evidence_id)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply schema v8: %w", err)
		}
	}
	return nil
}

func applyV9(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS ficha_activity_selections (
			ficha_id TEXT NOT NULL REFERENCES fichas(id) ON DELETE CASCADE,
			activity_id TEXT NOT NULL,
			title TEXT NOT NULL,
			url TEXT NOT NULL,
			phase_name TEXT NOT NULL DEFAULT '',
			phase_section INTEGER NOT NULL DEFAULT 0,
			subsection TEXT NOT NULL DEFAULT '',
			technical INTEGER NOT NULL DEFAULT 0 CHECK(technical IN (0, 1)),
			updated_at TEXT NOT NULL,
			PRIMARY KEY(ficha_id, activity_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ficha_activity_selections_ficha ON ficha_activity_selections(ficha_id, updated_at DESC)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply schema v9: %w", err)
		}
	}
	return nil
}

func applyV10(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS checklist_route_reviews (
			ficha_id TEXT NOT NULL REFERENCES fichas(id) ON DELETE CASCADE,
			route_key TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'review' CHECK(status IN ('review', 'confirmed', 'correction')),
			manual_url TEXT NOT NULL DEFAULT '',
			manual_selector TEXT NOT NULL DEFAULT '',
			note TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL,
			PRIMARY KEY(ficha_id, route_key)
		)
	`)
	if err != nil {
		return fmt.Errorf("apply schema v10: %w", err)
	}
	return nil
}

func applyV11(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS checklist_item_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ficha_id TEXT NOT NULL REFERENCES fichas(id) ON DELETE CASCADE,
			item_code TEXT NOT NULL,
			from_status TEXT NOT NULL DEFAULT '',
			to_status TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT 'manual',
			note TEXT NOT NULL DEFAULT '',
			job_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_checklist_item_events_item ON checklist_item_events(ficha_id, item_code, id DESC)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply schema v11: %w", err)
		}
	}
	return nil
}

func applyV12(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS notifications (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			title TEXT NOT NULL,
			message TEXT NOT NULL,
			job_id TEXT NOT NULL DEFAULT '',
			read_at TEXT,
			created_at TEXT NOT NULL,
			UNIQUE(job_id, kind)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_notifications_created ON notifications(created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_notifications_unread ON notifications(read_at, created_at DESC)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply schema v12: %w", err)
		}
	}
	return nil
}

func seedChecklistCatalog(ctx context.Context, tx *sql.Tx) error {
	if err := checklist.ValidateDefinitions(); err != nil {
		return err
	}
	for _, category := range checklist.Categories() {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO checklist_catalog_categories(id, code, label, sort_order)
			VALUES(?, ?, ?, ?)
			ON CONFLICT(code) DO UPDATE SET label = excluded.label, sort_order = excluded.sort_order
		`, category.ID, category.Code, category.Label, category.SortOrder); err != nil {
			return fmt.Errorf("seed checklist category %s: %w", category.Code, err)
		}
	}
	for _, item := range checklist.Items() {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO checklist_catalog_items(id, category_code, item_code, description, max_evidences, sort_order, group_name)
			VALUES(?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(item_code) DO UPDATE SET
				category_code = excluded.category_code,
				description = excluded.description,
				max_evidences = excluded.max_evidences,
				sort_order = excluded.sort_order,
				group_name = excluded.group_name
		`, item.ID, item.CategoryCode, item.ItemCode, item.Description, item.MaxEvidences, item.SortOrder, item.GroupName); err != nil {
			return fmt.Errorf("seed checklist item %s: %w", item.ItemCode, err)
		}
	}
	return nil
}

func applyV1(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS app_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			status TEXT NOT NULL,
			input_json TEXT NOT NULL DEFAULT '{}',
			result_json TEXT,
			progress INTEGER NOT NULL DEFAULT 0 CHECK(progress >= 0 AND progress <= 100),
			stage TEXT NOT NULL DEFAULT '',
			message TEXT NOT NULL DEFAULT '',
			attempt INTEGER NOT NULL DEFAULT 0,
			max_attempts INTEGER NOT NULL DEFAULT 3,
			error_code TEXT NOT NULL DEFAULT '',
			error_message TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			started_at TEXT,
			finished_at TEXT,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_status_updated ON jobs(status, updated_at)`,
		`CREATE TABLE IF NOT EXISTS job_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
			kind TEXT NOT NULL,
			stage TEXT NOT NULL DEFAULT '',
			progress INTEGER NOT NULL DEFAULT 0 CHECK(progress >= 0 AND progress <= 100),
			message TEXT NOT NULL DEFAULT '',
			data_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_job_events_job_id ON job_events(job_id, id)`,
		`CREATE TABLE IF NOT EXISTS fichas (
			id TEXT PRIMARY KEY,
			external_id TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			course_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			metadata_json TEXT NOT NULL DEFAULT '{}',
			synced_at TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS checklist_items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ficha_id TEXT NOT NULL REFERENCES fichas(id) ON DELETE CASCADE,
			item_code TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			position INTEGER NOT NULL DEFAULT 0,
			metadata_json TEXT NOT NULL DEFAULT '{}',
			updated_at TEXT NOT NULL,
			UNIQUE(ficha_id, item_code)
		)`,
		`CREATE TABLE IF NOT EXISTS evidences (
			id TEXT PRIMARY KEY,
			ficha_id TEXT NOT NULL REFERENCES fichas(id) ON DELETE CASCADE,
			item_code TEXT NOT NULL,
			slot_number INTEGER NOT NULL DEFAULT 1,
			name TEXT NOT NULL,
			file_path TEXT NOT NULL,
			format TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT 'manual',
			sha256 TEXT NOT NULL,
			metadata_json TEXT NOT NULL DEFAULT '{}',
			captured_at TEXT NOT NULL,
			UNIQUE(ficha_id, item_code, slot_number, sha256)
		)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply schema v1: %w", err)
		}
	}
	return nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) DB() *sql.DB {
	return s.db
}

// GetAppSetting reads a non-secret application preference. Secrets such as the
// Zajuna password never use this table; they stay in the operating system
// credential store.
func (s *Store) GetAppSetting(ctx context.Context, key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", errors.New("setting key is required")
	}
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read app setting: %w", err)
	}
	return value, nil
}

// SetAppSetting persists a non-secret application preference.
func (s *Store) SetAppSetting(ctx context.Context, key, value string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("setting key is required")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO app_settings(key, value, updated_at) VALUES(?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, key, value, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("persist app setting: %w", err)
	}
	return nil
}

func (s *Store) CreateNotification(ctx context.Context, record NotificationRecord) error {
	if strings.TrimSpace(record.ID) == "" || strings.TrimSpace(record.Kind) == "" || strings.TrimSpace(record.Title) == "" {
		return errors.New("notification requires ID, kind and title")
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO notifications(id, kind, title, message, job_id, read_at, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(job_id, kind) DO NOTHING
	`, record.ID, record.Kind, record.Title, record.Message, record.JobID, nullableTime(record.ReadAt), record.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("create notification: %w", err)
	}
	return nil
}

func (s *Store) ListNotifications(ctx context.Context, limit int) ([]NotificationRecord, error) {
	limit = normalizeLimit(limit)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, kind, title, message, job_id, read_at, created_at
		FROM notifications ORDER BY created_at DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()
	result := make([]NotificationRecord, 0)
	for rows.Next() {
		var item NotificationRecord
		var readAt, createdAt sql.NullString
		if err := rows.Scan(&item.ID, &item.Kind, &item.Title, &item.Message, &item.JobID, &readAt, &createdAt); err != nil {
			return nil, fmt.Errorf("scan notification: %w", err)
		}
		if readAt.Valid {
			parsed, parseErr := time.Parse(time.RFC3339Nano, readAt.String)
			if parseErr == nil {
				item.ReadAt = &parsed
			}
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt.String)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read notifications: %w", err)
	}
	return result, nil
}

func (s *Store) MarkNotificationRead(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("notification id is required")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE notifications SET read_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return fmt.Errorf("mark notification read: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) MarkAllNotificationsRead(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE notifications SET read_at = COALESCE(read_at, ?) WHERE read_at IS NULL`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("mark all notifications read: %w", err)
	}
	return nil
}

// createJobNotification is deliberately best-effort. A notification failure
// must never turn an already completed or failed job into a different state.
func (s *Store) createJobNotification(ctx context.Context, jobID, kind, title, message string) {
	if jobID == "" || !s.notificationPreferenceEnabled(ctx, kind) {
		return
	}
	_ = s.CreateNotification(ctx, NotificationRecord{
		ID: fmt.Sprintf("notification-%s-%s", jobID, kind), Kind: kind,
		Title: title, Message: message, JobID: jobID, CreatedAt: time.Now().UTC(),
	})
}

func (s *Store) notificationPreferenceEnabled(ctx context.Context, kind string) bool {
	contents, err := s.GetAppSetting(ctx, "ui_preferences")
	if err != nil || contents == "" {
		return true
	}
	var preferences struct {
		Notifications struct {
			JobCompleted bool `json:"jobCompleted"`
			NeedsReview  bool `json:"needsReview"`
		} `json:"notifications"`
	}
	preferences.Notifications.JobCompleted = true
	preferences.Notifications.NeedsReview = true
	if json.Unmarshal([]byte(contents), &preferences) != nil {
		return true
	}
	if kind == "job_completed" {
		return preferences.Notifications.JobCompleted
	}
	if kind == "job_failed" {
		return preferences.Notifications.NeedsReview
	}
	return true
}

// SnapshotDatabase creates a consistent SQLite snapshot at target. The caller
// is responsible for placing the snapshot inside a temporary directory and
// packaging it afterwards.
func (s *Store) SnapshotDatabase(ctx context.Context, target string) error {
	if target == "" {
		return errors.New("snapshot target is required")
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve snapshot target: %w", err)
	}
	if _, err := os.Stat(absTarget); err == nil {
		return fmt.Errorf("snapshot target already exists: %s", absTarget)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check snapshot target: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absTarget), 0o700); err != nil {
		return fmt.Errorf("create snapshot directory: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, absTarget); err != nil {
		return fmt.Errorf("create sqlite snapshot: %w", err)
	}
	return nil
}

func (s *Store) CreateJob(ctx context.Context, job jobs.Job) error {
	input := string(job.Input)
	if input == "" {
		input = "{}"
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO jobs(id, type, status, input_json, progress, stage, message, attempt, max_attempts, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, job.ID, job.Type, job.Status, input, job.Progress, job.Stage, job.Message, job.Attempt, job.MaxAttempts, job.CreatedAt.UTC().Format(time.RFC3339Nano), job.UpdatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("create job: %w", err)
	}
	return s.AppendEvent(ctx, jobs.Event{JobID: job.ID, Kind: "queued", Progress: job.Progress, Message: "Trabajo en cola", CreatedAt: job.CreatedAt})
}

func (s *Store) UpsertFichas(ctx context.Context, fichas []zajuna.Ficha) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin ficha sync: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	count := 0
	for _, ficha := range fichas {
		if ficha.ExternalID == "" {
			return 0, errors.New("Zajuna devolvió una ficha sin identificador")
		}
		hash := sha256.Sum256([]byte(ficha.ExternalID))
		id := "ficha-" + hex.EncodeToString(hash[:8])
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO fichas(id, external_id, name, course_id, status, metadata_json, synced_at, created_at, updated_at)
			VALUES(?, ?, ?, ?, 'active', '{}', ?, ?, ?)
			ON CONFLICT(external_id) DO UPDATE SET
				name = excluded.name,
				course_id = excluded.course_id,
				status = 'active',
				synced_at = excluded.synced_at,
				updated_at = excluded.updated_at
		`, id, ficha.ExternalID, ficha.Name, ficha.CourseID, now, now, now); err != nil {
			return 0, fmt.Errorf("upsert ficha %s: %w", ficha.ExternalID, err)
		}
		if err := ensureChecklistItemsTx(ctx, tx, id); err != nil {
			return 0, fmt.Errorf("initialize checklist for ficha %s: %w", ficha.ExternalID, err)
		}
		count++
	}
	var activeID string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(value, '') FROM app_settings WHERE key = 'active_ficha_id'`).Scan(&activeID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("read active ficha: %w", err)
	}
	if activeID == "" && len(fichas) > 0 {
		hash := sha256.Sum256([]byte(fichas[0].ExternalID))
		activeID = "ficha-" + hex.EncodeToString(hash[:8])
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO app_settings(key, value, updated_at) VALUES('active_ficha_id', ?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
		`, activeID, now); err != nil {
			return 0, fmt.Errorf("persist active ficha: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit ficha sync: %w", err)
	}
	return count, nil
}

func (s *Store) GetJob(ctx context.Context, id string) (jobs.Job, error) {
	var job jobs.Job
	var status string
	var input, result, errorCode, errorMessage string
	var created, started, finished, updated string
	var startedValue, finishedValue sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, type, status, input_json, COALESCE(result_json, ''), progress, stage, message,
		       attempt, max_attempts, error_code, error_message, created_at, started_at, finished_at, updated_at
		FROM jobs WHERE id = ?
	`, id).Scan(&job.ID, &job.Type, &status, &input, &result, &job.Progress, &job.Stage, &job.Message, &job.Attempt, &job.MaxAttempts, &errorCode, &errorMessage, &created, &startedValue, &finishedValue, &updated)
	if err != nil {
		return jobs.Job{}, err
	}
	job.Status = jobs.Status(status)
	job.Input = []byte(input)
	if result != "" {
		job.Result = []byte(result)
	}
	job.ErrorCode = errorCode
	job.ErrorMessage = errorMessage
	job.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	job.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	if startedValue.Valid {
		started = startedValue.String
		parsed, _ := time.Parse(time.RFC3339Nano, started)
		job.StartedAt = &parsed
	}
	if finishedValue.Valid {
		finished = finishedValue.String
		parsed, _ := time.Parse(time.RFC3339Nano, finished)
		job.FinishedAt = &parsed
	}
	return job, nil
}

func (s *Store) ListJobs(ctx context.Context, limit int) ([]jobs.Job, error) {
	limit = normalizeLimit(limit)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, type, status, input_json, COALESCE(result_json, ''), progress, stage, message,
		       attempt, max_attempts, error_code, error_message, created_at, started_at, finished_at, updated_at
		FROM jobs ORDER BY updated_at DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()
	result := make([]jobs.Job, 0)
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		result = append(result, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read jobs: %w", err)
	}
	return result, nil
}

func (s *Store) ListFichas(ctx context.Context, limit int) ([]FichaRecord, error) {
	limit = normalizeLimit(limit)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, external_id, name, course_id, status, synced_at, updated_at
		FROM fichas ORDER BY updated_at DESC, name ASC LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list fichas: %w", err)
	}
	defer rows.Close()
	result := make([]FichaRecord, 0)
	for rows.Next() {
		var item FichaRecord
		var syncedAt, updatedAt sql.NullString
		if err := rows.Scan(&item.ID, &item.ExternalID, &item.Name, &item.CourseID, &item.Status, &syncedAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan ficha: %w", err)
		}
		if syncedAt.Valid {
			parsed, parseErr := time.Parse(time.RFC3339Nano, syncedAt.String)
			if parseErr == nil {
				item.SyncedAt = &parsed
			}
		}
		item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt.String)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read fichas: %w", err)
	}
	return result, nil
}

// GetFicha returns one locally synchronized ficha by its stable local ID.
func (s *Store) GetFicha(ctx context.Context, fichaID string) (FichaRecord, error) {
	return s.getFichaByID(ctx, strings.TrimSpace(fichaID))
}

func ensureChecklistItemsTx(ctx context.Context, tx *sql.Tx, fichaID string) error {
	if fichaID == "" {
		return errors.New("ficha id is required")
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO checklist_items(ficha_id, item_code, status, position, metadata_json, updated_at)
		SELECT ?, item_code, 'PENDIENTE', sort_order, '{}', ?
		FROM checklist_catalog_items
		WHERE NOT EXISTS (
			SELECT 1 FROM checklist_items existing
			WHERE existing.ficha_id = ? AND existing.item_code = checklist_catalog_items.item_code
		)
	`, fichaID, time.Now().UTC().Format(time.RFC3339Nano), fichaID)
	return err
}

func (s *Store) EnsureChecklistItems(ctx context.Context, fichaID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin checklist initialization: %w", err)
	}
	defer tx.Rollback()
	if err := ensureChecklistItemsTx(ctx, tx, fichaID); err != nil {
		return fmt.Errorf("initialize checklist: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit checklist initialization: %w", err)
	}
	return nil
}

func (s *Store) GetActiveFichaID(ctx context.Context) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = 'active_ficha_id'`).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read active ficha: %w", err)
	}
	return value, nil
}

func (s *Store) SetActiveFicha(ctx context.Context, fichaID string) error {
	fichaID = strings.TrimSpace(fichaID)
	if fichaID == "" {
		return errors.New("ficha id is required")
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM fichas WHERE id = ?`, fichaID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("ficha %q no existe", fichaID)
		}
		return fmt.Errorf("validate active ficha: %w", err)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO app_settings(key, value, updated_at) VALUES('active_ficha_id', ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, fichaID, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("persist active ficha: %w", err)
	}
	return nil
}

func (s *Store) GetChecklistDashboard(ctx context.Context, fichaID string) (ChecklistDashboard, error) {
	activeID, err := s.GetActiveFichaID(ctx)
	if err != nil {
		return ChecklistDashboard{}, err
	}
	if strings.TrimSpace(fichaID) == "" {
		fichaID = activeID
	}
	if strings.TrimSpace(fichaID) == "" {
		return ChecklistDashboard{}, sql.ErrNoRows
	}
	if err := s.EnsureChecklistItems(ctx, fichaID); err != nil {
		return ChecklistDashboard{}, err
	}
	ficha, err := s.getFichaByID(ctx, fichaID)
	if err != nil {
		return ChecklistDashboard{}, err
	}
	items, err := s.listChecklistItems(ctx, fichaID)
	if err != nil {
		return ChecklistDashboard{}, err
	}
	dashboard := ChecklistDashboard{Ficha: ficha, ActiveID: fichaID, Items: items}
	dashboard.Categories = make([]ChecklistCategoryProgress, 0, len(checklist.Categories()))
	for _, category := range checklist.Categories() {
		progress := ChecklistCategoryProgress{Code: category.Code, Label: category.Label}
		for _, item := range items {
			if item.CategoryCode != category.Code {
				continue
			}
			progress.Total++
			switch checklist.NormalizeStatus(item.Status) {
			case checklist.StatusYes:
				progress.Yes++
			case checklist.StatusNo:
				progress.No++
			default:
				progress.Pending++
			}
		}
		if progress.Total > 0 {
			progress.Percentage = (progress.Yes * 100) / progress.Total
		}
		dashboard.Categories = append(dashboard.Categories, progress)
	}
	dashboard.Summary.Total = len(items)
	for _, item := range items {
		switch checklist.NormalizeStatus(item.Status) {
		case checklist.StatusYes:
			dashboard.Summary.Yes++
		case checklist.StatusNo:
			dashboard.Summary.No++
		default:
			dashboard.Summary.Pending++
		}
	}
	if dashboard.Summary.Total > 0 {
		dashboard.Summary.Percentage = (dashboard.Summary.Yes * 100) / dashboard.Summary.Total
	}
	return dashboard, nil
}

func (s *Store) getFichaByID(ctx context.Context, fichaID string) (FichaRecord, error) {
	var item FichaRecord
	var syncedAt, updatedAt string
	if err := s.db.QueryRowContext(ctx, `SELECT id, external_id, name, course_id, status, synced_at, updated_at FROM fichas WHERE id = ?`, fichaID).Scan(&item.ID, &item.ExternalID, &item.Name, &item.CourseID, &item.Status, &syncedAt, &updatedAt); err != nil {
		return FichaRecord{}, err
	}
	if syncedAt != "" {
		parsed, parseErr := time.Parse(time.RFC3339Nano, syncedAt)
		if parseErr == nil {
			item.SyncedAt = &parsed
		}
	}
	item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return item, nil
}

func (s *Store) listChecklistItems(ctx context.Context, fichaID string) ([]ChecklistItemRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ci.id, ci.ficha_id, cat.code, cat.label, catalog.item_code, catalog.description,
		       catalog.group_name, catalog.max_evidences, ci.status,
		       (SELECT COUNT(*) FROM evidences e WHERE e.ficha_id = ci.ficha_id AND e.item_code = ci.item_code),
		       ci.position, ci.updated_at
		FROM checklist_items ci
		JOIN checklist_catalog_items catalog ON catalog.item_code = ci.item_code
		JOIN checklist_catalog_categories cat ON cat.code = catalog.category_code
		WHERE ci.ficha_id = ?
		ORDER BY ci.position, catalog.sort_order
	`, fichaID)
	if err != nil {
		return nil, fmt.Errorf("list checklist items: %w", err)
	}
	defer rows.Close()
	items := make([]ChecklistItemRecord, 0, 62)
	for rows.Next() {
		var item ChecklistItemRecord
		var updated string
		if err := rows.Scan(&item.ID, &item.FichaID, &item.CategoryCode, &item.CategoryLabel, &item.ItemCode, &item.Description, &item.GroupName, &item.MaxEvidences, &item.Status, &item.EvidenceCount, &item.Position, &updated); err != nil {
			return nil, fmt.Errorf("scan checklist item: %w", err)
		}
		item.Status = string(checklist.NormalizeStatus(item.Status))
		item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read checklist items: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close checklist items: %w", err)
	}
	evidenceByItem, err := s.listChecklistEvidences(ctx, fichaID)
	if err != nil {
		return nil, err
	}
	for index := range items {
		items[index].Evidences = evidenceByItem[items[index].ItemCode]
	}
	return items, nil
}

func (s *Store) listChecklistEvidences(ctx context.Context, fichaID string) (map[string][]evidence.Record, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, COALESCE(ficha_id, ''), item_code, slot_number, name, file_path, format, source, sha256, metadata_json, captured_at
		FROM evidences WHERE ficha_id = ? ORDER BY item_code, slot_number, captured_at
	`, fichaID)
	if err != nil {
		return nil, fmt.Errorf("list checklist evidences: %w", err)
	}
	defer rows.Close()
	result := make(map[string][]evidence.Record)
	for rows.Next() {
		var item evidence.Record
		var metadata, capturedAt string
		if err := rows.Scan(&item.ID, &item.FichaID, &item.ItemCode, &item.SlotNumber, &item.Name, &item.FilePath, &item.Format, &item.Source, &item.SHA256, &metadata, &capturedAt); err != nil {
			return nil, fmt.Errorf("scan checklist evidence: %w", err)
		}
		item.Metadata = json.RawMessage(metadata)
		item.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)
		result[item.ItemCode] = append(result[item.ItemCode], item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read checklist evidences: %w", err)
	}
	return result, nil
}

func (s *Store) SetChecklistItemStatus(ctx context.Context, fichaID, itemCode, status string) error {
	if !checklist.ValidStatus(status) {
		return fmt.Errorf("estado de checklist inválido: %s", status)
	}
	if err := s.EnsureChecklistItems(ctx, fichaID); err != nil {
		return err
	}
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin checklist status update: %w", err)
	}
	defer transaction.Rollback()
	var previous string
	if err := transaction.QueryRowContext(ctx, `SELECT status FROM checklist_items WHERE ficha_id = ? AND item_code = ?`, fichaID, itemCode).Scan(&previous); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sql.ErrNoRows
		}
		return fmt.Errorf("read checklist status: %w", err)
	}
	now := time.Now().UTC()
	result, err := transaction.ExecContext(ctx, `UPDATE checklist_items SET status = ?, updated_at = ? WHERE ficha_id = ? AND item_code = ?`, status, now.Format(time.RFC3339Nano), fichaID, itemCode)
	if err != nil {
		return fmt.Errorf("update checklist status: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO checklist_item_events(ficha_id, item_code, from_status, to_status, source, created_at)
		VALUES(?, ?, ?, ?, 'manual', ?)
	`, fichaID, itemCode, previous, status, now.Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("record checklist status event: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit checklist status update: %w", err)
	}
	return nil
}

func (s *Store) GetChecklistItemDetail(ctx context.Context, fichaID, itemCode string) (ChecklistItemDetail, error) {
	fichaID = strings.TrimSpace(fichaID)
	itemCode = strings.TrimSpace(itemCode)
	if fichaID == "" || itemCode == "" {
		return ChecklistItemDetail{}, sql.ErrNoRows
	}
	if err := s.EnsureChecklistItems(ctx, fichaID); err != nil {
		return ChecklistItemDetail{}, err
	}
	items, err := s.listChecklistItems(ctx, fichaID)
	if err != nil {
		return ChecklistItemDetail{}, err
	}
	var item ChecklistItemRecord
	for _, candidate := range items {
		if candidate.ItemCode == itemCode {
			item = candidate
			break
		}
	}
	if item.ItemCode == "" {
		return ChecklistItemDetail{}, sql.ErrNoRows
	}
	events, err := s.ListChecklistItemEvents(ctx, fichaID, itemCode, 50)
	if err != nil {
		return ChecklistItemDetail{}, err
	}
	return ChecklistItemDetail{Item: item, Events: events}, nil
}

func (s *Store) ListChecklistItemEvents(ctx context.Context, fichaID, itemCode string, limit int) ([]ChecklistItemEventRecord, error) {
	limit = normalizeLimit(limit)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, ficha_id, item_code, from_status, to_status, source, note, job_id, created_at
		FROM checklist_item_events
		WHERE ficha_id = ? AND item_code = ?
		ORDER BY id DESC LIMIT ?
	`, fichaID, itemCode, limit)
	if err != nil {
		return nil, fmt.Errorf("list checklist item events: %w", err)
	}
	defer rows.Close()
	events := make([]ChecklistItemEventRecord, 0)
	for rows.Next() {
		var event ChecklistItemEventRecord
		var createdAt string
		if err := rows.Scan(&event.ID, &event.FichaID, &event.ItemCode, &event.FromStatus, &event.ToStatus, &event.Source, &event.Note, &event.JobID, &createdAt); err != nil {
			return nil, fmt.Errorf("scan checklist item event: %w", err)
		}
		event.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read checklist item events: %w", err)
	}
	return events, nil
}

func (s *Store) CreateOrReplaceCourseMap(ctx context.Context, record coursemaps.Record) error {
	if record.CourseID == "" {
		return errors.New("el mapa de curso requiere un identificador")
	}
	if record.DiscoveredAt.IsZero() {
		record.DiscoveredAt = time.Now().UTC()
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = record.DiscoveredAt
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode course map: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO course_capture_maps(course_id, map_json, link_count, source, discovered_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(course_id) DO UPDATE SET
			map_json = excluded.map_json,
			link_count = excluded.link_count,
			source = excluded.source,
			discovered_at = excluded.discovered_at,
			updated_at = excluded.updated_at
	`, record.CourseID, string(encoded), record.LinkCount, record.Source, record.DiscoveredAt.UTC().Format(time.RFC3339Nano), record.UpdatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("save course map %s: %w", record.CourseID, err)
	}
	return nil
}

func (s *Store) GetCourseMap(ctx context.Context, courseID string) (coursemaps.Record, error) {
	var encoded string
	var discoveredAt, updatedAt string
	if err := s.db.QueryRowContext(ctx, `
		SELECT map_json, discovered_at, updated_at
		FROM course_capture_maps WHERE course_id = ?
	`, courseID).Scan(&encoded, &discoveredAt, &updatedAt); err != nil {
		return coursemaps.Record{}, err
	}
	var record coursemaps.Record
	if err := json.Unmarshal([]byte(encoded), &record); err != nil {
		return coursemaps.Record{}, fmt.Errorf("decode course map %s: %w", courseID, err)
	}
	if parsed, err := time.Parse(time.RFC3339Nano, discoveredAt); err == nil {
		record.DiscoveredAt = parsed
	}
	if parsed, err := time.Parse(time.RFC3339Nano, updatedAt); err == nil {
		record.UpdatedAt = parsed
	}
	return record, nil
}

func (s *Store) ListCourseMaps(ctx context.Context, limit int) ([]coursemaps.Record, error) {
	limit = normalizeLimit(limit)
	rows, err := s.db.QueryContext(ctx, `
		SELECT map_json, discovered_at, updated_at
		FROM course_capture_maps ORDER BY updated_at DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list course maps: %w", err)
	}
	defer rows.Close()
	result := make([]coursemaps.Record, 0)
	for rows.Next() {
		var encoded, discoveredAt, updatedAt string
		if err := rows.Scan(&encoded, &discoveredAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan course map: %w", err)
		}
		var record coursemaps.Record
		if err := json.Unmarshal([]byte(encoded), &record); err != nil {
			return nil, fmt.Errorf("decode course map: %w", err)
		}
		if parsed, err := time.Parse(time.RFC3339Nano, discoveredAt); err == nil {
			record.DiscoveredAt = parsed
		}
		if parsed, err := time.Parse(time.RFC3339Nano, updatedAt); err == nil {
			record.UpdatedAt = parsed
		}
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read course maps: %w", err)
	}
	return result, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(row rowScanner) (jobs.Job, error) {
	var job jobs.Job
	var status string
	var input, result, errorCode, errorMessage string
	var created, updated string
	var started, finished sql.NullString
	if err := row.Scan(&job.ID, &job.Type, &status, &input, &result, &job.Progress, &job.Stage, &job.Message, &job.Attempt, &job.MaxAttempts, &errorCode, &errorMessage, &created, &started, &finished, &updated); err != nil {
		return jobs.Job{}, err
	}
	job.Status = jobs.Status(status)
	job.Input = []byte(input)
	if result != "" {
		job.Result = []byte(result)
	}
	job.ErrorCode = errorCode
	job.ErrorMessage = errorMessage
	job.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	job.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	if started.Valid {
		parsed, parseErr := time.Parse(time.RFC3339Nano, started.String)
		if parseErr == nil {
			job.StartedAt = &parsed
		}
	}
	if finished.Valid {
		parsed, parseErr := time.Parse(time.RFC3339Nano, finished.String)
		if parseErr == nil {
			job.FinishedAt = &parsed
		}
	}
	return job, nil
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func (s *Store) MarkRunning(ctx context.Context, id string) (jobs.Job, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.transitionJob(ctx, id, jobs.AllowedSources(jobs.StatusRunning),
		`UPDATE jobs SET status = ?, attempt = attempt + 1, started_at = ?, finished_at = NULL, updated_at = ?`,
		[]any{jobs.StatusRunning, now, now},
		jobs.Event{JobID: id, Kind: "status", Stage: "running", Message: "Trabajo iniciado", CreatedAt: time.Now().UTC()},
	); err != nil {
		return jobs.Job{}, err
	}
	return s.GetJob(ctx, id)
}

func (s *Store) UpdateProgress(ctx context.Context, id string, stage string, progress int, message string) error {
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `UPDATE jobs SET stage = ?, progress = ?, message = ?, updated_at = ? WHERE id = ? AND status = ?`, stage, progress, message, now.Format(time.RFC3339Nano), id, jobs.StatusRunning); err != nil {
		return fmt.Errorf("update job progress: %w", err)
	}
	return s.AppendEvent(ctx, jobs.Event{JobID: id, Kind: "progress", Stage: stage, Progress: progress, Message: message, CreatedAt: now})
}

func (s *Store) AppendEvent(ctx context.Context, event jobs.Event) error {
	data := string(event.Data)
	if data == "" {
		data = "{}"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO job_events(job_id, kind, stage, progress, message, data_json, created_at) VALUES(?, ?, ?, ?, ?, ?, ?)`, event.JobID, event.Kind, event.Stage, event.Progress, event.Message, data, event.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("append job event: %w", err)
	}
	return nil
}

func (s *Store) ListJobEvents(ctx context.Context, id string) ([]jobs.Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind, stage, progress, message, data_json, created_at FROM job_events WHERE job_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, fmt.Errorf("list job events: %w", err)
	}
	defer rows.Close()
	result := make([]jobs.Event, 0)
	for rows.Next() {
		var event jobs.Event
		var data, created string
		if err := rows.Scan(&event.Kind, &event.Stage, &event.Progress, &event.Message, &data, &created); err != nil {
			return nil, fmt.Errorf("scan job event: %w", err)
		}
		event.JobID = id
		event.Data = json.RawMessage(data)
		event.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read job events: %w", err)
	}
	return result, nil
}

func (s *Store) CompleteJob(ctx context.Context, id string, output json.RawMessage) error {
	now := time.Now().UTC()
	if err := s.transitionJob(ctx, id, jobs.AllowedSources(jobs.StatusCompleted),
		`UPDATE jobs SET status = ?, result_json = ?, progress = 100, stage = 'completed', message = 'Trabajo completado', finished_at = ?, updated_at = ?`,
		[]any{jobs.StatusCompleted, string(output), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)},
		jobs.Event{JobID: id, Kind: "status", Stage: "completed", Progress: 100, Message: "Trabajo completado", CreatedAt: now},
	); err != nil {
		return err
	}
	s.createJobNotification(ctx, id, "job_completed", "Trabajo completado", "El proceso terminó correctamente.")
	return nil
}

func (s *Store) RetryJob(ctx context.Context, id string, code string, message string) error {
	now := time.Now().UTC()
	return s.transitionJob(ctx, id, jobs.AllowedSources(jobs.StatusRetrying),
		`UPDATE jobs SET status = ?, error_code = ?, error_message = ?, updated_at = ?`,
		[]any{jobs.StatusRetrying, code, message, now.Format(time.RFC3339Nano)},
		jobs.Event{JobID: id, Kind: "retry", Stage: "retrying", Message: message, CreatedAt: now},
	)
}

func (s *Store) FailJob(ctx context.Context, id string, code string, message string) error {
	now := time.Now().UTC()
	if err := s.transitionJob(ctx, id, jobs.AllowedSources(jobs.StatusFailed),
		`UPDATE jobs SET status = ?, error_code = ?, error_message = ?, stage = 'failed', message = ?, finished_at = ?, updated_at = ?`,
		[]any{jobs.StatusFailed, code, message, message, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)},
		jobs.Event{JobID: id, Kind: "status", Stage: "failed", Message: message, CreatedAt: now},
	); err != nil {
		return err
	}
	s.createJobNotification(ctx, id, "job_failed", "Trabajo necesita atención", fmt.Sprintf("El proceso terminó con el código %s.", code))
	return nil
}

func (s *Store) MarkCancelled(ctx context.Context, id string) error {
	now := time.Now().UTC()
	return s.transitionJob(ctx, id, jobs.AllowedSources(jobs.StatusCancelled),
		`UPDATE jobs SET status = ?, stage = 'cancelled', message = 'Trabajo cancelado', finished_at = ?, updated_at = ?`,
		[]any{jobs.StatusCancelled, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)},
		jobs.Event{JobID: id, Kind: "status", Stage: "cancelled", Message: "Trabajo cancelado", CreatedAt: now},
	)
}

func (s *Store) ReconcileInterrupted(ctx context.Context) ([]jobs.Job, error) {
	interrupted, err := s.listJobsByStatus(ctx, jobs.StatusRunning, jobs.StatusRetrying, jobs.StatusQueued)
	if err != nil {
		return nil, err
	}
	ready := make([]jobs.Job, 0, len(interrupted))
	for _, job := range interrupted {
		switch job.Status {
		case jobs.StatusRunning:
			if job.Attempt < job.MaxAttempts {
				if err := s.RetryJob(ctx, job.ID, "interrupted", "el core se reinició mientras el trabajo estaba en ejecución"); err != nil && !errors.Is(err, jobs.ErrInvalidTransition) {
					return nil, err
				}
			} else if err := s.FailJob(ctx, job.ID, "interrupted", "el trabajo quedó huérfano tras reiniciar el core"); err != nil && !errors.Is(err, jobs.ErrInvalidTransition) {
				return nil, err
			} else {
				continue
			}
			job, err = s.GetJob(ctx, job.ID)
			if err != nil {
				return nil, err
			}
			if job.Status == jobs.StatusRetrying || job.Status == jobs.StatusQueued {
				ready = append(ready, job)
			}
		default:
			ready = append(ready, job)
		}
	}
	return ready, nil
}

func (s *Store) listJobsByStatus(ctx context.Context, statuses ...jobs.Status) ([]jobs.Job, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(statuses))
	args := make([]any, len(statuses))
	for i, status := range statuses {
		placeholders[i] = "?"
		args[i] = status
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, type, status, input_json, COALESCE(result_json, ''), progress, stage, message,
		       attempt, max_attempts, error_code, error_message, created_at, started_at, finished_at, updated_at
		FROM jobs WHERE status IN (`+strings.Join(placeholders, ", ")+`) ORDER BY updated_at`, args...)
	if err != nil {
		return nil, fmt.Errorf("list jobs by status: %w", err)
	}
	defer rows.Close()
	result := make([]jobs.Job, 0)
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan interrupted job: %w", err)
		}
		result = append(result, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read interrupted jobs: %w", err)
	}
	return result, nil
}

func (s *Store) transitionJob(ctx context.Context, id string, from []jobs.Status, setSQL string, setArgs []any, event jobs.Event) error {
	if len(from) == 0 {
		return jobs.ErrInvalidTransition
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin job transition: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	placeholders := make([]string, len(from))
	args := append([]any{}, setArgs...)
	args = append(args, id)
	for i, status := range from {
		placeholders[i] = "?"
		args = append(args, status)
	}
	result, err := tx.ExecContext(ctx, setSQL+` WHERE id = ? AND status IN (`+strings.Join(placeholders, ", ")+`)`, args...)
	if err != nil {
		return fmt.Errorf("job transition: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return jobs.ErrInvalidTransition
	}
	data := string(event.Data)
	if data == "" {
		data = "{}"
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO job_events(job_id, kind, stage, progress, message, data_json, created_at) VALUES(?, ?, ?, ?, ?, ?, ?)`,
		event.JobID, event.Kind, event.Stage, event.Progress, event.Message, data, event.CreatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("append job event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit job transition: %w", err)
	}
	return nil
}

func (s *Store) CreateSchedule(ctx context.Context, schedule scheduler.Schedule) error {
	if schedule.ID == "" || schedule.WorkerType == "" || schedule.Interval < time.Second {
		return errors.New("schedule requires ID, worker type and an interval of at least one second")
	}
	input := string(schedule.Input)
	if input == "" {
		input = "{}"
	}
	now := time.Now().UTC()
	if schedule.CreatedAt.IsZero() {
		schedule.CreatedAt = now
	}
	if schedule.UpdatedAt.IsZero() {
		schedule.UpdatedAt = now
	}
	if schedule.NextRunAt.IsZero() {
		schedule.NextRunAt = now.Add(schedule.Interval)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO schedules(id, worker_type, input_json, interval_seconds, enabled, next_run_at, last_run_at, last_job_id, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, schedule.ID, schedule.WorkerType, input, int64(schedule.Interval/time.Second), boolToInt(schedule.Enabled), schedule.NextRunAt.UTC().Format(time.RFC3339Nano), nullableTime(schedule.LastRunAt), nullableString(schedule.LastJobID), schedule.CreatedAt.UTC().Format(time.RFC3339Nano), schedule.UpdatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("create schedule: %w", err)
	}
	return nil
}

func (s *Store) ListSchedules(ctx context.Context) ([]scheduler.Schedule, error) {
	return s.listSchedules(ctx, `SELECT id, worker_type, input_json, interval_seconds, enabled, next_run_at, last_run_at, last_job_id, created_at, updated_at FROM schedules ORDER BY created_at`)
}

func (s *Store) ListDueSchedules(ctx context.Context, now time.Time) ([]scheduler.Schedule, error) {
	return s.listSchedules(ctx, `SELECT id, worker_type, input_json, interval_seconds, enabled, next_run_at, last_run_at, last_job_id, created_at, updated_at FROM schedules WHERE enabled = 1 AND next_run_at <= ? ORDER BY next_run_at`, now.UTC().Format(time.RFC3339Nano))
}

func (s *Store) listSchedules(ctx context.Context, query string, args ...any) ([]scheduler.Schedule, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	defer rows.Close()
	result := make([]scheduler.Schedule, 0)
	for rows.Next() {
		var item scheduler.Schedule
		var input, nextRun, lastRun, lastJobID, created, updated sql.NullString
		var intervalSeconds int64
		var enabled int
		if err := rows.Scan(&item.ID, &item.WorkerType, &input, &intervalSeconds, &enabled, &nextRun, &lastRun, &lastJobID, &created, &updated); err != nil {
			return nil, fmt.Errorf("scan schedule: %w", err)
		}
		item.Input = json.RawMessage(input.String)
		item.Interval = time.Duration(intervalSeconds) * time.Second
		item.Enabled = enabled == 1
		item.NextRunAt, _ = time.Parse(time.RFC3339Nano, nextRun.String)
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created.String)
		item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated.String)
		if lastRun.Valid {
			parsed, parseErr := time.Parse(time.RFC3339Nano, lastRun.String)
			if parseErr == nil {
				item.LastRunAt = &parsed
			}
		}
		if lastJobID.Valid {
			item.LastJobID = lastJobID.String
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read schedules: %w", err)
	}
	return result, nil
}

func (s *Store) MarkScheduleRun(ctx context.Context, id, jobID string, lastRunAt, nextRunAt time.Time) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx, `UPDATE schedules SET last_run_at = ?, last_job_id = ?, next_run_at = ?, updated_at = ? WHERE id = ?`, lastRunAt.UTC().Format(time.RFC3339Nano), jobID, nextRunAt.UTC().Format(time.RFC3339Nano), now, id)
	if err != nil {
		return fmt.Errorf("mark schedule run: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) SetScheduleEnabled(ctx context.Context, id string, enabled bool) error {
	result, err := s.db.ExecContext(ctx, `UPDATE schedules SET enabled = ?, updated_at = ? WHERE id = ?`, boolToInt(enabled), time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return fmt.Errorf("set schedule enabled: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) CreateEvidence(ctx context.Context, record evidence.Record) error {
	if record.ID == "" || record.Name == "" || record.FilePath == "" || record.Format == "" || record.SHA256 == "" {
		return errors.New("evidence requires ID, name, file path, format and sha256")
	}
	metadata := string(record.Metadata)
	if metadata == "" {
		metadata = "{}"
	}
	if record.Source == "" {
		record.Source = "worker"
	}
	if record.CapturedAt.IsZero() {
		record.CapturedAt = time.Now().UTC()
	}
	// A checklist slot represents the current capture. Older database versions
	// may contain a hash-based ID for the same slot, so update that row in place
	// instead of turning a retry into a UNIQUE(ficha, item, slot, sha256) error.
	if record.Source == "capture-checklist" && record.FichaID != "" && record.ItemCode != "" {
		var existingID string
		lookupErr := s.db.QueryRowContext(ctx, `
			SELECT id FROM evidences
			WHERE ficha_id = ? AND item_code = ? AND slot_number = ? AND source = 'capture-checklist'
			ORDER BY captured_at DESC, id DESC LIMIT 1
		`, record.FichaID, record.ItemCode, record.SlotNumber).Scan(&existingID)
		if lookupErr == nil {
			_, err := s.db.ExecContext(ctx, `
				UPDATE evidences SET name = ?, file_path = ?, format = ?, sha256 = ?, metadata_json = ?, captured_at = ?
				WHERE id = ?
			`, record.Name, record.FilePath, record.Format, record.SHA256, metadata, record.CapturedAt.UTC().Format(time.RFC3339Nano), existingID)
			if err != nil {
				return fmt.Errorf("update checklist evidence: %w", err)
			}
			return nil
		}
		if !errors.Is(lookupErr, sql.ErrNoRows) {
			return fmt.Errorf("find existing checklist evidence: %w", lookupErr)
		}
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO evidences(id, ficha_id, item_code, slot_number, name, file_path, format, source, sha256, metadata_json, captured_at)
		VALUES(?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name, file_path = excluded.file_path, format = excluded.format,
			source = excluded.source, sha256 = excluded.sha256, metadata_json = excluded.metadata_json, captured_at = excluded.captured_at
	`, record.ID, record.FichaID, record.ItemCode, record.SlotNumber, record.Name, record.FilePath, record.Format, record.Source, record.SHA256, metadata, record.CapturedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("create evidence: %w", err)
	}
	return nil
}

func (s *Store) ListEvidences(ctx context.Context, limit int) ([]evidence.Record, error) {
	limit = normalizeLimit(limit)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, COALESCE(ficha_id, ''), item_code, slot_number, name, file_path, format, source, sha256, metadata_json, captured_at
		FROM evidences ORDER BY captured_at DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list evidences: %w", err)
	}
	defer rows.Close()
	result := make([]evidence.Record, 0)
	for rows.Next() {
		var item evidence.Record
		var metadata, capturedAt string
		if err := rows.Scan(&item.ID, &item.FichaID, &item.ItemCode, &item.SlotNumber, &item.Name, &item.FilePath, &item.Format, &item.Source, &item.SHA256, &metadata, &capturedAt); err != nil {
			return nil, fmt.Errorf("scan evidence: %w", err)
		}
		item.Metadata = json.RawMessage(metadata)
		item.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read evidences: %w", err)
	}
	return result, nil
}

func (s *Store) GetEvidence(ctx context.Context, id string) (evidence.Record, error) {
	var item evidence.Record
	var metadata, capturedAt string
	err := s.db.QueryRowContext(ctx, `SELECT id, COALESCE(ficha_id, ''), item_code, slot_number, name, file_path, format, source, sha256, metadata_json, captured_at FROM evidences WHERE id = ?`, id).Scan(&item.ID, &item.FichaID, &item.ItemCode, &item.SlotNumber, &item.Name, &item.FilePath, &item.Format, &item.Source, &item.SHA256, &metadata, &capturedAt)
	if err != nil {
		return evidence.Record{}, err
	}
	item.Metadata = json.RawMessage(metadata)
	item.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)
	return item, nil
}

func (s *Store) DeleteEvidence(ctx context.Context, id string) (evidence.Record, error) {
	item, err := s.GetEvidence(ctx, id)
	if err != nil {
		return evidence.Record{}, err
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM evidences WHERE id = ?`, id)
	if err != nil {
		return evidence.Record{}, fmt.Errorf("delete evidence: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return evidence.Record{}, sql.ErrNoRows
	}
	return item, nil
}

func (s *Store) CreateReport(ctx context.Context, record reports.Record) error {
	if record.ID == "" || record.Name == "" || record.FilePath == "" || record.Format == "" || record.SHA256 == "" {
		return errors.New("report requires ID, name, file path, format and sha256")
	}
	metadata := string(record.Metadata)
	if metadata == "" {
		metadata = "{}"
	}
	now := time.Now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = now
	}
	if record.Status == "" {
		record.Status = "completed"
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO reports(id, name, file_path, format, status, sha256, metadata_json, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name, file_path = excluded.file_path, format = excluded.format,
			status = excluded.status, sha256 = excluded.sha256, metadata_json = excluded.metadata_json, updated_at = excluded.updated_at
	`, record.ID, record.Name, record.FilePath, record.Format, record.Status, record.SHA256, metadata, record.CreatedAt.UTC().Format(time.RFC3339Nano), record.UpdatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("create report: %w", err)
	}
	return nil
}

func (s *Store) ListReports(ctx context.Context, limit int) ([]reports.Record, error) {
	limit = normalizeLimit(limit)
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, file_path, format, status, sha256, metadata_json, created_at, updated_at FROM reports ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list reports: %w", err)
	}
	defer rows.Close()
	result := make([]reports.Record, 0)
	for rows.Next() {
		var item reports.Record
		var metadata, createdAt, updatedAt string
		if err := rows.Scan(&item.ID, &item.Name, &item.FilePath, &item.Format, &item.Status, &item.SHA256, &metadata, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan report: %w", err)
		}
		item.Metadata = json.RawMessage(metadata)
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read reports: %w", err)
	}
	return result, nil
}

func (s *Store) GetReport(ctx context.Context, id string) (reports.Record, error) {
	var item reports.Record
	var metadata, createdAt, updatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT id, name, file_path, format, status, sha256, metadata_json, created_at, updated_at FROM reports WHERE id = ?`, id).Scan(&item.ID, &item.Name, &item.FilePath, &item.Format, &item.Status, &item.SHA256, &metadata, &createdAt, &updatedAt)
	if err != nil {
		return reports.Record{}, err
	}
	item.Metadata = json.RawMessage(metadata)
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return item, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
