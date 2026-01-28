package main

import (
	"fmt"
	"path/filepath"

	"github.com/agentfs/agentfs/internal/context"
	"github.com/agentfs/agentfs/internal/registry"
	"github.com/spf13/cobra"
)

var unmountAllFlag bool

var unmountCmd = &cobra.Command{
	Use:   "unmount",
	Short: "Unmount the current store",
	Long: `Unmount the current store.

Detects the store from the current context (.agentfs file).
The mount directory will be removed after unmounting.
The store (foo.fs/) remains intact.

Use --all to unmount all currently mounted stores.

Examples:
  agentfs unmount        # Unmount current store
  agentfs unmount --all  # Unmount all mounted stores`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		if unmountAllFlag {
			unmountAll()
			return
		}

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

func unmountAll() {
	reg, err := registry.Open()
	if err != nil {
		exitWithError(ExitError, "failed to open registry: %v", err)
	}
	defer reg.Close()

	stores, err := reg.List()
	if err != nil {
		exitWithError(ExitError, "failed to get stores: %v", err)
	}

	if len(stores) == 0 {
		fmt.Println("No stores registered.")
		return
	}

	unmounted := 0

	for _, regStore := range stores {
		// Get store info
		s, err := storeManager.GetFromPath(regStore.StorePath)
		if err != nil {
			continue
		}
		if s == nil {
			continue
		}

		// Check if mounted
		if !storeManager.IsMounted(s.MountPath) {
			continue
		}

		// Unmount the store
		fmt.Printf("Unmounting %s... ", filepath.Base(regStore.StorePath))
		if err := storeManager.Unmount(s); err != nil {
			fmt.Printf("failed: %v\n", err)
			continue
		}
		fmt.Println("done")
		unmounted++
	}

	if unmounted > 0 {
		fmt.Printf("Unmounted %d stores\n", unmounted)
	} else {
		fmt.Println("No stores were mounted")
	}
}

func init() {
	unmountCmd.Flags().BoolVar(&unmountAllFlag, "all", false, "unmount all mounted stores")
	rootCmd.AddCommand(unmountCmd)
}
