package store

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Store represents a sparse bundle store (self-contained in foo.fs/)
type Store struct {
	Name        string
	StorePath   string // Path to foo.fs/ directory
	BundlePath  string // Path to foo.fs/data.sparsebundle/
	MountPath   string // Path to foo/ mount point (adjacent)
	SizeBytes   int64
	CreatedAt   time.Time
	MountedAt   *time.Time
	Checkpoints int // Count of checkpoints
}

// Manager manages sparse bundle stores (new self-contained format)
type Manager struct {
	// No longer needs a database - stores are self-contained
}

// NewManager creates a new store manager
func NewManager() *Manager {
	return &Manager{}
}

// CreateOpts contains options for creating a store
type CreateOpts struct {
	Size string // e.g., "50G"
}

// Create creates a new sparse bundle store in the current directory
// Creates foo.fs/ directory and mounts at foo/
func (m *Manager) Create(name string, opts CreateOpts) (*Store, error) {
	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current directory: %w", err)
	}

	storePath := filepath.Join(cwd, name+".fs")
	mountPath := filepath.Join(cwd, name)

	// Check if store already exists
	if _, err := os.Stat(storePath); err == nil {
		return nil, fmt.Errorf("%s already exists", name+".fs")
	}

	// Check if mount point already exists and is not empty
	if info, err := os.Stat(mountPath); err == nil {
		if info.IsDir() {
			entries, _ := os.ReadDir(mountPath)
			if len(entries) > 0 {
				return nil, fmt.Errorf("%s/ already exists and is not empty", name)
			}
		} else {
			return nil, fmt.Errorf("%s already exists and is not a directory", name)
		}
	}

	// Set defaults
	if opts.Size == "" {
		opts.Size = "50G"
	}

	// Create store directory structure
	if err := os.MkdirAll(storePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create store directory: %w", err)
	}

	// Create checkpoints directory
	checkpointsDir := filepath.Join(storePath, "checkpoints")
	if err := os.MkdirAll(checkpointsDir, 0755); err != nil {
		os.RemoveAll(storePath)
		return nil, fmt.Errorf("failed to create checkpoints directory: %w", err)
	}

	// Create sparse bundle inside store directory
	bundlePath := filepath.Join(storePath, "data.sparsebundle")
	cmd := exec.Command("hdiutil", "create",
		"-size", opts.Size,
		"-type", "SPARSEBUNDLE",
		"-fs", "APFS",
		"-volname", name,
		bundlePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		os.RemoveAll(storePath)
		return nil, fmt.Errorf("failed to create sparse bundle: %w\n%s", err, output)
	}

	// Create mount point directory
	if err := os.MkdirAll(mountPath, 0755); err != nil {
		os.RemoveAll(storePath)
		return nil, fmt.Errorf("failed to create mount point: %w", err)
	}

	// Mount the sparse bundle
	cmd = exec.Command("hdiutil", "attach", bundlePath, "-mountpoint", mountPath)
	output, err = cmd.CombinedOutput()
	if err != nil {
		os.RemoveAll(storePath)
		os.RemoveAll(mountPath)
		return nil, fmt.Errorf("failed to mount sparse bundle: %w\n%s", err, output)
	}

	now := time.Now()
	store := &Store{
		Name:       name,
		StorePath:  storePath,
		BundlePath: bundlePath,
		MountPath:  mountPath,
		SizeBytes:  parseSize(opts.Size),
		CreatedAt:  now,
		MountedAt:  &now,
	}

	return store, nil
}

// Get retrieves a store by name from the current directory
func (m *Manager) Get(name string) (*Store, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current directory: %w", err)
	}

	return m.GetFromDir(name, cwd)
}

// GetFromDir retrieves a store by name from a specific directory
func (m *Manager) GetFromDir(name string, dir string) (*Store, error) {
	storePath := filepath.Join(dir, name+".fs")
	return m.GetFromPath(storePath)
}

// GetFromPath retrieves a store from its full .fs path
func (m *Manager) GetFromPath(storePath string) (*Store, error) {
	// Verify store exists
	info, err := os.Stat(storePath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to stat store: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("not a valid store: %s", storePath)
	}

	// Verify it's a valid store (has data.sparsebundle)
	bundlePath := filepath.Join(storePath, "data.sparsebundle")
	if _, err := os.Stat(bundlePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("invalid store (missing data.sparsebundle): %s", storePath)
	}

	// Extract name from path (remove .fs suffix)
	name := strings.TrimSuffix(filepath.Base(storePath), ".fs")

	// Calculate mount path (adjacent directory)
	mountPath := filepath.Join(filepath.Dir(storePath), name)

	// Build store object
	store := &Store{
		Name:       name,
		StorePath:  storePath,
		BundlePath: bundlePath,
		MountPath:  mountPath,
		SizeBytes:  m.readStoreSizeFromBundle(bundlePath),
		CreatedAt:  info.ModTime(), // Use dir mtime as proxy for creation time
	}

	// Check if mounted
	if m.IsMounted(mountPath) {
		now := time.Now()
		store.MountedAt = &now
	}

	// Count checkpoints
	checkpointsPath := filepath.Join(storePath, "checkpoints")
	if entries, err := os.ReadDir(checkpointsPath); err == nil {
		for _, entry := range entries {
			if entry.IsDir() && strings.HasPrefix(entry.Name(), "v") {
				store.Checkpoints++
			}
		}
	}

	return store, nil
}

