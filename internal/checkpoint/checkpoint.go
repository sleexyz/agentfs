package checkpoint

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/agentfs/agentfs/internal/db"
	"github.com/agentfs/agentfs/internal/store"
)

// Manager manages checkpoints
type Manager struct {
	db    *db.DB
	store *store.Manager
}

// NewManager creates a new checkpoint manager
func NewManager(database *db.DB, storeManager *store.Manager) *Manager {
	return &Manager{
		db:    database,
		store: storeManager,
	}
}

// CreateOpts contains options for creating a checkpoint
type CreateOpts struct {
	Message string
}

// Create creates a new checkpoint
func (m *Manager) Create(storeName string, opts CreateOpts) (*db.Checkpoint, time.Duration, error) {
	start := time.Now()

	// Get the store (use GetFast for performance)
	s, err := m.store.GetFast(storeName)
	if err != nil {
		return nil, 0, err
	}
	if s == nil {
		return nil, 0, fmt.Errorf("store '%s' not found", storeName)
	}

	// Check if mounted
	if !m.store.IsMounted(s.MountPath) {
		return nil, 0, fmt.Errorf("store '%s' is not mounted", storeName)
	}

	// Sync filesystem buffers for the mount point
	// Using diskutil quiet sync for specific volume is faster than global sync
	cmd := exec.Command("sync", "-f", s.MountPath)
	cmd.Run() // Ignore errors, sync is best-effort

	// Get next version number
	version, err := m.db.GetNextVersion(s.ID)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get next version: %w", err)
	}

	// Get paths
	bandsPath := m.store.GetBandsPath(s)
	checkpointsPath := m.store.GetCheckpointsPath(s)
	versionPath := filepath.Join(checkpointsPath, fmt.Sprintf("v%d", version))

	// Clone bands directory using APFS reflink (cp -Rc)
	cmd = exec.Command("cp", "-Rc", bandsPath+"/", versionPath+"/")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create checkpoint: %w\n%s", err, output)
	}

	// Update latest symlink
	latestPath := filepath.Join(checkpointsPath, "latest")
	os.Remove(latestPath) // Remove old symlink if exists
	if err := os.Symlink(fmt.Sprintf("v%d", version), latestPath); err != nil {
		// Non-fatal, just log
		fmt.Fprintf(os.Stderr, "warning: failed to update latest symlink: %v\n", err)
	}

	// Record in database
	cp := &db.Checkpoint{
		StoreID:   s.ID,
		Version:   version,
		Message:   opts.Message,
		CreatedAt: time.Now(),
	}
	if err := m.db.CreateCheckpoint(cp); err != nil {
		// Clean up the checkpoint directory
		os.RemoveAll(versionPath)
		return nil, 0, fmt.Errorf("failed to record checkpoint: %w", err)
	}

	return cp, time.Since(start), nil
}

// List returns all checkpoints for a store
func (m *Manager) List(storeName string, limit int) ([]*db.Checkpoint, error) {
	s, err := m.store.Get(storeName)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("store '%s' not found", storeName)
	}

	return m.db.ListCheckpoints(s.ID, limit)
}

// Get retrieves a checkpoint by version
func (m *Manager) Get(storeName string, version int) (*db.Checkpoint, error) {
	s, err := m.store.Get(storeName)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("store '%s' not found", storeName)
	}

	return m.db.GetCheckpoint(s.ID, version)
}

// Delete deletes a checkpoint
func (m *Manager) Delete(storeName string, version int) error {
	s, err := m.store.Get(storeName)
	if err != nil {
		return err
	}
	if s == nil {
		return fmt.Errorf("store '%s' not found", storeName)
	}

	// Delete checkpoint directory
	checkpointsPath := m.store.GetCheckpointsPath(s)
	versionPath := filepath.Join(checkpointsPath, fmt.Sprintf("v%d", version))

	if err := os.RemoveAll(versionPath); err != nil {
		return fmt.Errorf("failed to delete checkpoint files: %w", err)
	}

	// Delete from database
	if err := m.db.DeleteCheckpoint(s.ID, version); err != nil {
		return fmt.Errorf("failed to delete checkpoint record: %w", err)
	}

	return nil
}

// Restore restores a store to a checkpoint
func (m *Manager) Restore(storeName string, version int, createPreRestore bool) (*db.Checkpoint, time.Duration, error) {
	start := time.Now()

	// Get the store
	s, err := m.store.Get(storeName)
	if err != nil {
		return nil, 0, err
	}
	if s == nil {
		return nil, 0, fmt.Errorf("store '%s' not found", storeName)
	}

	// Get the target checkpoint
	cp, err := m.db.GetCheckpoint(s.ID, version)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get checkpoint: %w", err)
	}
	if cp == nil {
		return nil, 0, fmt.Errorf("checkpoint v%d not found", version)
	}

	checkpointsPath := m.store.GetCheckpointsPath(s)
	targetPath := filepath.Join(checkpointsPath, fmt.Sprintf("v%d", version))

	// Verify checkpoint exists on disk
	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		return nil, 0, fmt.Errorf("checkpoint v%d files not found on disk", version)
	}

	// Create pre-restore checkpoint if requested
	if createPreRestore && m.store.IsMounted(s.MountPath) {
		_, _, err := m.Create(storeName, CreateOpts{Message: "pre-restore"})
		if err != nil {
			return nil, 0, fmt.Errorf("failed to create pre-restore checkpoint: %w", err)
		}
	}

	// Unmount the store
	wasMounted := m.store.IsMounted(s.MountPath)
	if wasMounted {
		if err := m.store.Unmount(storeName); err != nil {
			return nil, 0, fmt.Errorf("failed to unmount: %w", err)
		}
	}

	// Swap bands
	bandsPath := m.store.GetBandsPath(s)
	backupPath := bandsPath + ".pre-restore"

	// Backup current bands
	if err := os.Rename(bandsPath, backupPath); err != nil {
		// Try to remount and fail
		if wasMounted {
			m.store.Mount(storeName)
		}
		return nil, 0, fmt.Errorf("failed to backup current bands: %w", err)
	}

	// Clone target checkpoint to bands
	cmd := exec.Command("cp", "-Rc", targetPath+"/", bandsPath+"/")
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Restore backup and remount
		os.RemoveAll(bandsPath)
		os.Rename(backupPath, bandsPath)
		if wasMounted {
			m.store.Mount(storeName)
		}
		return nil, 0, fmt.Errorf("failed to restore checkpoint: %w\n%s", err, output)
	}

	// Remount
	if wasMounted {
		if err := m.store.Mount(storeName); err != nil {
			return nil, 0, fmt.Errorf("failed to remount after restore: %w", err)
		}
	}

	// Clean up backup
	os.RemoveAll(backupPath)

	return cp, time.Since(start), nil
}

