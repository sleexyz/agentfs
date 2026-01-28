package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// StoreInfo represents store metadata stored in the per-store database
type StoreInfo struct {
	Name      string
	CreatedAt time.Time
	SizeBytes int64
}

// Checkpoint represents a checkpoint record
type Checkpoint struct {
	ID            int64
	Version       int
	Message       string
	CreatedAt     time.Time
	DurationMs    int64 // Duration of checkpoint creation in milliseconds
	ParentVersion *int  // Version this checkpoint was created from (null for v1 or imports)
}

// DB wraps the per-store SQLite database (foo.fs/metadata.db)
type DB struct {
	db   *sql.DB
	path string
}

// Open opens or creates the per-store database at the given path
func Open(path string) (*DB, error) {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	d := &DB{db: db, path: path}
	if err := d.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return d, nil
}

// OpenFromStorePath opens the database for a store given its .fs path
func OpenFromStorePath(storePath string) (*DB, error) {
	dbPath := filepath.Join(storePath, "metadata.db")
	return Open(dbPath)
}

// Close closes the database
func (d *DB) Close() error {
	return d.db.Close()
}

func (d *DB) migrate() error {
	schema := `
	-- Store info (singleton row with id=1)
	CREATE TABLE IF NOT EXISTS store (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		name TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		size_bytes INTEGER NOT NULL
	);

	-- Checkpoints
	CREATE TABLE IF NOT EXISTS checkpoints (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		version INTEGER NOT NULL UNIQUE,
		message TEXT,
		created_at INTEGER NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_checkpoints_version ON checkpoints(version DESC);

	-- Settings (key-value store for future use)
	CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY,
		value TEXT
	);
	`

	if _, err := d.db.Exec(schema); err != nil {
		return err
	}

	// Migration: Add duration_ms and parent_version columns if they don't exist
	// SQLite ALTER TABLE ADD COLUMN with NULL default works on existing rows
	migrations := []string{
		"ALTER TABLE checkpoints ADD COLUMN duration_ms INTEGER",
		"ALTER TABLE checkpoints ADD COLUMN parent_version INTEGER",
	}

	for _, migration := range migrations {
		// Ignore "duplicate column" errors - column already exists
		_, err := d.db.Exec(migration)
		if err != nil && !isDuplicateColumnError(err) {
			return err
		}
	}

	return nil
}

// isDuplicateColumnError checks if the error is a duplicate column error
func isDuplicateColumnError(err error) bool {
	if err == nil {
		return false
	}
	// SQLite returns "duplicate column name" error
	return strings.Contains(err.Error(), "duplicate column")
}

// InitStore initializes store info in the database
func (d *DB) InitStore(name string, sizeBytes int64) error {
	_, err := d.db.Exec(`
		INSERT OR REPLACE INTO store (id, name, created_at, size_bytes)
		VALUES (1, ?, ?, ?)
	`, name, time.Now().Unix(), sizeBytes)
	return err
}

// GetStoreInfo retrieves store info
func (d *DB) GetStoreInfo() (*StoreInfo, error) {
	var info StoreInfo
	var createdAt int64

	err := d.db.QueryRow(`
		SELECT name, created_at, size_bytes FROM store WHERE id = 1
	`).Scan(&info.Name, &createdAt, &info.SizeBytes)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	info.CreatedAt = time.Unix(createdAt, 0)
	return &info, nil
}

// CreateCheckpoint creates a new checkpoint record
func (d *DB) CreateCheckpoint(cp *Checkpoint) error {
	result, err := d.db.Exec(`
		INSERT INTO checkpoints (version, message, created_at, duration_ms, parent_version)
		VALUES (?, ?, ?, ?, ?)
	`, cp.Version, nullString(cp.Message), cp.CreatedAt.Unix(), cp.DurationMs, nullInt(cp.ParentVersion))
	if err != nil {
		return err
	}
	id, _ := result.LastInsertId()
	cp.ID = id
	return nil
}

