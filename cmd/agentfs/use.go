package main

import (
	"fmt"
	"os"

	"github.com/agentfs/agentfs/internal/context"
	"github.com/spf13/cobra"
)

var useCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Set context for current directory",
	Long: `Set the store context for the current directory.

Creates a .agentfs file in the current directory containing the store name.
This file is used by other commands to determine which store to use.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]

		// Verify store exists
		s, err := storeManager.Get(name)
		if err != nil {
			exitWithError(ExitError, "%v", err)
		}
		if s == nil {
			exitWithError(ExitStoreNotFound, "store '%s' not found", name)
		}

		// Get current directory
		cwd, err := os.Getwd()
		if err != nil {
			exitWithError(ExitError, "failed to get current directory: %v", err)
		}

		// Write context file
		if err := context.WriteContext(cwd, name); err != nil {
			exitWithError(ExitError, "failed to create .agentfs file: %v", err)
		}

		fmt.Println("Created .agentfs")
	},
}

func init() {
	rootCmd.AddCommand(useCmd)
}
