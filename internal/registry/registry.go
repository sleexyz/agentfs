// Package registry provides a global registry for tracking agentfs stores.
// The registry is stored at ~/.agentfs/registry.db and tracks store paths,
// mount points, and auto-mount preferences.
package registry

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const (
	registryDir  = ".agentfs"
	registryFile = "registry.db"
)

// ErrNotFound is returned when a store is not found in the registry.
var ErrNotFound = errors.New("store not found in registry")

// Store represents a registered store entry.
type Store struct {
	ID            int64
	StorePath     string
	MountPoint    string
	AutoMount     bool
	CreatedAt     time.Time
	LastMountedAt *time.Time
}

// Registry manages the global store registry.
type Registry struct {
	db   *sql.DB
	path string
}

// registryPath returns the path to the registry database.
func registryPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, registryDir, registryFile), nil
}

// ensureRegistryDir creates the ~/.agentfs directory if it doesn't exist.
func ensureRegistryDir() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}
	dir := filepath.Join(home, registryDir)
	return os.MkdirAll(dir, 0755)
}

// Open opens the global registry, creating it if necessary.
func Open() (*Registry, error) {
	if err := ensureRegistryDir(); err != nil {
		return nil, err
	}

	dbPath, err := registryPath()
	if err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("failed to open registry: %w", err)
	}

	r := &Registry{db: db, path: dbPath}
	if err := r.init(); err != nil {
		db.Close()
		return nil, err
	}

	return r, nil
}

// init creates the registry schema if it doesn't exist.
func (r *Registry) init() error {
	schema := `
	CREATE TABLE IF NOT EXISTS stores (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		store_path TEXT NOT NULL UNIQUE,
		mount_point TEXT NOT NULL,
		auto_mount INTEGER NOT NULL DEFAULT 1,
		created_at INTEGER NOT NULL,
		last_mounted_at INTEGER
	);
	CREATE INDEX IF NOT EXISTS idx_stores_auto_mount ON stores(auto_mount);
	`
	_, err := r.db.Exec(schema)
	return err
}

// Close closes the registry database.
func (r *Registry) Close() error {
	return r.db.Close()
}

// Register adds a store to the registry.
// If the store already exists, it updates the mount point.
func (r *Registry) Register(storePath, mountPoint string) error {
	// Normalize paths
	storePath, err := filepath.Abs(storePath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}
	mountPoint, err = filepath.Abs(mountPoint)
	if err != nil {
		return fmt.Errorf("failed to get absolute mount path: %w", err)
	}

	now := time.Now().Unix()

	// Use INSERT OR REPLACE to handle both new and existing stores
	_, err = r.db.Exec(`
		INSERT INTO stores (store_path, mount_point, auto_mount, created_at, last_mounted_at)
		VALUES (?, ?, 1, ?, ?)
		ON CONFLICT(store_path) DO UPDATE SET
			mount_point = excluded.mount_point,
			last_mounted_at = excluded.last_mounted_at
	`, storePath, mountPoint, now, now)

	return err
}

