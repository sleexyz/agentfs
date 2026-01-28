package diff

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sleexyz/agentfs/internal/store"
)

// ChangeType represents the type of file change
type ChangeType int

const (
	Added ChangeType = iota
	Modified
	Deleted
)

func (c ChangeType) String() string {
	switch c {
	case Added:
		return "Added"
	case Modified:
		return "Modified"
	case Deleted:
		return "Deleted"
	default:
		return "Unknown"
	}
}

// FileInfo holds metadata about a file
type FileInfo struct {
	Path   string
	Size   int64
	Mtime  time.Time
	Mode   fs.FileMode
	IsDir  bool
	IsLink bool
	Target string // symlink target if IsLink
}

// Change represents a single file change
type Change struct {
	Path    string
	Type    ChangeType
	OldInfo *FileInfo
	NewInfo *FileInfo
}

// Result holds the diff comparison result
type Result struct {
	Base    string // e.g., "v3"
	Target  string // e.g., "current" or "v5"
	Changes []Change
}

// Summary returns counts of each change type
func (r *Result) Summary() (added, modified, deleted int) {
	for _, c := range r.Changes {
		switch c.Type {
		case Added:
			added++
		case Modified:
			modified++
		case Deleted:
			deleted++
		}
	}
	return
}

// Differ handles diff operations between checkpoints
type Differ struct {
	store        *store.Manager
	storeObj     *store.Store
	mountedPaths []string // track mounted paths for cleanup
}

// NewDiffer creates a new Differ for a specific store
func NewDiffer(storeManager *store.Manager, s *store.Store) *Differ {
	return &Differ{
		store:    storeManager,
		storeObj: s,
	}
}

// defaultIgnore contains patterns to skip during diff
var defaultIgnore = []string{
	".DS_Store",
	".Spotlight-V100",
	".Trashes",
	".fseventsd",
	".TemporaryItems",
	"._*",
}

// shouldIgnore checks if a path should be ignored
func shouldIgnore(path string) bool {
	base := filepath.Base(path)
	for _, pattern := range defaultIgnore {
		if strings.HasPrefix(pattern, "*") {
			// Simple suffix match for patterns like "._*"
			if strings.HasPrefix(base, pattern[0:len(pattern)-1]) {
				return true
			}
		} else if base == pattern {
			return true
		}
	}
	return false
}

// Diff compares two versions (v1 vs v2, or v1 vs current)
// If toVersion is 0, compares against current (live CWD)
func (d *Differ) Diff(fromVersion, toVersion int) (*Result, error) {
	result := &Result{}

	// Determine paths and labels
	var fromPath, toPath string
	var fromCleanup, toCleanup func() error

	// Mount fromVersion checkpoint
	var err error
	fromPath, fromCleanup, err = d.mountCheckpoint(fromVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to mount v%d: %w", fromVersion, err)
	}
	if fromCleanup != nil {
		defer fromCleanup()
	}
	result.Base = fmt.Sprintf("v%d", fromVersion)

	// Get toPath (either mount checkpoint or use live CWD)
	if toVersion == 0 {
		// Compare against current (live mount)
		if !d.store.IsMounted(d.storeObj.MountPath) {
			return nil, fmt.Errorf("store must be mounted to diff against current state")
		}
		toPath = d.storeObj.MountPath
		result.Target = "current"
	} else {
		toPath, toCleanup, err = d.mountCheckpoint(toVersion)
		if err != nil {
			return nil, fmt.Errorf("failed to mount v%d: %w", toVersion, err)
		}
		if toCleanup != nil {
			defer toCleanup()
		}
		result.Target = fmt.Sprintf("v%d", toVersion)
	}

	// Compare directories
	result.Changes, err = d.compareDirectories(fromPath, toPath)
	if err != nil {
		return nil, fmt.Errorf("failed to compare directories: %w", err)
	}

	return result, nil
}

