package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/sleexyz/agentfs/internal/backup"
	agentfsctx "github.com/sleexyz/agentfs/internal/context"
	"github.com/sleexyz/agentfs/internal/db"
	"github.com/sleexyz/agentfs/internal/registry"
	"github.com/spf13/cobra"
)

var unmanageCmd = &cobra.Command{
	Use:   "unmanage [dir]",
	Short: "Convert an agentfs-managed directory back to a regular directory",
	Long: `Convert an agentfs-managed directory back to a regular directory.

This command:
1. Copies all files from the mounted store to a temp directory
2. Unmounts and deletes the store (dir.fs/)
3. Moves the files to the original location

All checkpoints will be deleted. Use from inside the mount or specify the directory.

Examples:
  agentfs unmanage          # From inside mount, uses context
  agentfs unmanage myapp    # Explicit directory`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var dirPath string
		var storePath string

		if len(args) > 0 {
			// Explicit directory provided
			var err error
			dirPath, err = filepath.Abs(args[0])
			if err != nil {
				exitWithError(ExitError, "failed to resolve path: %v", err)
			}

			// Derive store path
			name := filepath.Base(dirPath)
			parentDir := filepath.Dir(dirPath)
			storePath = filepath.Join(parentDir, name+".fs")
		} else {
			// Use context to find store
			cwd, err := os.Getwd()
			if err != nil {
				exitWithError(ExitError, "failed to get current directory: %v", err)
			}

			ctx, err := agentfsctx.FindContext(cwd)
			if err != nil {
				exitWithError(ExitError, "failed to find context: %v", err)
			}
			if ctx == nil {
				exitWithError(ExitUsageError, "not in an agentfs-managed directory. Use 'agentfs unmanage <dir>'")
			}

			storePath = ctx.StorePath
			name := agentfsctx.StoreNameFromPath(storePath)
			dirPath = filepath.Join(filepath.Dir(storePath), name)
		}

		runUnmanage(dirPath, storePath)
	},
}

func runUnmanage(dirPath, storePath string) {
	name := filepath.Base(dirPath)

	// === VALIDATION ===

	// Check if store exists
	if _, err := os.Stat(storePath); os.IsNotExist(err) {
		exitWithError(ExitError, "not an agentfs-managed directory (%s not found)", name+".fs")
	}

	// Get store
	s, err := storeManager.GetFromPath(storePath)
	if err != nil {
		exitWithError(ExitError, "failed to get store: %v", err)
	}
	if s == nil {
		exitWithError(ExitError, "not a valid agentfs store")
	}

	// Check if mounted
	if !storeManager.IsMounted(dirPath) {
		exitWithError(ExitError, "store not mounted. Mount first with 'agentfs mount' or delete with 'agentfs delete'")
	}

	// Get checkpoint count
	database, err := db.OpenFromStorePath(storePath)
	var checkpointCount int
	if err == nil {
		checkpointCount, _ = database.CountCheckpoints()
		database.Close()
	}

	// === CONFIRMATION ===
	fmt.Printf("This will delete %s and", name+".fs")
	if checkpointCount > 0 {
		fmt.Printf(" all %d checkpoints.\n", checkpointCount)
	} else {
		fmt.Println(" all data in the store.")
	}
	fmt.Println("Your files will be preserved as a regular directory.")

	if !confirmPrompt("Continue?") {
		fmt.Println("Cancelled")
		return
	}

	// === COPY OUT ===
	fmt.Println("Copying files out...")

	tempDir, err := os.MkdirTemp("", "agentfs-unmanage-")
	if err != nil {
		exitWithError(ExitError, "failed to create temp directory: %v", err)
	}

	// Use cp -R to preserve symlinks and permissions
	// Exclude .agentfs file
	cmd := exec.Command("rsync", "-a", "--exclude", ".agentfs", dirPath+"/", tempDir+"/")
	output, err := cmd.CombinedOutput()
	if err != nil {
		os.RemoveAll(tempDir)
		exitWithError(ExitError, "failed to copy files: %v\n%s", err, output)
	}

	// === VERIFY ===
	fmt.Println("Verifying...")

	// Count files excluding .agentfs
	srcCount, srcSize := countFilesExcluding(dirPath, ".agentfs")
	dstCount, dstSize := countFilesAndSize(tempDir)

	if srcCount != dstCount {
		os.RemoveAll(tempDir)
		exitWithError(ExitError, "verification failed: file count mismatch (%d vs %d). Store unchanged.", srcCount, dstCount)
	}
	if srcSize != dstSize {
		os.RemoveAll(tempDir)
		exitWithError(ExitError, "verification failed: size mismatch (%d vs %d bytes). Store unchanged.", srcSize, dstSize)
	}

	fmt.Printf("  Files: %d ✓\n", srcCount)
	fmt.Printf("  Size: %s ✓\n", backup.FormatSize(srcSize))

	// === UNMOUNT ===
	fmt.Println("Unmounting store...")

	cmd = exec.Command("hdiutil", "detach", dirPath)
	output, err = cmd.CombinedOutput()
	if err != nil {
		os.RemoveAll(tempDir)
		exitWithError(ExitError, "failed to unmount: %v\n%s", err, output)
	}

	// Remove mount point directory (should be empty after unmount)
	os.Remove(dirPath)

	// === RESTORE ===
	fmt.Println("Restoring files...")

	// Move temp contents to original location
	if err := os.Rename(tempDir, dirPath); err != nil {
		// If rename fails, try copy
		cmd := exec.Command("cp", "-R", tempDir+"/.", dirPath+"/")
		if output, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "error: failed to restore files: %v\n%s\n", err, output)
			fmt.Fprintf(os.Stderr, "Files preserved in: %s\n", tempDir)
			fmt.Fprintf(os.Stderr, "Store preserved at: %s\n", storePath)
			os.Exit(ExitError)
		}
		os.RemoveAll(tempDir)
	}

	// === DELETE STORE ===
	fmt.Println("Deleting store...")

	if err := os.RemoveAll(storePath); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to delete store: %v\n", err)
		fmt.Fprintf(os.Stderr, "You may need to manually delete: %s\n", storePath)
	}

	// === UNREGISTER ===
	reg, err := registry.Open()
	if err == nil {
		defer reg.Close()
		if err := reg.Unregister(storePath); err != nil && err != registry.ErrNotFound {
			fmt.Fprintf(os.Stderr, "warning: failed to unregister store: %v\n", err)
		}
	}

	// === SUCCESS ===
	fmt.Println()
	fmt.Printf("✓ Converted %s back to regular directory\n", name)
	if checkpointCount > 0 {
		fmt.Printf("  Deleted %s and %d checkpoints\n", name+".fs", checkpointCount)
	} else {
		fmt.Printf("  Deleted %s\n", name+".fs")
	}
}

func countFilesExcluding(dir, exclude string) (int, int64) {
	var count int
	var size int64
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		// Skip excluded file
		if filepath.Base(path) == exclude {
			return nil
		}
		count++
		size += info.Size()
		return nil
	})
	return count, size
}

func init() {
	rootCmd.AddCommand(unmanageCmd)
}