// Unregister removes a store from the registry.
func (r *Registry) Unregister(storePath string) error {
	storePath, err := filepath.Abs(storePath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	result, err := r.db.Exec("DELETE FROM stores WHERE store_path = ?", storePath)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// Get retrieves a store from the registry by path.
func (r *Registry) Get(storePath string) (*Store, error) {
	storePath, err := filepath.Abs(storePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	var s Store
	var createdAt int64
	var lastMountedAt sql.NullInt64

	err = r.db.QueryRow(`
		SELECT id, store_path, mount_point, auto_mount, created_at, last_mounted_at
		FROM stores WHERE store_path = ?
	`, storePath).Scan(&s.ID, &s.StorePath, &s.MountPoint, &s.AutoMount, &createdAt, &lastMountedAt)

	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	s.CreatedAt = time.Unix(createdAt, 0)
	if lastMountedAt.Valid {
		t := time.Unix(lastMountedAt.Int64, 0)
		s.LastMountedAt = &t
	}

	return &s, nil
}

// List returns all registered stores.
func (r *Registry) List() ([]*Store, error) {
	rows, err := r.db.Query(`
		SELECT id, store_path, mount_point, auto_mount, created_at, last_mounted_at
		FROM stores ORDER BY store_path
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stores []*Store
	for rows.Next() {
		var s Store
		var createdAt int64
		var lastMountedAt sql.NullInt64

		if err := rows.Scan(&s.ID, &s.StorePath, &s.MountPoint, &s.AutoMount, &createdAt, &lastMountedAt); err != nil {
			return nil, err
		}

		s.CreatedAt = time.Unix(createdAt, 0)
		if lastMountedAt.Valid {
			t := time.Unix(lastMountedAt.Int64, 0)
			s.LastMountedAt = &t
		}

		stores = append(stores, &s)
	}

	return stores, rows.Err()
}

// GetAutoMountStores returns all stores with auto_mount enabled.
func (r *Registry) GetAutoMountStores() ([]*Store, error) {
	rows, err := r.db.Query(`
		SELECT id, store_path, mount_point, auto_mount, created_at, last_mounted_at
		FROM stores WHERE auto_mount = 1 ORDER BY store_path
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stores []*Store
	for rows.Next() {
		var s Store
		var createdAt int64
		var lastMountedAt sql.NullInt64

		if err := rows.Scan(&s.ID, &s.StorePath, &s.MountPoint, &s.AutoMount, &createdAt, &lastMountedAt); err != nil {
			return nil, err
		}

		s.CreatedAt = time.Unix(createdAt, 0)
		if lastMountedAt.Valid {
			t := time.Unix(lastMountedAt.Int64, 0)
			s.LastMountedAt = &t
		}

		stores = append(stores, &s)
	}

	return stores, rows.Err()
}

// UpdateLastMounted updates the last_mounted_at timestamp for a store.
func (r *Registry) UpdateLastMounted(storePath string) error {
	storePath, err := filepath.Abs(storePath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	now := time.Now().Unix()
	result, err := r.db.Exec("UPDATE stores SET last_mounted_at = ? WHERE store_path = ?", now, storePath)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		// Store not in registry - this is not an error, just ignore
		return nil
	}
	return nil
}

// SetAutoMount enables or disables auto-mount for a store.
func (r *Registry) SetAutoMount(storePath string, autoMount bool) error {
	storePath, err := filepath.Abs(storePath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	autoMountVal := 0
	if autoMount {
		autoMountVal = 1
	}

	result, err := r.db.Exec("UPDATE stores SET auto_mount = ? WHERE store_path = ?", autoMountVal, storePath)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// RemoveStale removes entries for stores that no longer exist on disk.
// Returns the list of removed store paths.
func (r *Registry) RemoveStale() ([]string, error) {
	stores, err := r.List()
	if err != nil {
		return nil, err
	}

	var removed []string
	for _, s := range stores {
		if _, err := os.Stat(s.StorePath); os.IsNotExist(err) {
			if err := r.Unregister(s.StorePath); err != nil {
				return removed, fmt.Errorf("failed to unregister %s: %w", s.StorePath, err)
			}
			removed = append(removed, s.StorePath)
		}
	}

	return removed, nil
}

// Count returns the number of registered stores.
func (r *Registry) Count() (int, error) {
	var count int
	err := r.db.QueryRow("SELECT COUNT(*) FROM stores").Scan(&count)
	return count, err
}

// Exists checks if a store is registered.
func (r *Registry) Exists(storePath string) (bool, error) {
	storePath, err := filepath.Abs(storePath)
	if err != nil {
		return false, fmt.Errorf("failed to get absolute path: %w", err)
	}

	var count int
	err = r.db.QueryRow("SELECT COUNT(*) FROM stores WHERE store_path = ?", storePath).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
