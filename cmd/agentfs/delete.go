package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var deleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a store",
	Long: `Delete a store and all its checkpoints.

This will unmount the store (if mounted), delete all checkpoint data,
and remove the sparse bundle.

Requires confirmation unless -f/--force is specified.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]

		s, err := storeManager.Get(name)
		if err != nil {
			exitWithError(ExitError, "%v", err)
		}
		if s == nil {
			exitWithError(ExitStoreNotFound, "store '%s' not found", name)
		}

		// Get checkpoint count for confirmation message
		count, _ := cpManager.Count(name)

		prompt := fmt.Sprintf("Delete store '%s'", name)
		if count > 0 {
			prompt = fmt.Sprintf("Delete store '%s' and all %d checkpoints?", name, count)
		} else {
			prompt += "?"
		}

		if !confirmPrompt(prompt) {
			fmt.Println("Cancelled")
			return
		}

		if err := storeManager.Delete(name); err != nil {
			exitWithError(ExitError, "%v", err)
		}

		fmt.Printf("Deleted '%s'\n", name)
	},
}

func init() {
	rootCmd.AddCommand(deleteCmd)
}
