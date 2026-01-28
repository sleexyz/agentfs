package checkpoint

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sleexyz/agentfs/internal/db"
	"github.com/sleexyz/agentfs/internal/store"
)

// Manager manages checkpoints for a store
type Manager struct {
	store    *store.Manager
	database *db.DB      // Per-store database
	s        *store.Store // Current store
}

// NewManager creates a new checkpoint manager for a specific store
func NewManager(storeManager *store.Manager, database *db.DB, s *store.Store) *Manager {
	return &Manager{
		store:    storeManager,
		database: database,
		s:        s,
	}
}

// CreateOpts contains options for creating a checkpoint
type CreateOpts struct {
	Message string
}

// Create creates a new checkpoint
func (m *Manager) Create(opts CreateOpts) (*db.Checkpoint, time.Duration, error) {
	start := time.Now()

	// Check if mounted
	if !m.store.IsMounted(m.s.MountPath) {
		return nil, 0, fmt.Errorf("store '%s' is not mounted", m.s.Name)
	}

	// Sync filesystem buffers for the mount point
	cmd := exec.Command("sync", "-f", m.s.MountPath)
	cmd.Run() // Ignore errors, sync is best-effort

	// Get next version number
	version, err := m.database.GetNextVersion()
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get next version: %w", err)
	}

	// Get paths
	bandsPath := m.store.GetBandsPath(m.s)
	checkpointsPath := m.store.GetCheckpointsPath(m.s)
	versionPath := filepath.Join(checkpointsPath, fmt.Sprintf("v%d", version))

	// Clone bands directory using APFS reflink (cp -Rc)
	// Use /bin/cp explicitly to ensure macOS native cp with clonefile support
	cmd = exec.Command("/bin/cp", "-Rc", bandsPath+"/", versionPath+"/")
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
		Version:   version,
		Message:   opts.Message,
		CreatedAt: time.Now(),
	}
	if err := m.database.CreateCheckpoint(cp); err != nil {
		// Clean up the checkpoint directory
		os.RemoveAll(versionPath)
		return nil, 0, fmt.Errorf("failed to record checkpoint: %w", err)
	}

	return cp, time.Since(start), nil
}

// List returns all checkpoints
func (m *Manager) List(limit int) ([]*db.Checkpoint, error) {
	return m.database.ListCheckpoints(limit)
}

// Get retrieves a checkpoint by version
func (m *Manager) Get(version int) (*db.Checkpoint, error) {
	return m.database.GetCheckpoint(version)
}

// Delete deletes a checkpoint
func (m *Manager) Delete(version int) error {
	// Delete checkpoint directory
	checkpointsPath := m.store.GetCheckpointsPath(m.s)
	versionPath := filepath.Join(checkpointsPath, fmt.Sprintf("v%d", version))

	if err := os.RemoveAll(versionPath); err != nil {
		return fmt.Errorf("failed to delete checkpoint files: %w", err)
	}

	// Delete from database
	if err := m.database.DeleteCheckpoint(version); err != nil {
		return fmt.Errorf("failed to delete checkpoint record: %w", err)
	}

	return nil
}

// Restore restores a store to a checkpoint
func (m *Manager) Restore(version int, createPreRestore bool) (*db.Checkpoint, time.Duration, error) {
	start := time.Now()

	// Get the target checkpoint
	cp, err := m.database.GetCheckpoint(version)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get checkpoint: %w", err)
	}
	if cp == nil {
		return nil, 0, fmt.Errorf("checkpoint v%d not found", version)
	}

	checkpointsPath := m.store.GetCheckpointsPath(m.s)
	targetPath := filepath.Join(checkpointsPath, fmt.Sprintf("v%d", version))

	// Verify checkpoint exists on disk
	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		return nil, 0, fmt.Errorf("checkpoint v%d files not found on disk", version)
	}

	// Create pre-restore checkpoint if requested
	if createPreRestore && m.store.IsMounted(m.s.MountPath) {
		_, _, err := m.Create(CreateOpts{Message: "pre-restore"})
		if err != nil {
			return nil, 0, fmt.Errorf("failed to create pre-restore checkpoint: %w", err)
		}
	}

	// Unmount the store
	wasMounted := m.store.IsMounted(m.s.MountPath)
	if wasMounted {
		if err := m.store.Unmount(m.s); err != nil {
			return nil, 0, fmt.Errorf("failed to unmount: %w", err)
		}
	}

	// Swap bands
	bandsPath := m.store.GetBandsPath(m.s)
	backupPath := bandsPath + ".pre-restore"

	// Backup current bands
	if err := os.Rename(bandsPath, backupPath); err != nil {
		// Try to remount and fail
		if wasMounted {
			m.store.Mount(m.s)
		}
		return nil, 0, fmt.Errorf("failed to backup current bands: %w", err)
	}

	// Clone target checkpoint to bands
	// Use /bin/cp explicitly to ensure macOS native cp with clonefile support
	cmd := exec.Command("/bin/cp", "-Rc", targetPath+"/", bandsPath+"/")
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Restore backup and remount
		os.RemoveAll(bandsPath)
		os.Rename(backupPath, bandsPath)
		if wasMounted {
			m.store.Mount(m.s)
		}
		return nil, 0, fmt.Errorf("failed to restore checkpoint: %w\n%s", err, output)
	}

	// Remount
	if wasMounted {
		if err := m.store.Mount(m.s); err != nil {
			return nil, 0, fmt.Errorf("failed to remount after restore: %w", err)
		}
	}

	// Clean up backup
	os.RemoveAll(backupPath)

	return cp, time.Since(start), nil
}

