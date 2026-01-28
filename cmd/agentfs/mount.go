package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sleexyz/agentfs/internal/context"
	"github.com/sleexyz/agentfs/internal/registry"
	"github.com/spf13/cobra"
)

var mountAllFlag bool

var mountCmd = &cobra.Command{
	Use:   "mount [name]",
	Short: "Mount a store",
	Long: `Mount an existing store.

If no name is provided, uses context from .agentfs file or auto-detects
if there's exactly one *.fs/ directory in the current directory.

Use --all to mount all registered stores (for auto-remount on login).

Examples:
  agentfs mount foo      # Mount foo.fs as foo/
  agentfs mount          # Auto-detect from context or single store
  agentfs mount --all    # Mount all registered stores`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if mountAllFlag {
			mountAll()
			return
		}

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

		// Update last_mounted_at in registry
		reg, err := registry.Open()
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to open registry: %v\n", err)
		} else {
			defer reg.Close()
			reg.UpdateLastMounted(s.StorePath)
		}

		fmt.Printf("Mounted at ./%s/\n", s.Name)
	},
}

func mountAll() {
	reg, err := registry.Open()
	if err != nil {
		exitWithError(ExitError, "failed to open registry: %v", err)
	}
	defer reg.Close()

	stores, err := reg.GetAutoMountStores()
	if err != nil {
		exitWithError(ExitError, "failed to get stores: %v", err)
	}

	if len(stores) == 0 {
		fmt.Println("No stores registered. Use 'agentfs init' to create a store.")
		return
	}

	mounted := 0
	skipped := 0

	for _, regStore := range stores {
		// Check if store path exists
		if _, err := os.Stat(regStore.StorePath); os.IsNotExist(err) {
			fmt.Printf("Skipping %s (not found)\n", filepath.Base(regStore.StorePath))
			skipped++
			continue
		}

		// Get store info
		s, err := storeManager.GetFromPath(regStore.StorePath)
		if err != nil {
			fmt.Printf("Skipping %s (error: %v)\n", filepath.Base(regStore.StorePath), err)
			skipped++
			continue
		}
		if s == nil {
			fmt.Printf("Skipping %s (not found)\n", filepath.Base(regStore.StorePath))
			skipped++
			continue
		}

		// Check if already mounted
		if storeManager.IsMounted(s.MountPath) {
			// Already mounted, just update timestamp
			reg.UpdateLastMounted(s.StorePath)
			continue
		}

		// Mount the store
		fmt.Printf("Mounting %s... ", filepath.Base(regStore.StorePath))
		if err := storeManager.Mount(s); err != nil {
			fmt.Printf("failed: %v\n", err)
			skipped++
			continue
		}
		fmt.Println("done")
		mounted++

		// Update last_mounted_at
		reg.UpdateLastMounted(s.StorePath)
	}

	if skipped > 0 {
		fmt.Printf("Mounted %d stores (%d skipped)\n", mounted, skipped)
	} else if mounted > 0 {
		fmt.Printf("Mounted %d stores\n", mounted)
	} else {
		fmt.Println("All stores already mounted")
	}
}

func init() {
	mountCmd.Flags().BoolVar(&mountAllFlag, "all", false, "mount all registered stores")
	rootCmd.AddCommand(mountCmd)
}
