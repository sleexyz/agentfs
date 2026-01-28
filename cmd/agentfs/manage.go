package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/agentfs/agentfs/internal/backup"
	"github.com/agentfs/agentfs/internal/context"
	"github.com/agentfs/agentfs/internal/db"
	"github.com/agentfs/agentfs/internal/registry"
	"github.com/agentfs/agentfs/internal/store"
	"github.com/spf13/cobra"
)

var (
	manageCleanup bool
)

var manageCmd = &cobra.Command{
	Use:   "manage <dir>",
	Short: "Convert an existing directory to agentfs management",
	Long: `Convert an existing directory to an agentfs-managed directory.

This command:
1. Creates a new store (dir.fs/)
2. Copies all files from dir/ to the store
3. Backs up the original to ~/.agentfs/backups/
4. Mounts the store at the original location

The original directory is safely backed up until you run:
  agentfs manage --cleanup <dir>

Examples:
  agentfs manage myapp          # Convert myapp/ to agentfs-managed
  agentfs manage ./path/to/app  # Convert with path
  agentfs manage --cleanup myapp # Remove backup after verification`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		dirPath := args[0]

		// Resolve to absolute path
		absPath, err := filepath.Abs(dirPath)
		if err != nil {
			exitWithError(ExitError, "failed to resolve path: %v", err)
		}

		// Handle --cleanup flag
		if manageCleanup {
			runManageCleanup(absPath)
			return
		}

		runManage(absPath)
	},
}

