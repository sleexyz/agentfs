package main

import (
	"fmt"

	"github.com/agentfs/agentfs/internal/context"
	"github.com/spf13/cobra"
)

var mountCmd = &cobra.Command{
	Use:   "mount [name]",
	Short: "Mount a store",
	Long: `Mount an existing store.

If no name is provided, uses context from .agentfs file or auto-detects
if there's exactly one *.fs/ directory in the current directory.

Examples:
  agentfs mount foo      # Mount foo.fs as foo/
  agentfs mount          # Auto-detect from context or single store`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var storePath string
		var err error

		if len(args) > 0 {
			// Explicit name provided
			storePath, err = context.ResolveStore(args[0], "")
		} else {
			// Try to auto-detect
			storePath, err = context.ResolveStore(storeFlag, "")
		}

		if err != nil {
			exitWithError(ExitError, "%v", err)
		}
		if storePath == "" {
			exitWithError(ExitUsageError, "no store specified. Use 'agentfs mount <name>' or run from a store directory")
		}

		// Get store info
		s, err := storeManager.GetFromPath(storePath)
		if err != nil {
			exitWithError(ExitError, "%v", err)
		}
		if s == nil {
			exitWithError(ExitStoreNotFound, "store not found: %s", storePath)
		}

		// Check if already mounted
		if storeManager.IsMounted(s.MountPath) {
			fmt.Printf("Already mounted at ./%s/\n", s.Name)
			return
		}

		// Mount the store
		if err := storeManager.Mount(s); err != nil {
			exitWithError(ExitMountFailed, "%v", err)
		}

		// Write context file in mount directory
		if err := context.WriteContext(s.MountPath, s.StorePath); err != nil {
			fmt.Printf("warning: failed to create .agentfs file: %v\n", err)
		}

		fmt.Printf("Mounted at ./%s/\n", s.Name)
	},
}

func init() {
	rootCmd.AddCommand(mountCmd)
}
