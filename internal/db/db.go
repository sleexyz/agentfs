package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Store represents a sparse bundle store
type Store struct {
	ID         string
	Name       string
	BundlePath string
	MountPath  string
	SizeBytes  int64
	CreatedAt  time.Time
	MountedAt  *time.Time
}

// Checkpoint represents a checkpoint of a store
type Checkpoint struct {
	ID        int64
	StoreID   string
	Version   int
	Message   string
	CreatedAt time.Time
}

// DB wraps the SQLite database
type DB struct {
	db *sql.DB
}

// DefaultPath returns the default database path
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, ".agentfs", "agentfs.db"), nil
}

// Open opens or creates the database
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

	d := &DB{db: db}
	if err := d.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return d, nil
}

// Close closes the database
func (d *DB) Close() error {
	return d.db.Close()
}

func (d *DB) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS stores (
		id TEXT PRIMARY KEY,
		name TEXT UNIQUE NOT NULL,
		bundle_path TEXT NOT NULL,
		mount_path TEXT NOT NULL,
		size_bytes INTEGER NOT NULL,
		created_at INTEGER NOT NULL,
		mounted_at INTEGER
	);

	CREATE TABLE IF NOT EXISTS checkpoints (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		store_id TEXT NOT NULL REFERENCES stores(id) ON DELETE CASCADE,
		version INTEGER NOT NULL,
		message TEXT,
		created_at INTEGER NOT NULL,
		UNIQUE(store_id, version)
	);

	CREATE INDEX IF NOT EXISTS idx_checkpoints_store ON checkpoints(store_id, version DESC);
	`

	_, err := d.db.Exec(schema)
	return err
}

// CreateStore creates a new store record
func (d *DB) CreateStore(store *Store) error {
	_, err := d.db.Exec(`
		INSERT INTO stores (id, name, bundle_path, mount_path, size_bytes, created_at, mounted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, store.ID, store.Name, store.BundlePath, store.MountPath, store.SizeBytes,
		store.CreatedAt.Unix(), timeToUnix(store.MountedAt))
	return err
}