// mountCheckpoint creates a temp bundle from checkpoint bands and mounts it
// Returns the mount path and a cleanup function
func (d *Differ) mountCheckpoint(version int) (string, func() error, error) {
	checkpointsPath := d.store.GetCheckpointsPath(d.storeObj)
	checkpointPath := filepath.Join(checkpointsPath, fmt.Sprintf("v%d", version))

	// Verify checkpoint exists
	if _, err := os.Stat(checkpointPath); os.IsNotExist(err) {
		return "", nil, fmt.Errorf("checkpoint v%d not found", version)
	}

	// Create temp bundle directory
	timestamp := time.Now().UnixNano()
	tmpBundle := filepath.Join(os.TempDir(), fmt.Sprintf("agentfs-diff-v%d-%d.sparsebundle", version, timestamp))
	mountPoint := filepath.Join(os.TempDir(), fmt.Sprintf("agentfs-diff-v%d-%d-mount", version, timestamp))

	// Create bundle structure
	if err := os.MkdirAll(tmpBundle, 0755); err != nil {
		return "", nil, fmt.Errorf("failed to create temp bundle directory: %w", err)
	}

	// Copy metadata from original bundle (Info.plist and token)
	if err := d.createTempBundle(tmpBundle, checkpointPath); err != nil {
		os.RemoveAll(tmpBundle)
		return "", nil, fmt.Errorf("failed to create temp bundle: %w", err)
	}

	// Create mount point
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		os.RemoveAll(tmpBundle)
		return "", nil, fmt.Errorf("failed to create mount point: %w", err)
	}

	// Mount the temp bundle
	cmd := exec.Command("hdiutil", "attach", tmpBundle,
		"-mountpoint", mountPoint,
		"-nobrowse",
		"-quiet")
	output, err := cmd.CombinedOutput()
	if err != nil {
		os.RemoveAll(tmpBundle)
		os.RemoveAll(mountPoint)
		return "", nil, fmt.Errorf("failed to mount temp bundle: %w\n%s", err, output)
	}

	// Return cleanup function
	cleanup := func() error {
		return d.unmountCheckpoint(mountPoint, tmpBundle)
	}

	return mountPoint, cleanup, nil
}

// createTempBundle creates a temp sparse bundle structure from checkpoint bands
func (d *Differ) createTempBundle(tmpBundle, checkpointPath string) error {
	// Copy Info.plist from original bundle
	origBundle := d.storeObj.BundlePath
	infoPlist := filepath.Join(origBundle, "Info.plist")
	if err := copyFile(infoPlist, filepath.Join(tmpBundle, "Info.plist")); err != nil {
		return fmt.Errorf("failed to copy Info.plist: %w", err)
	}

	// Copy token file if it exists
	tokenFile := filepath.Join(origBundle, "token")
	if _, err := os.Stat(tokenFile); err == nil {
		if err := copyFile(tokenFile, filepath.Join(tmpBundle, "token")); err != nil {
			return fmt.Errorf("failed to copy token: %w", err)
		}
	}

	// Clone bands from checkpoint using APFS reflink (cp -Rc)
	// This is instant and uses no extra disk space on APFS
	bandsDir := filepath.Join(tmpBundle, "bands")
	cmd := exec.Command("/bin/cp", "-Rc", checkpointPath+"/", bandsDir+"/")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to clone bands: %w\n%s", err, output)
	}

	return nil
}

// unmountCheckpoint unmounts and cleans up a temp bundle
func (d *Differ) unmountCheckpoint(mountPoint, tmpBundle string) error {
	// Unmount
	cmd := exec.Command("hdiutil", "detach", mountPoint, "-quiet")
	if err := cmd.Run(); err != nil {
		// Try force detach
		cmd = exec.Command("hdiutil", "detach", mountPoint, "-force", "-quiet")
		cmd.Run()
	}

	// Remove mount point directory
	os.RemoveAll(mountPoint)

	// Remove temp bundle
	os.RemoveAll(tmpBundle)

	return nil
}

// compareDirectories walks both directories and compares files
func (d *Differ) compareDirectories(dir1, dir2 string) ([]Change, error) {
	files1, err := d.walkDirectory(dir1)
	if err != nil {
		return nil, fmt.Errorf("failed to walk %s: %w", dir1, err)
	}

	files2, err := d.walkDirectory(dir2)
	if err != nil {
		return nil, fmt.Errorf("failed to walk %s: %w", dir2, err)
	}

	var changes []Change

	// Find modified and deleted files (in files1 but different or missing in files2)
	for path, info1 := range files1 {
		if info2, exists := files2[path]; exists {
			// Check if modified (different size or mtime)
			if info1.Size != info2.Size || !info1.Mtime.Equal(info2.Mtime) {
				changes = append(changes, Change{
					Path:    path,
					Type:    Modified,
					OldInfo: info1,
					NewInfo: info2,
				})
			}
			// Check symlink target changes
			if info1.IsLink && info2.IsLink && info1.Target != info2.Target {
				changes = append(changes, Change{
					Path:    path,
					Type:    Modified,
					OldInfo: info1,
					NewInfo: info2,
				})
			}
		} else {
			// File deleted (exists in dir1 but not dir2)
			changes = append(changes, Change{
				Path:    path,
				Type:    Deleted,
				OldInfo: info1,
			})
		}
	}

	// Find added files (in files2 but not in files1)
	for path, info2 := range files2 {
		if _, exists := files1[path]; !exists {
			changes = append(changes, Change{
				Path:    path,
				Type:    Added,
				NewInfo: info2,
			})
		}
	}

	// Sort changes by path for consistent output
	sort.Slice(changes, func(i, j int) bool {
		return changes[i].Path < changes[j].Path
	})

	return changes, nil
}