// Count returns the number of checkpoints
func (m *Manager) Count() (int, error) {
	return m.database.CountCheckpoints()
}

// GetLatest returns the most recent checkpoint
func (m *Manager) GetLatest() (*db.Checkpoint, error) {
	return m.database.GetLatestCheckpoint()
}

// DiffResult represents the result of a diff operation
type DiffResult struct {
	Modified []FileChange
	Added    []string
	Deleted  []string
}

// FileChange represents a modified file
type FileChange struct {
	Path         string
	LinesAdded   int
	LinesDeleted int
}

// Diff compares two checkpoints or current state vs checkpoint
func (m *Manager) Diff(fromVersion, toVersion int) (*DiffResult, error) {
	checkpointsPath := m.store.GetCheckpointsPath(m.s)

	var fromPath, toPath string

	if fromVersion == 0 {
		// Current state
		if !m.store.IsMounted(m.s.MountPath) {
			return nil, fmt.Errorf("store must be mounted to diff against current state")
		}
		fromPath = m.s.MountPath
	} else {
		fromPath = filepath.Join(checkpointsPath, fmt.Sprintf("v%d", fromVersion))
		if _, err := os.Stat(fromPath); os.IsNotExist(err) {
			return nil, fmt.Errorf("checkpoint v%d not found", fromVersion)
		}
	}

	if toVersion == 0 {
		// Current state
		if !m.store.IsMounted(m.s.MountPath) {
			return nil, fmt.Errorf("store must be mounted to diff against current state")
		}
		toPath = m.s.MountPath
	} else {
		toPath = filepath.Join(checkpointsPath, fmt.Sprintf("v%d", toVersion))
		if _, err := os.Stat(toPath); os.IsNotExist(err) {
			return nil, fmt.Errorf("checkpoint v%d not found", toVersion)
		}
	}

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

// HasChanges checks if there are changes since the last checkpoint
// by comparing band files (names + sizes) between current bands and last checkpoint
func (m *Manager) HasChanges() (bool, error) {
	// Get the latest checkpoint
	latestCp, err := m.database.GetLatestCheckpoint()
	if err != nil {
		return false, err
	}
	if latestCp == nil {
		// No previous checkpoint = always has changes
		return true, nil
	}

	// Get paths
	currentBands := m.store.GetBandsPath(m.s)
	checkpointsPath := m.store.GetCheckpointsPath(m.s)
	lastBands := filepath.Join(checkpointsPath, fmt.Sprintf("v%d", latestCp.Version))

	// Compare directories by listing files with sizes
	return !dirsEqual(currentBands, lastBands), nil
}

// listDirWithSizes returns a map of filename -> size for all files in a directory
func listDirWithSizes(dir string) (map[string]int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	result := make(map[string]int64)
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		result[entry.Name()] = info.Size()
	}
	return result, nil
}

// dirsEqual compares two directories by file names and sizes
func dirsEqual(dir1, dir2 string) bool {
	entries1, err := listDirWithSizes(dir1)
	if err != nil {
		return false
	}

	entries2, err := listDirWithSizes(dir2)
	if err != nil {
		return false
	}

	if len(entries1) != len(entries2) {
		return false
	}

	for name, size1 := range entries1 {
		if size2, ok := entries2[name]; !ok || size1 != size2 {
			return false
		}
	}
	return true
}
