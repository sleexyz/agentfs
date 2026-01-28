package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
)

var deleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a store",
	Long: `Delete a store and all its checkpoints.

This will unmount the store (if mounted), delete all checkpoint data,
and remove the foo.fs/ directory completely.

Requires confirmation unless -f/--force is specified.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]

		// Look for the store
		s, err := storeManager.Get(name)
		if err != nil {
			exitWithError(ExitError, "%v", err)
		}
		if s == nil {
			exitWithError(ExitStoreNotFound, "store '%s' not found (looked for %s.fs/)", name, name)
		}

		// Confirmation prompt
		prompt := fmt.Sprintf("Delete %s.fs", name)
		if s.Checkpoints > 0 {
			prompt = fmt.Sprintf("Delete %s.fs and all %d checkpoints?", name, s.Checkpoints)
		} else {
			prompt += "?"
		}

		if !confirmPrompt(prompt) {
			fmt.Println("Cancelled")
			return
		}

		// Show progress
		if storeManager.IsMounted(s.MountPath) {
			fmt.Println("Unmounting...")
		}

		if err := storeManager.Delete(s); err != nil {
			exitWithError(ExitError, "%v", err)
		}

		fmt.Printf("Deleted %s/\n", filepath.Base(s.StorePath))
	},
}

func init() {
	rootCmd.AddCommand(deleteCmd)
}