// walkDirectory walks a directory and returns file info map
func (d *Differ) walkDirectory(root string) (map[string]*FileInfo, error) {
	files := make(map[string]*FileInfo)

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			// Skip permission errors
			return nil
		}

		// Get relative path
		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}

		// Skip root directory itself
		if relPath == "." {
			return nil
		}

		// Skip ignored files
		if shouldIgnore(relPath) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip directories (we only track files)
		if entry.IsDir() {
			return nil
		}

		// Get file info
		info, err := entry.Info()
		if err != nil {
			return nil
		}

		fileInfo := &FileInfo{
			Path:  relPath,
			Size:  info.Size(),
			Mtime: info.ModTime(),
			Mode:  info.Mode(),
			IsDir: info.IsDir(),
		}

		// Check if symlink
		if info.Mode()&os.ModeSymlink != 0 {
			fileInfo.IsLink = true
			target, err := os.Readlink(path)
			if err == nil {
				fileInfo.Target = target
			}
		}

		files[relPath] = fileInfo
		return nil
	})

	return files, err
}

// ShowFileDiff shows the diff of a specific file between two paths
func (d *Differ) ShowFileDiff(path1, path2, relPath string) error {
	file1 := filepath.Join(path1, relPath)
	file2 := filepath.Join(path2, relPath)

	// Check if files exist
	stat1, err1 := os.Stat(file1)
	stat2, err2 := os.Stat(file2)

	// Handle missing files
	if os.IsNotExist(err1) && os.IsNotExist(err2) {
		return fmt.Errorf("file does not exist in either version")
	}

	// For added files, show as all additions
	if os.IsNotExist(err1) {
		file1 = "/dev/null"
	}

	// For deleted files, show as all deletions
	if os.IsNotExist(err2) {
		file2 = "/dev/null"
	}

	// Check if binary (if file exists)
	if err1 == nil && isBinaryFile(file1) {
		size1 := stat1.Size()
		size2 := int64(0)
		if err2 == nil {
			size2 = stat2.Size()
		}
		fmt.Printf("Binary file %s changed (%s → %s)\n", relPath, humanizeBytes(size1), humanizeBytes(size2))
		return nil
	}
	if err2 == nil && isBinaryFile(file2) {
		size1 := int64(0)
		if err1 == nil {
			size1 = stat1.Size()
		}
		size2 := stat2.Size()
		fmt.Printf("Binary file %s changed (%s → %s)\n", relPath, humanizeBytes(size1), humanizeBytes(size2))
		return nil
	}

	// Use native diff for text files
	cmd := exec.Command("diff", "-u",
		"--label", "a/"+relPath,
		"--label", "b/"+relPath,
		file1, file2)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run() // Ignore exit code (diff returns 1 if files differ)

	return nil
}

// DiffFile performs a diff of a specific file between versions
func (d *Differ) DiffFile(fromVersion, toVersion int, relPath string) error {
	// Mount fromVersion
	fromPath, fromCleanup, err := d.mountCheckpoint(fromVersion)
	if err != nil {
		return fmt.Errorf("failed to mount v%d: %w", fromVersion, err)
	}
	if fromCleanup != nil {
		defer fromCleanup()
	}

	// Get toPath
	var toPath string
	if toVersion == 0 {
		if !d.store.IsMounted(d.storeObj.MountPath) {
			return fmt.Errorf("store must be mounted to diff against current state")
		}
		toPath = d.storeObj.MountPath
	} else {
		var toCleanup func() error
		toPath, toCleanup, err = d.mountCheckpoint(toVersion)
		if err != nil {
			return fmt.Errorf("failed to mount v%d: %w", toVersion, err)
		}
		if toCleanup != nil {
			defer toCleanup()
		}
	}

	// Show diff
	return d.ShowFileDiff(fromPath, toPath, relPath)
}

// isBinaryFile checks if a file is binary by looking for null bytes
func isBinaryFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	// Check first 8KB for null bytes
	buf := make([]byte, 8192)
	n, err := f.Read(buf)
	if err != nil {
		return false
	}

	for i := 0; i < n; i++ {
		if buf[i] == 0 {
			return true
		}
	}
	return false
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

// humanizeBytes formats bytes in human-readable form
func humanizeBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
