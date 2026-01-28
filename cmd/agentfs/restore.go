package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/agentfs/agentfs/internal/context"
	"github.com/spf13/cobra"
)

var restoreCmd = &cobra.Command{
	Use:   "restore <version>",
	Short: "Restore to a checkpoint",
	Long: `Restore the store to a previous checkpoint.

This will:
1. Create a checkpoint of the current state (unless --no-backup)
2. Unmount the store
3. Swap the sparse bundle bands with the checkpoint
4. Remount the store

Requires confirmation unless -f/--force is specified.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name, err := context.MustResolveStore(storeFlag, "")
		if err != nil {
			exitWithError(ExitUsageError, "%v", err)
		}

		version, err := parseVersion(args[0])
		if err != nil {
			exitWithError(ExitUsageError, "invalid version: %v", err)
		}

		// Get the target checkpoint first
		targetCp, err := cpManager.Get(name, version)
		if err != nil {
			exitWithError(ExitError, "%v", err)
		}
		if targetCp == nil {
			exitWithError(ExitCPNotFound, "checkpoint v%d not found", version)
		}

		// Get next version for pre-restore checkpoint
		nextVersion := version + 1
		if latest, _ := cpManager.GetLatest(name); latest != nil {
			nextVersion = latest.Version + 1
		}

		prompt := fmt.Sprintf("Restore to v%d? Current state will be saved as v%d.", version, nextVersion)
		if !confirmPrompt(prompt) {
			fmt.Println("Cancelled")
			return
		}

		fmt.Printf("Creating checkpoint v%d \"pre-restore\"...\n", nextVersion)
		fmt.Println("Unmounting...")
		fmt.Printf("Restoring from v%d...\n", version)
		fmt.Println("Mounting...")

		cp, duration, err := cpManager.Restore(name, version, true)
		if err != nil {
			exitWithError(ExitError, "%v", err)
		}

		if jsonFlag {
			type restoreJSON struct {
				Version    string `json:"version"`
				Message    string `json:"message,omitempty"`
				DurationMs int64  `json:"duration_ms"`
			}

			output := restoreJSON{
				Version:    fmt.Sprintf("v%d", cp.Version),
				Message:    cp.Message,
				DurationMs: duration.Milliseconds(),
			}

			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			enc.Encode(output)
			return
		}

		output := fmt.Sprintf("Restored to v%d", cp.Version)
		if cp.Message != "" {
			output += fmt.Sprintf(" %q", cp.Message)
		}
		output += fmt.Sprintf(" (%dms)", duration.Milliseconds())
		fmt.Println(output)
	},
}

func init() {
	rootCmd.AddCommand(restoreCmd)
}
