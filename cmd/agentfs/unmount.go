package main

import (
	"fmt"

	"github.com/agentfs/agentfs/internal/context"
	"github.com/spf13/cobra"
)

var unmountCmd = &cobra.Command{
	Use:   "unmount",
	Short: "Unmount the current store",
	Long: `Unmount the current store.

Detects the store from the current context (.agentfs file).
The mount directory will be removed after unmounting.
The store (foo.fs/) remains intact.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		// Resolve store from context
		storePath, err := context.MustResolveStore(storeFlag, "")
		if err != nil {
			exitWithError(ExitUsageError, "%v", err)
		}

		// Get store info
		s, err := storeManager.GetFromPath(storePath)
		if err != nil {
			exitWithError(ExitError, "%v", err)
		}
		if s == nil {
			exitWithError(ExitStoreNotFound, "store not found")
		}

		// Check if mounted
		if !storeManager.IsMounted(s.MountPath) {
			fmt.Printf("Store '%s' is not mounted\n", s.Name)
			return
		}

		// Unmount the store
		if err := storeManager.Unmount(s); err != nil {
			exitWithError(ExitMountFailed, "%v", err)
		}

		fmt.Printf("Unmounted %s\n", s.Name)
	},
}

func init() {
	rootCmd.AddCommand(unmountCmd)
}
