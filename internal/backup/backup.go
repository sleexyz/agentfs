// Package backup manages backups for the manage command.
// Backups are stored at ~/.agentfs/backups/ with metadata in index.json.
package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	backupsDir = "backups"
	indexFile  = "index.json"
)

// Entry represents a single backup entry.
type Entry struct {
	ID           string    `json:"id"`
	OriginalPath string    `json:"original_path"`
	StorePath    string    `json:"store_path"`
	CreatedAt    time.Time `json:"created_at"`
	SizeBytes    int64     `json:"size_bytes"`
}

// Index represents the backup index.
type Index struct {
	Backups []Entry `json:"backups"`
}

// Manager manages backups stored in ~/.agentfs/backups/.
type Manager struct {
	basePath string // ~/.agentfs/backups/
}

// NewManager creates a new backup manager.
func NewManager() (*Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	basePath := filepath.Join(home, ".agentfs", backupsDir)
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create backups directory: %w", err)
	}

	return &Manager{basePath: basePath}, nil
}

// GenerateID generates a unique backup ID based on original path and current time.
func GenerateID(originalPath string) string {
	h := sha256.New()
	h.Write([]byte(originalPath))
	h.Write([]byte(time.Now().Format(time.RFC3339Nano)))
	return hex.EncodeToString(h.Sum(nil))[:8]
}

// loadIndex loads the backup index from disk.
func (m *Manager) loadIndex() (*Index, error) {
	indexPath := filepath.Join(m.basePath, indexFile)
	data, err := os.ReadFile(indexPath)
	if os.IsNotExist(err) {
		return &Index{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read index: %w", err)
	}

	var index Index
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("failed to parse index: %w", err)
	}

	return &index, nil
}

// saveIndex saves the backup index to disk.
func (m *Manager) saveIndex(index *Index) error {
	indexPath := filepath.Join(m.basePath, indexFile)
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal index: %w", err)
	}

	if err := os.WriteFile(indexPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write index: %w", err)
	}

	return nil
}

// Save moves a directory to the backup location and records metadata.
// Returns the backup entry on success.
func (m *Manager) Save(originalPath, storePath string) (*Entry, error) {
	// Resolve to absolute paths
	absOriginal, err := filepath.Abs(originalPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve original path: %w", err)
	}
	absStore, err := filepath.Abs(storePath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve store path: %w", err)
	}

	// Check if backup already exists for this path
	existing, err := m.GetByOriginalPath(absOriginal)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("backup already exists for %s (ID: %s)", absOriginal, existing.ID)
	}

	// Calculate size before moving
	size, err := dirSize(absOriginal)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate size: %w", err)
	}

	// Generate backup ID
	id := GenerateID(absOriginal)
	backupPath := filepath.Join(m.basePath, id)

	// Move directory to backup location
	// First try rename (fast, same filesystem)
	if err := os.Rename(absOriginal, backupPath); err != nil {
		// If rename fails (cross-device), fall back to copy+delete
		if err := copyDir(absOriginal, backupPath); err != nil {
			os.RemoveAll(backupPath) // Clean up partial copy
			return nil, fmt.Errorf("failed to copy to backup: %w", err)
		}
		if err := os.RemoveAll(absOriginal); err != nil {
			// Copy succeeded but delete failed - warn but continue
			fmt.Fprintf(os.Stderr, "warning: failed to remove original after copy: %v\n", err)
		}
	}

	// Create entry
	entry := &Entry{
		ID:           id,
		OriginalPath: absOriginal,
		StorePath:    absStore,
		CreatedAt:    time.Now(),
		SizeBytes:    size,
	}

	// Update index
	index, err := m.loadIndex()
	if err != nil {
		return nil, err
	}
	index.Backups = append(index.Backups, *entry)
	if err := m.saveIndex(index); err != nil {
		return nil, err
	}

	return entry, nil
}

// GetByOriginalPath finds a backup by original path.
func (m *Manager) GetByOriginalPath(originalPath string) (*Entry, error) {
	absPath, err := filepath.Abs(originalPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve path: %w", err)
	}

	index, err := m.loadIndex()
	if err != nil {
		return nil, err
	}

	for _, entry := range index.Backups {
		if entry.OriginalPath == absPath {
			return &entry, nil
		}
	}

	return nil, nil
}

// GetByStorePath finds a backup by store path.
func (m *Manager) GetByStorePath(storePath string) (*Entry, error) {
	absPath, err := filepath.Abs(storePath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve path: %w", err)
	}

	index, err := m.loadIndex()
	if err != nil {
		return nil, err
	}

	for _, entry := range index.Backups {
		if entry.StorePath == absPath {
			return &entry, nil
		}
	}

	return nil, nil
}

// GetByID finds a backup by ID.
func (m *Manager) GetByID(id string) (*Entry, error) {
	index, err := m.loadIndex()
	if err != nil {
		return nil, err
	}

	for _, entry := range index.Backups {
		if entry.ID == id {
			return &entry, nil
		}
	}

	return nil, nil
}

// Delete removes a backup and updates the index.
func (m *Manager) Delete(id string) error {
	index, err := m.loadIndex()
	if err != nil {
		return err
	}

	// Find and remove from index
	found := false
	newBackups := make([]Entry, 0, len(index.Backups))
	for _, entry := range index.Backups {
		if entry.ID == id {
			found = true
			continue
		}
		newBackups = append(newBackups, entry)
	}

	if !found {
		return fmt.Errorf("backup not found: %s", id)
	}

	// Remove backup directory
	backupPath := filepath.Join(m.basePath, id)
	if err := os.RemoveAll(backupPath); err != nil {
		return fmt.Errorf("failed to remove backup directory: %w", err)
	}

	// Update index
	index.Backups = newBackups
	return m.saveIndex(index)
}

// List returns all backups.
func (m *Manager) List() ([]Entry, error) {
	index, err := m.loadIndex()
	if err != nil {
		return nil, err
	}
	return index.Backups, nil
}

// Path returns the path to a backup's contents.
func (m *Manager) Path(id string) string {
	return filepath.Join(m.basePath, id)
}

// dirSize calculates the total size of a directory.
func dirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}

// copyDir copies a directory recursively.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Calculate destination path
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		// Handle symlinks
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, dstPath)
		}

		// Copy regular file
		return copyFile(path, dstPath, info.Mode())
	})
}

// copyFile copies a single file.
func copyFile(src, dst string, mode os.FileMode) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// FormatSize formats a size in bytes to human-readable format.
func FormatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
