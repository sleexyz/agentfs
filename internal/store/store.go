package store

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/agentfs/agentfs/internal/db"
	"github.com/google/uuid"
)

// Manager manages sparse bundle stores
type Manager struct {
	db       *db.DB
	basePath string
}

// NewManager creates a new store manager
func NewManager(database *db.DB) (*Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	basePath := filepath.Join(home, ".agentfs", "stores")
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create stores directory: %w", err)
	}

	return &Manager{
		db:       database,
		basePath: basePath,
	}, nil
}

// CreateOpts contains options for creating a store
type CreateOpts struct {
	Size      string // e.g., "50G"
	MountPath string // e.g., ~/projects/myapp
}

// Create creates a new sparse bundle store
func (m *Manager) Create(name string, opts CreateOpts) (*db.Store, error) {
	// Check if store already exists
	existing, err := m.db.GetStore(name)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing store: %w", err)
	}
	if existing != nil {
		return nil, fmt.Errorf("store '%s' already exists", name)
	}

	// Set defaults
	if opts.Size == "" {
		opts.Size = "50G"
	}
	if opts.MountPath == "" {
		home, _ := os.UserHomeDir()
		opts.MountPath = filepath.Join(home, "projects", name)
	}

	// Expand ~ in mount path
	opts.MountPath = expandPath(opts.MountPath)

	// Create store directory
	storeDir := filepath.Join(m.basePath, name)
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create store directory: %w", err)
	}

	// Create checkpoints directory
	checkpointsDir := filepath.Join(storeDir, "checkpoints")
	if err := os.MkdirAll(checkpointsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create checkpoints directory: %w", err)
	}

	bundlePath := filepath.Join(storeDir, name+".sparsebundle")

	// Create sparse bundle
	cmd := exec.Command("hdiutil", "create",
		"-size", opts.Size,
		"-type", "SPARSEBUNDLE",
		"-fs", "APFS",
		"-volname", name,
		bundlePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		os.RemoveAll(storeDir)
		return nil, fmt.Errorf("failed to create sparse bundle: %w\n%s", err, output)
	}

	// Mount the sparse bundle
	if err := os.MkdirAll(filepath.Dir(opts.MountPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create mount parent directory: %w", err)
	}

	cmd = exec.Command("hdiutil", "attach", bundlePath, "-mountpoint", opts.MountPath)
	output, err = cmd.CombinedOutput()
	if err != nil {
		os.RemoveAll(storeDir)
		return nil, fmt.Errorf("failed to mount sparse bundle: %w\n%s", err, output)
	}

	now := time.Now()
	store := &db.Store{
		ID:         uuid.New().String(),
		Name:       name,
		BundlePath: bundlePath,
		MountPath:  opts.MountPath,
		SizeBytes:  parseSize(opts.Size),
		CreatedAt:  now,
		MountedAt:  &now,
	}

	if err := m.db.CreateStore(store); err != nil {
		// Clean up on failure
		exec.Command("hdiutil", "detach", opts.MountPath).Run()
		os.RemoveAll(storeDir)
		return nil, fmt.Errorf("failed to record store: %w", err)
	}

	return store, nil
}

// Get retrieves a store by name and updates its mounted status
func (m *Manager) Get(name string) (*db.Store, error) {
	store, err := m.GetFast(name)
	if err != nil || store == nil {
		return store, err
	}

	// Update mounted status based on actual state
	mounted := m.IsMounted(store.MountPath)
	if mounted && store.MountedAt == nil {
		m.db.SetMounted(name, true)
		now := time.Now()
		store.MountedAt = &now
	} else if !mounted && store.MountedAt != nil {
		m.db.SetMounted(name, false)
		store.MountedAt = nil
	}

	return store, nil
}

// GetFast retrieves a store by name without checking mount status
// Use this in performance-critical paths where you'll check mounted status separately
func (m *Manager) GetFast(name string) (*db.Store, error) {
	store, err := m.db.GetStore(name)
	if err != nil {
		return nil, fmt.Errorf("failed to get store: %w", err)
	}
	return store, nil
}

// List returns all stores
func (m *Manager) List() ([]*db.Store, error) {
	stores, err := m.db.ListStores()
	if err != nil {
		return nil, fmt.Errorf("failed to list stores: %w", err)
	}

	// Update mounted status for each store
	for _, s := range stores {
		mounted := m.IsMounted(s.MountPath)
		if mounted && s.MountedAt == nil {
			m.db.SetMounted(s.Name, true)
			now := time.Now()
			s.MountedAt = &now
		} else if !mounted && s.MountedAt != nil {
			m.db.SetMounted(s.Name, false)
			s.MountedAt = nil
		}
	}

	return stores, nil
}

// Mount mounts a store
func (m *Manager) Mount(name string) error {
	store, err := m.Get(name)
	if err != nil {
		return err
	}
	if store == nil {
		return fmt.Errorf("store '%s' not found", name)
	}

	if m.IsMounted(store.MountPath) {
		return fmt.Errorf("store '%s' is already mounted", name)
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

	return m.db.SetMounted(name, true)
}

// Unmount unmounts a store
func (m *Manager) Unmount(name string) error {
	store, err := m.Get(name)
	if err != nil {
		return err
	}
	if store == nil {
		return fmt.Errorf("store '%s' not found", name)
	}

	if !m.IsMounted(store.MountPath) {
		return fmt.Errorf("store '%s' is not mounted", name)
	}

	cmd := exec.Command("hdiutil", "detach", store.MountPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to unmount: %w\n%s", err, output)
	}

	return m.db.SetMounted(name, false)
}

// Delete deletes a store
func (m *Manager) Delete(name string) error {
	store, err := m.Get(name)
	if err != nil {
		return err
	}
	if store == nil {
		return fmt.Errorf("store '%s' not found", name)
	}

	// Unmount if mounted
	if m.IsMounted(store.MountPath) {
		cmd := exec.Command("hdiutil", "detach", store.MountPath)
		cmd.Run() // Ignore error, we'll try to delete anyway
	}

	// Delete store directory
	storeDir := filepath.Join(m.basePath, name)
	if err := os.RemoveAll(storeDir); err != nil {
		return fmt.Errorf("failed to delete store files: %w", err)
	}

	// Delete from database
	if err := m.db.DeleteStore(name); err != nil {
		return fmt.Errorf("failed to delete store record: %w", err)
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
func (m *Manager) GetBandsPath(store *db.Store) string {
	return filepath.Join(store.BundlePath, "bands")
}

// GetCheckpointsPath returns the path to the checkpoints directory
func (m *Manager) GetCheckpointsPath(store *db.Store) string {
	storeDir := filepath.Join(m.basePath, store.Name)
	return filepath.Join(storeDir, "checkpoints")
}

func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
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