// GetNextVersion returns the next version number
func (d *DB) GetNextVersion() (int, error) {
	var maxVersion sql.NullInt64
	err := d.db.QueryRow(`SELECT MAX(version) FROM checkpoints`).Scan(&maxVersion)
	if err != nil {
		return 0, err
	}
	if !maxVersion.Valid {
		return 1, nil
	}
	return int(maxVersion.Int64) + 1, nil
}

// GetCheckpoint retrieves a checkpoint by version
func (d *DB) GetCheckpoint(version int) (*Checkpoint, error) {
	var cp Checkpoint
	var createdAt int64
	var message sql.NullString
	var durationMs sql.NullInt64
	var parentVersion sql.NullInt64

	err := d.db.QueryRow(`
		SELECT id, version, message, created_at, duration_ms, parent_version
		FROM checkpoints WHERE version = ?
	`, version).Scan(&cp.ID, &cp.Version, &message, &createdAt, &durationMs, &parentVersion)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	cp.Message = message.String
	cp.CreatedAt = time.Unix(createdAt, 0)
	if durationMs.Valid {
		cp.DurationMs = durationMs.Int64
	}
	if parentVersion.Valid {
		pv := int(parentVersion.Int64)
		cp.ParentVersion = &pv
	}

	return &cp, nil
}

// ListCheckpoints returns all checkpoints
func (d *DB) ListCheckpoints(limit int) ([]*Checkpoint, error) {
	query := `
		SELECT id, version, message, created_at, duration_ms, parent_version
		FROM checkpoints
		ORDER BY version DESC
	`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := d.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var checkpoints []*Checkpoint
	for rows.Next() {
		var cp Checkpoint
		var createdAt int64
		var message sql.NullString
		var durationMs sql.NullInt64
		var parentVersion sql.NullInt64

		if err := rows.Scan(&cp.ID, &cp.Version, &message, &createdAt, &durationMs, &parentVersion); err != nil {
			return nil, err
		}

		cp.Message = message.String
		cp.CreatedAt = time.Unix(createdAt, 0)
		if durationMs.Valid {
			cp.DurationMs = durationMs.Int64
		}
		if parentVersion.Valid {
			pv := int(parentVersion.Int64)
			cp.ParentVersion = &pv
		}

		checkpoints = append(checkpoints, &cp)
	}

	return checkpoints, rows.Err()
}

// CountCheckpoints returns the number of checkpoints
func (d *DB) CountCheckpoints() (int, error) {
	var count int
	err := d.db.QueryRow(`SELECT COUNT(*) FROM checkpoints`).Scan(&count)
	return count, err
}

// DeleteCheckpoint deletes a checkpoint by version
func (d *DB) DeleteCheckpoint(version int) error {
	result, err := d.db.Exec("DELETE FROM checkpoints WHERE version = ?", version)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// GetLatestCheckpoint returns the most recent checkpoint
func (d *DB) GetLatestCheckpoint() (*Checkpoint, error) {
	var cp Checkpoint
	var createdAt int64
	var message sql.NullString
	var durationMs sql.NullInt64
	var parentVersion sql.NullInt64

	err := d.db.QueryRow(`
		SELECT id, version, message, created_at, duration_ms, parent_version
		FROM checkpoints
		ORDER BY version DESC LIMIT 1
	`).Scan(&cp.ID, &cp.Version, &message, &createdAt, &durationMs, &parentVersion)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	cp.Message = message.String
	cp.CreatedAt = time.Unix(createdAt, 0)
	if durationMs.Valid {
		cp.DurationMs = durationMs.Int64
	}
	if parentVersion.Valid {
		pv := int(parentVersion.Int64)
		cp.ParentVersion = &pv
	}

	return &cp, nil
}

// GetSetting retrieves a setting by key
func (d *DB) GetSetting(key string) (string, error) {
	var value string
	err := d.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

// SetSetting stores a setting
func (d *DB) SetSetting(key, value string) error {
	_, err := d.db.Exec(`
		INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)
	`, key, value)
	return err
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(i *int) any {
	if i == nil {
		return nil
	}
	return *i
}