// GetStore retrieves a store by name
func (d *DB) GetStore(name string) (*Store, error) {
	var s Store
	var createdAt int64
	var mountedAt sql.NullInt64

	err := d.db.QueryRow(`
		SELECT id, name, bundle_path, mount_path, size_bytes, created_at, mounted_at
		FROM stores WHERE name = ?
	`, name).Scan(&s.ID, &s.Name, &s.BundlePath, &s.MountPath, &s.SizeBytes, &createdAt, &mountedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	s.CreatedAt = time.Unix(createdAt, 0)
	if mountedAt.Valid {
		t := time.Unix(mountedAt.Int64, 0)
		s.MountedAt = &t
	}

	return &s, nil
}

// GetStoreByID retrieves a store by ID
func (d *DB) GetStoreByID(id string) (*Store, error) {
	var s Store
	var createdAt int64
	var mountedAt sql.NullInt64

	err := d.db.QueryRow(`
		SELECT id, name, bundle_path, mount_path, size_bytes, created_at, mounted_at
		FROM stores WHERE id = ?
	`, id).Scan(&s.ID, &s.Name, &s.BundlePath, &s.MountPath, &s.SizeBytes, &createdAt, &mountedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	s.CreatedAt = time.Unix(createdAt, 0)
	if mountedAt.Valid {
		t := time.Unix(mountedAt.Int64, 0)
		s.MountedAt = &t
	}

	return &s, nil
}

// ListStores returns all stores
func (d *DB) ListStores() ([]*Store, error) {
	rows, err := d.db.Query(`
		SELECT id, name, bundle_path, mount_path, size_bytes, created_at, mounted_at
		FROM stores ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stores []*Store
	for rows.Next() {
		var s Store
		var createdAt int64
		var mountedAt sql.NullInt64

		if err := rows.Scan(&s.ID, &s.Name, &s.BundlePath, &s.MountPath, &s.SizeBytes, &createdAt, &mountedAt); err != nil {
			return nil, err
		}

		s.CreatedAt = time.Unix(createdAt, 0)
		if mountedAt.Valid {
			t := time.Unix(mountedAt.Int64, 0)
			s.MountedAt = &t
		}

		stores = append(stores, &s)
	}

	return stores, rows.Err()
}

// DeleteStore deletes a store by name
func (d *DB) DeleteStore(name string) error {
	result, err := d.db.Exec("DELETE FROM stores WHERE name = ?", name)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SetMounted updates the mounted_at timestamp
func (d *DB) SetMounted(name string, mounted bool) error {
	var mountedAt interface{}
	if mounted {
		mountedAt = time.Now().Unix()
	}
	_, err := d.db.Exec("UPDATE stores SET mounted_at = ? WHERE name = ?", mountedAt, name)
	return err
}

// CreateCheckpoint creates a new checkpoint record
func (d *DB) CreateCheckpoint(cp *Checkpoint) error {
	result, err := d.db.Exec(`
		INSERT INTO checkpoints (store_id, version, message, created_at)
		VALUES (?, ?, ?, ?)
	`, cp.StoreID, cp.Version, nullString(cp.Message), cp.CreatedAt.Unix())
	if err != nil {
		return err
	}
	id, _ := result.LastInsertId()
	cp.ID = id
	return nil
}

// GetNextVersion returns the next version number for a store
func (d *DB) GetNextVersion(storeID string) (int, error) {
	var maxVersion sql.NullInt64
	err := d.db.QueryRow(`
		SELECT MAX(version) FROM checkpoints WHERE store_id = ?
	`, storeID).Scan(&maxVersion)
	if err != nil {
		return 0, err
	}
	if !maxVersion.Valid {
		return 1, nil
	}
	return int(maxVersion.Int64) + 1, nil
}

// GetCheckpoint retrieves a checkpoint by store ID and version
func (d *DB) GetCheckpoint(storeID string, version int) (*Checkpoint, error) {
	var cp Checkpoint
	var createdAt int64
	var message sql.NullString

	err := d.db.QueryRow(`
		SELECT id, store_id, version, message, created_at
		FROM checkpoints WHERE store_id = ? AND version = ?
	`, storeID, version).Scan(&cp.ID, &cp.StoreID, &cp.Version, &message, &createdAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	cp.Message = message.String
	cp.CreatedAt = time.Unix(createdAt, 0)

	return &cp, nil
}

// ListCheckpoints returns all checkpoints for a store
func (d *DB) ListCheckpoints(storeID string, limit int) ([]*Checkpoint, error) {
	query := `
		SELECT id, store_id, version, message, created_at
		FROM checkpoints WHERE store_id = ?
		ORDER BY version DESC
	`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := d.db.Query(query, storeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var checkpoints []*Checkpoint
	for rows.Next() {
		var cp Checkpoint
		var createdAt int64
		var message sql.NullString

		if err := rows.Scan(&cp.ID, &cp.StoreID, &cp.Version, &message, &createdAt); err != nil {
			return nil, err
		}

		cp.Message = message.String
		cp.CreatedAt = time.Unix(createdAt, 0)

		checkpoints = append(checkpoints, &cp)
	}

	return checkpoints, rows.Err()
}

// CountCheckpoints returns the number of checkpoints for a store
func (d *DB) CountCheckpoints(storeID string) (int, error) {
	var count int
	err := d.db.QueryRow(`SELECT COUNT(*) FROM checkpoints WHERE store_id = ?`, storeID).Scan(&count)
	return count, err
}

// DeleteCheckpoint deletes a checkpoint by store ID and version
func (d *DB) DeleteCheckpoint(storeID string, version int) error {
	result, err := d.db.Exec("DELETE FROM checkpoints WHERE store_id = ? AND version = ?", storeID, version)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// GetLatestCheckpoint returns the most recent checkpoint for a store
func (d *DB) GetLatestCheckpoint(storeID string) (*Checkpoint, error) {
	var cp Checkpoint
	var createdAt int64
	var message sql.NullString

	err := d.db.QueryRow(`
		SELECT id, store_id, version, message, created_at
		FROM checkpoints WHERE store_id = ?
		ORDER BY version DESC LIMIT 1
	`, storeID).Scan(&cp.ID, &cp.StoreID, &cp.Version, &message, &createdAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	cp.Message = message.String
	cp.CreatedAt = time.Unix(createdAt, 0)

	return &cp, nil
}

func timeToUnix(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return t.Unix()
}

func nullString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