func runManage(dirPath string) {
	// Extract name from path
	name := filepath.Base(dirPath)
	parentDir := filepath.Dir(dirPath)
	storePath := filepath.Join(parentDir, name+".fs")

	// === VALIDATION ===

	// 1. Directory must exist
	info, err := os.Stat(dirPath)
	if os.IsNotExist(err) {
		exitWithError(ExitError, "directory not found: %s", dirPath)
	}
	if err != nil {
		exitWithError(ExitError, "failed to stat directory: %v", err)
	}
	if !info.IsDir() {
		exitWithError(ExitError, "not a directory: %s", dirPath)
	}

	// 2. Store must not exist (not already managed)
	if _, err := os.Stat(storePath); err == nil {
		exitWithError(ExitError, "already managed (%s exists)", name+".fs")
	}

	// 3. Directory must not be inside an agentfs mount
	ctx, err := context.FindContext(dirPath)
	if err != nil {
		exitWithError(ExitError, "failed to check context: %v", err)
	}
	if ctx != nil {
		exitWithError(ExitError, "cannot manage directory inside agentfs mount")
	}

	// 4. Check for existing backup
	backupMgr, err := backup.NewManager()
	if err != nil {
		exitWithError(ExitError, "failed to initialize backup manager: %v", err)
	}

	existingBackup, err := backupMgr.GetByOriginalPath(dirPath)
	if err != nil {
		exitWithError(ExitError, "failed to check backups: %v", err)
	}
	if existingBackup != nil {
		exitWithError(ExitError, "previous backup exists for this path. Run 'agentfs manage --cleanup %s' first", name)
	}

	// === CREATE STORE ===
	fmt.Printf("Creating store %s...\n", name+".fs")

	// Create store directory structure
	if err := os.MkdirAll(storePath, 0755); err != nil {
		exitWithError(ExitError, "failed to create store directory: %v", err)
	}

	// Create checkpoints directory
	checkpointsDir := filepath.Join(storePath, "checkpoints")
	if err := os.MkdirAll(checkpointsDir, 0755); err != nil {
		cleanup(storePath, "", "")
		exitWithError(ExitError, "failed to create checkpoints directory: %v", err)
	}

	// Create sparse bundle inside store directory
	bundlePath := filepath.Join(storePath, "data.sparsebundle")
	cmd := exec.Command("hdiutil", "create",
		"-size", "50G",
		"-type", "SPARSEBUNDLE",
		"-fs", "APFS",
		"-volname", name,
		bundlePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		cleanup(storePath, "", "")
		exitWithError(ExitError, "failed to create sparse bundle: %v\n%s", err, output)
	}

	// === MOUNT AT TEMP LOCATION ===
	tempMount, err := os.MkdirTemp("", "agentfs-manage-")
	if err != nil {
		cleanup(storePath, "", "")
		exitWithError(ExitError, "failed to create temp directory: %v", err)
	}

	fmt.Println("Mounting store...")
	cmd = exec.Command("hdiutil", "attach", bundlePath, "-mountpoint", tempMount)
	output, err = cmd.CombinedOutput()
	if err != nil {
		cleanup(storePath, tempMount, "")
		exitWithError(ExitError, "failed to mount sparse bundle: %v\n%s", err, output)
	}

	// === COPY DATA ===
	fmt.Println("Copying files...")

	// Use cp -R to preserve symlinks and permissions
	// Note: trailing /. copies contents, not the directory itself
	cmd = exec.Command("cp", "-R", dirPath+"/.", tempMount+"/")
	output, err = cmd.CombinedOutput()
	if err != nil {
		unmountAndCleanup(storePath, tempMount)
		exitWithError(ExitError, "failed to copy files: %v\n%s", err, output)
	}

	// === VERIFY ===
	fmt.Println("Verifying...")
	srcCount, srcSize := countFilesAndSize(dirPath)
	dstCount, dstSize := countFilesAndSize(tempMount)

	if srcCount != dstCount {
		// Debug: show which files differ
		fmt.Fprintf(os.Stderr, "\nDebug: Finding file differences...\n")
		srcFiles := listAllFiles(dirPath)
		dstFiles := listAllFiles(tempMount)

		// Files in src but not dst
		fmt.Fprintf(os.Stderr, "Files in source but not dest:\n")
		for f := range srcFiles {
			if _, ok := dstFiles[f]; !ok {
				fmt.Fprintf(os.Stderr, "  + %s\n", f)
			}
		}
		// Files in dst but not src
		fmt.Fprintf(os.Stderr, "Files in dest but not source:\n")
		for f := range dstFiles {
			if _, ok := srcFiles[f]; !ok {
				fmt.Fprintf(os.Stderr, "  - %s\n", f)
			}
		}

		unmountAndCleanup(storePath, tempMount)
		exitWithError(ExitError, "verification failed: file count mismatch (%d vs %d). Original unchanged.", srcCount, dstCount)
	}
	if srcSize != dstSize {
		unmountAndCleanup(storePath, tempMount)
		exitWithError(ExitError, "verification failed: size mismatch (%d vs %d bytes). Original unchanged.", srcSize, dstSize)
	}

	fmt.Printf("  Files: %d ✓\n", srcCount)
	fmt.Printf("  Size: %s ✓\n", backup.FormatSize(srcSize))

	// === UNMOUNT TEMP ===
	cmd = exec.Command("hdiutil", "detach", tempMount)
	if _, err := cmd.CombinedOutput(); err != nil {
		cleanup(storePath, tempMount, "")
		exitWithError(ExitError, "failed to unmount temp: %v", err)
	}
	os.Remove(tempMount)

	// === BACKUP ORIGINAL ===
	fmt.Println("Moving original to backup...")
	backupEntry, err := backupMgr.Save(dirPath, storePath)
	if err != nil {
		cleanup(storePath, "", "")
		exitWithError(ExitError, "failed to backup original: %v", err)
	}

	// === MOUNT AT ORIGINAL LOCATION ===
	fmt.Println("Mounting store at original location...")

	// Create mount point
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		// Try to restore backup
		restoreBackupOnFailure(backupMgr, backupEntry, dirPath)
		cleanup(storePath, "", "")
		exitWithError(ExitError, "failed to create mount point: %v", err)
	}

	cmd = exec.Command("hdiutil", "attach", bundlePath, "-mountpoint", dirPath)
	output, err = cmd.CombinedOutput()
	if err != nil {
		restoreBackupOnFailure(backupMgr, backupEntry, dirPath)
		cleanup(storePath, "", dirPath)
		exitWithError(ExitError, "failed to mount at original location: %v\n%s", err, output)
	}

	// === INITIALIZE DATABASE ===
	database, err := db.OpenFromStorePath(storePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to create database: %v\n", err)
	} else {
		if err := database.InitStore(name, 50*1024*1024*1024); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to initialize store database: %v\n", err)
		}
		database.Close()
	}

	// === CREATE CONTEXT FILE ===
	if err := context.WriteContext(dirPath, storePath); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to create .agentfs file: %v\n", err)
	}

	// === REGISTER ===
	reg, err := registry.Open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to open registry: %v\n", err)
	} else {
		defer reg.Close()
		if err := reg.Register(storePath, dirPath); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to register store: %v\n", err)
		}
	}

	// === SUCCESS ===
	fmt.Println()
	fmt.Printf("✓ Converted %s/ to agentfs\n", name)
	fmt.Printf("  Backup: ~/.agentfs/backups/%s/ (%s)\n", backupEntry.ID, backup.FormatSize(backupEntry.SizeBytes))
	fmt.Printf("  Run 'agentfs manage --cleanup %s' after verifying\n", name)
}