// List returns all stores in the current directory
func (m *Manager) List() ([]*Store, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current directory: %w", err)
	}

	return m.ListFromDir(cwd)
}

// ListFromDir returns all stores in a specific directory
func (m *Manager) ListFromDir(dir string) ([]*Store, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	var stores []*Store
	for _, entry := range entries {
		if entry.IsDir() && strings.HasSuffix(entry.Name(), ".fs") {
			storePath := filepath.Join(dir, entry.Name())
			store, err := m.GetFromPath(storePath)
			if err != nil {
				continue // Skip invalid stores
			}
			if store != nil {
				stores = append(stores, store)
			}
		}
	}

	return stores, nil
}

// Mount mounts a store
func (m *Manager) Mount(store *Store) error {
	if m.IsMounted(store.MountPath) {
		return fmt.Errorf("already mounted at %s", store.MountPath)
	}

	// Create mount point if it doesn't exist
	if err := os.MkdirAll(store.MountPath, 0755); err != nil {
		return fmt.Errorf("failed to create mount point: %w", err)
	}

	cmd := exec.Command("hdiutil", "attach", store.BundlePath, "-mountpoint", store.MountPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to mount: %w\n%s", err, output)
	}

	now := time.Now()
	store.MountedAt = &now
	return nil
}

// Unmount unmounts a store and removes the mount directory
func (m *Manager) Unmount(store *Store) error {
	if !m.IsMounted(store.MountPath) {
		return fmt.Errorf("not mounted")
	}

	cmd := exec.Command("hdiutil", "detach", store.MountPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to unmount: %w\n%s", err, output)
	}

	// Remove mount point directory
	os.Remove(store.MountPath)

	store.MountedAt = nil
	return nil
}

// Delete deletes a store completely
func (m *Manager) Delete(store *Store) error {
	// Unmount if mounted
	if m.IsMounted(store.MountPath) {
		cmd := exec.Command("hdiutil", "detach", store.MountPath)
		cmd.Run() // Ignore error, we'll try to delete anyway
	}

	// Remove mount point directory
	os.Remove(store.MountPath)

	// Delete store directory
	if err := os.RemoveAll(store.StorePath); err != nil {
		return fmt.Errorf("failed to delete store files: %w", err)
	}

	return nil
}

// IsMounted checks if a path is a mount point
func (m *Manager) IsMounted(path string) bool {
	// Check if the path exists
	pathInfo, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !pathInfo.IsDir() {
		return false
	}

	// Fast check: compare device IDs between path and its parent
	// If different, it's a mount point
	parentPath := filepath.Dir(path)
	parentInfo, err := os.Stat(parentPath)
	if err != nil {
		return false
	}

	// Get system-specific stat info to compare device IDs
	pathSys, ok1 := pathInfo.Sys().(*syscall.Stat_t)
	parentSys, ok2 := parentInfo.Sys().(*syscall.Stat_t)

	if ok1 && ok2 {
		return pathSys.Dev != parentSys.Dev
	}

	// Fallback: check if path is in mount list (slower but reliable)
	cmd := exec.Command("mount")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(output), " on "+path+" ")
}

// GetBandsPath returns the path to the bands directory in the sparse bundle
func (m *Manager) GetBandsPath(store *Store) string {
	return filepath.Join(store.BundlePath, "bands")
}

// GetCheckpointsPath returns the path to the checkpoints directory
func (m *Manager) GetCheckpointsPath(store *Store) string {
	return filepath.Join(store.StorePath, "checkpoints")
}

// readStoreSizeFromBundle reads the size from the sparse bundle Info.plist
func (m *Manager) readStoreSizeFromBundle(bundlePath string) int64 {
	// Default to 50GB if we can't read the size
	return 50 * 1024 * 1024 * 1024
}

func parseSize(size string) int64 {
	// Simple parser for sizes like "50G", "100M"
	size = strings.TrimSpace(size)
	if len(size) == 0 {
		return 0
	}

	var multiplier int64 = 1
	suffix := size[len(size)-1]
	numStr := size[:len(size)-1]

	switch suffix {
	case 'G', 'g':
		multiplier = 1024 * 1024 * 1024
	case 'M', 'm':
		multiplier = 1024 * 1024
	case 'K', 'k':
		multiplier = 1024
	default:
		numStr = size
	}

	var num int64
	fmt.Sscanf(numStr, "%d", &num)
	return num * multiplier
}
