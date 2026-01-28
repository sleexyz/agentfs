// Package filehash provides content-addressed file tracking for checkpoints
package filehash

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// FileVersion represents a file's content hash at a specific checkpoint
type FileVersion struct {
	ID           int64
	CheckpointID int64
	Path         string
	ContentHash  string
	Size         int64
	Mtime        time.Time
}

// HashResult contains the result of hashing a file
type HashResult struct {
	Path        string
	ContentHash string
	Size        int64
	Mtime       time.Time
	Error       error
}

// HashOptions configures the hashing behavior
type HashOptions struct {
	Workers     int              // Number of parallel workers
	SkipDirs    map[string]bool  // Directories to skip (e.g., ".git", "node_modules")
	PrevHashes  map[string]*FileVersion // Previous checkpoint's hashes for incremental
}

// DefaultSkipDirs returns the default directories to skip
func DefaultSkipDirs() map[string]bool {
	return map[string]bool{
		".git":         true,
		"node_modules": true,
		".next":        true,
		"vendor":       true,
		"__pycache__":  true,
		".venv":        true,
		".DS_Store":    true,
	}
}

// Manager handles file hashing and tracking
type Manager struct {
	db *sql.DB
}

// NewManager creates a new file hash manager
func NewManager(db *sql.DB) *Manager {
	return &Manager{db: db}
}

// MigrateSchema creates the file_versions table if it doesn't exist
func (m *Manager) MigrateSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS file_versions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		checkpoint_id INTEGER NOT NULL REFERENCES checkpoints(id) ON DELETE CASCADE,
		path TEXT NOT NULL,
		content_hash TEXT NOT NULL,
		size INTEGER NOT NULL,
		mtime INTEGER NOT NULL,
		UNIQUE(checkpoint_id, path)
	);

	CREATE INDEX IF NOT EXISTS idx_file_versions_hash ON file_versions(content_hash);
	CREATE INDEX IF NOT EXISTS idx_file_versions_path ON file_versions(path, checkpoint_id);
	`
	_, err := m.db.Exec(schema)
	return err
}

// HashDirectory hashes all files in a directory
func (m *Manager) HashDirectory(dir string, opts HashOptions) ([]HashResult, time.Duration, error) {
	start := time.Now()

	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	if opts.SkipDirs == nil {
		opts.SkipDirs = DefaultSkipDirs()
	}

	// Collect all file paths
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if d.IsDir() {
			if opts.SkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			// Store relative path
			relPath, _ := filepath.Rel(dir, path)
			files = append(files, relPath)
		}
		return nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("walk directory: %w", err)
	}

	// Hash files in parallel
	results := make([]HashResult, len(files))
	var wg sync.WaitGroup
	fileCh := make(chan int, opts.Workers*2)
	var processed atomic.Int64

	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range fileCh {
				relPath := files[idx]
				absPath := filepath.Join(dir, relPath)

				// Check if we can skip (incremental mode)
				if opts.PrevHashes != nil {
					if prev, ok := opts.PrevHashes[relPath]; ok {
						// Check mtime
						info, err := os.Stat(absPath)
						if err == nil && info.ModTime().Equal(prev.Mtime) && info.Size() == prev.Size {
							// File unchanged, reuse previous hash
							results[idx] = HashResult{
								Path:        relPath,
								ContentHash: prev.ContentHash,
								Size:        prev.Size,
								Mtime:       prev.Mtime,
							}
							processed.Add(1)
							continue
						}
					}
				}

				// Hash the file
				hash, size, mtime, err := hashFile(absPath)
				results[idx] = HashResult{
					Path:        relPath,
					ContentHash: hash,
					Size:        size,
					Mtime:       mtime,
					Error:       err,
				}
				processed.Add(1)
			}
		}()
	}

	// Send work
	for i := range files {
		fileCh <- i
	}
	close(fileCh)
	wg.Wait()

	// Sort by path for consistent ordering
	sort.Slice(results, func(i, j int) bool {
		return results[i].Path < results[j].Path
	})

	return results, time.Since(start), nil
}

// StoreFileVersions stores file versions for a checkpoint
func (m *Manager) StoreFileVersions(checkpointID int64, results []HashResult) error {
	tx, err := m.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO file_versions (checkpoint_id, path, content_hash, size, mtime)
		VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, r := range results {
		if r.Error != nil {
			continue // Skip files that couldn't be hashed
		}
		_, err := stmt.Exec(checkpointID, r.Path, r.ContentHash, r.Size, r.Mtime.Unix())
		if err != nil {
			return fmt.Errorf("insert file version: %w", err)
		}
	}

	return tx.Commit()
}

// GetFileVersions retrieves all file versions for a checkpoint
func (m *Manager) GetFileVersions(checkpointID int64) (map[string]*FileVersion, error) {
	rows, err := m.db.Query(`
		SELECT id, checkpoint_id, path, content_hash, size, mtime
		FROM file_versions WHERE checkpoint_id = ?
	`, checkpointID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	versions := make(map[string]*FileVersion)
	for rows.Next() {
		var fv FileVersion
		var mtime int64
		if err := rows.Scan(&fv.ID, &fv.CheckpointID, &fv.Path, &fv.ContentHash, &fv.Size, &mtime); err != nil {
			return nil, err
		}
		fv.Mtime = time.Unix(mtime, 0)
		versions[fv.Path] = &fv
	}

	return versions, rows.Err()
}

// FindCheckpointsWithFile finds all checkpoints that contain a specific file version
func (m *Manager) FindCheckpointsWithFile(contentHash string) ([]int64, error) {
	rows, err := m.db.Query(`
		SELECT DISTINCT checkpoint_id FROM file_versions WHERE content_hash = ?
		ORDER BY checkpoint_id DESC
	`, contentHash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var checkpointIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		checkpointIDs = append(checkpointIDs, id)
	}

	return checkpointIDs, rows.Err()
}

// CountFiles returns the number of tracked files for a checkpoint
func (m *Manager) CountFiles(checkpointID int64) (int, error) {
	var count int
	err := m.db.QueryRow(`SELECT COUNT(*) FROM file_versions WHERE checkpoint_id = ?`, checkpointID).Scan(&count)
	return count, err
}

// GetTotalSize returns the total size of tracked files for a checkpoint
func (m *Manager) GetTotalSize(checkpointID int64) (int64, error) {
	var size sql.NullInt64
	err := m.db.QueryRow(`SELECT SUM(size) FROM file_versions WHERE checkpoint_id = ?`, checkpointID).Scan(&size)
	if !size.Valid {
		return 0, err
	}
	return size.Int64, err
}

func hashFile(path string) (hash string, size int64, mtime time.Time, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, time.Time{}, err
	}
	mtime = info.ModTime()
	size = info.Size()

	f, err := os.Open(path)
	if err != nil {
		return "", 0, time.Time{}, err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", 0, time.Time{}, err
	}

	return fmt.Sprintf("%x", h.Sum(nil)), size, mtime, nil
}