func runManageCleanup(dirPath string) {
	name := filepath.Base(dirPath)
	parentDir := filepath.Dir(dirPath)
	storePath := filepath.Join(parentDir, name+".fs")

	backupMgr, err := backup.NewManager()
	if err != nil {
		exitWithError(ExitError, "failed to initialize backup manager: %v", err)
	}

	// Look up backup by store path
	entry, err := backupMgr.GetByStorePath(storePath)
	if err != nil {
		exitWithError(ExitError, "failed to lookup backup: %v", err)
	}
	if entry == nil {
		// Also try by original path
		entry, err = backupMgr.GetByOriginalPath(dirPath)
		if err != nil {
			exitWithError(ExitError, "failed to lookup backup: %v", err)
		}
	}

	if entry == nil {
		exitWithError(ExitError, "no backup found for %s", name)
	}

	// Confirm deletion
	if !confirmPrompt(fmt.Sprintf("Delete backup (%s)?", backup.FormatSize(entry.SizeBytes))) {
		fmt.Println("Cancelled")
		return
	}

	if err := backupMgr.Delete(entry.ID); err != nil {
		exitWithError(ExitError, "failed to delete backup: %v", err)
	}

	fmt.Println("Backup deleted")
}

func countFilesAndSize(dir string) (int, int64) {
	var count int
	var size int64
	filepath.Walk(dir, func(path string, info fs.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		// Skip macOS system files that are auto-created on mount
		rel, _ := filepath.Rel(dir, path)
		if isSystemFile(rel) {
			return nil
		}
		count++
		size += info.Size()
		return nil
	})
	return count, size
}

func isSystemFile(relPath string) bool {
	// Skip .fseventsd (macOS File System Events daemon)
	// Skip .Spotlight-V100 (Spotlight indexing)
	// Skip .Trashes (Trash folder)
	// Skip .DS_Store (Finder metadata)
	for _, prefix := range []string{".fseventsd/", ".Spotlight-V100/", ".Trashes/", ".TemporaryItems/"} {
		if len(relPath) >= len(prefix) && relPath[:len(prefix)] == prefix {
			return true
		}
	}
	return relPath == ".DS_Store"
}

func cleanup(storePath, tempMount, mountPoint string) {
	if tempMount != "" {
		os.RemoveAll(tempMount)
	}
	if mountPoint != "" {
		os.Remove(mountPoint)
	}
	if storePath != "" {
		os.RemoveAll(storePath)
	}
}

func unmountAndCleanup(storePath, tempMount string) {
	// Try to unmount
	exec.Command("hdiutil", "detach", tempMount).Run()
	cleanup(storePath, tempMount, "")
}

func restoreBackupOnFailure(backupMgr *backup.Manager, entry *backup.Entry, originalPath string) {
	// Try to restore the backup to its original location
	backupPath := backupMgr.Path(entry.ID)
	if err := os.Rename(backupPath, originalPath); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to restore backup. Manual recovery needed from ~/.agentfs/backups/%s/\n", entry.ID)
	}
}

func listAllFiles(dir string) map[string]struct{} {
	files := make(map[string]struct{})
	filepath.Walk(dir, func(path string, info fs.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		// Get relative path
		rel, _ := filepath.Rel(dir, path)
		if isSystemFile(rel) {
			return nil
		}
		files[rel] = struct{}{}
		return nil
	})
	return files
}

// ResolveStore resolves a store from manage command argument
// This works like the init command - takes a directory path
func resolveStoreForManage(dirPath string) (*store.Store, error) {
	name := filepath.Base(dirPath)
	parentDir := filepath.Dir(dirPath)
	storePath := filepath.Join(parentDir, name+".fs")

	return storeManager.GetFromPath(storePath)
}

func init() {
	manageCmd.Flags().BoolVar(&manageCleanup, "cleanup", false, "remove backup after verification")
	rootCmd.AddCommand(manageCmd)
}