// DiffResult represents the result of a diff operation
type DiffResult struct {
	Modified []FileChange
	Added    []string
	Deleted  []string
}

// FileChange represents a modified file
type FileChange struct {
	Path      string
	LinesAdded   int
	LinesDeleted int
}

// Diff compares two checkpoints or current state vs checkpoint
func (m *Manager) Diff(storeName string, fromVersion, toVersion int) (*DiffResult, error) {
	s, err := m.store.Get(storeName)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("store '%s' not found", storeName)
	}

	checkpointsPath := m.store.GetCheckpointsPath(s)

	var fromPath, toPath string

	if fromVersion == 0 {
		// Current state
		if !m.store.IsMounted(s.MountPath) {
			return nil, fmt.Errorf("store must be mounted to diff against current state")
		}
		fromPath = s.MountPath
	} else {
		fromPath = filepath.Join(checkpointsPath, fmt.Sprintf("v%d", fromVersion))
		if _, err := os.Stat(fromPath); os.IsNotExist(err) {
			return nil, fmt.Errorf("checkpoint v%d not found", fromVersion)
		}
	}

	if toVersion == 0 {
		// Current state
		if !m.store.IsMounted(s.MountPath) {
			return nil, fmt.Errorf("store must be mounted to diff against current state")
		}
		toPath = s.MountPath
	} else {
		toPath = filepath.Join(checkpointsPath, fmt.Sprintf("v%d", toVersion))
		if _, err := os.Stat(toPath); os.IsNotExist(err) {
			return nil, fmt.Errorf("checkpoint v%d not found", toVersion)
		}
	}

	// For sparse bundle bands, we need to mount both to compare
	// For now, return a simplified diff based on file existence
	// A full implementation would mount the sparse bundles and compare

	result := &DiffResult{}

	// Use diff command to get changed files
	cmd := exec.Command("diff", "-rq", fromPath, toPath)
	output, _ := cmd.Output() // diff returns non-zero if there are differences

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "Files ") && strings.Contains(line, " differ") {
			// Extract file path
			parts := strings.Split(line, " ")
			if len(parts) >= 2 {
				path := parts[1]
				path = strings.TrimPrefix(path, fromPath+"/")
				result.Modified = append(result.Modified, FileChange{Path: path})
			}
		} else if strings.HasPrefix(line, "Only in "+fromPath) {
			// File deleted (only in from)
			path := extractOnlyInPath(line, fromPath)
			if path != "" {
				result.Deleted = append(result.Deleted, path)
			}
		} else if strings.HasPrefix(line, "Only in "+toPath) {
			// File added (only in to)
			path := extractOnlyInPath(line, toPath)
			if path != "" {
				result.Added = append(result.Added, path)
			}
		}
	}

	return result, nil
}

// Count returns the number of checkpoints for a store
func (m *Manager) Count(storeName string) (int, error) {
	s, err := m.store.Get(storeName)
	if err != nil {
		return 0, err
	}
	if s == nil {
		return 0, fmt.Errorf("store '%s' not found", storeName)
	}

	return m.db.CountCheckpoints(s.ID)
}

// GetLatest returns the most recent checkpoint for a store
func (m *Manager) GetLatest(storeName string) (*db.Checkpoint, error) {
	s, err := m.store.Get(storeName)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("store '%s' not found", storeName)
	}

	return m.db.GetLatestCheckpoint(s.ID)
}

func extractOnlyInPath(line, basePath string) string {
	// Format: "Only in /path/to/dir: filename"
	prefix := "Only in "
	line = strings.TrimPrefix(line, prefix)
	parts := strings.SplitN(line, ": ", 2)
	if len(parts) != 2 {
		return ""
	}
	dir := parts[0]
	file := parts[1]
	dir = strings.TrimPrefix(dir, basePath)
	dir = strings.TrimPrefix(dir, "/")
	if dir == "" {
		return file
	}
	return filepath.Join(dir, file)
}

// CountLines counts added/deleted lines using diff
func CountLines(fromFile, toFile string) (added, deleted int) {
	cmd := exec.Command("diff", "-u", fromFile, toFile)
	output, _ := cmd.Output()

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			added++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			deleted++
		}
	}
	return
}
