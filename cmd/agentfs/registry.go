package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/sleexyz/agentfs/internal/registry"
	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
)

var registryCmd = &cobra.Command{
	Use:   "registry",
	Short: "Manage the store registry",
	Long: `Manage the global store registry.

The registry tracks all stores created with 'agentfs init' and is used
by 'agentfs mount --all' to remount stores after reboot.

Commands:
  list    List all registered stores
  remove  Remove a store from the registry
  clean   Remove stale entries (stores that no longer exist)`,
}

var registryListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all registered stores",
	Long:  `List all stores in the registry with their paths and settings.`,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		reg, err := registry.Open()
		if err != nil {
			exitWithError(ExitError, "failed to open registry: %v", err)
		}
		defer reg.Close()

		stores, err := reg.List()
		if err != nil {
			exitWithError(ExitError, "failed to list stores: %v", err)
		}

		if len(stores) == 0 {
			fmt.Println("No stores registered.")
			return
		}

		if jsonFlag {
			type jsonStore struct {
				StorePath     string  `json:"store_path"`
				MountPoint    string  `json:"mount_point"`
				AutoMount     bool    `json:"auto_mount"`
				CreatedAt     string  `json:"created_at"`
				LastMountedAt *string `json:"last_mounted_at,omitempty"`
			}
			var output []jsonStore
			for _, s := range stores {
				js := jsonStore{
					StorePath:  s.StorePath,
					MountPoint: s.MountPoint,
					AutoMount:  s.AutoMount,
					CreatedAt:  s.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
				}
				if s.LastMountedAt != nil {
					t := s.LastMountedAt.Format("2006-01-02T15:04:05Z07:00")
					js.LastMountedAt = &t
				}
				output = append(output, js)
			}
			json.NewEncoder(os.Stdout).Encode(output)
			return
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "STORE\tMOUNT\tAUTO-MOUNT\tLAST MOUNTED")
		for _, s := range stores {
			autoMount := "yes"
			if !s.AutoMount {
				autoMount = "no"
			}
			lastMounted := "-"
			if s.LastMountedAt != nil {
				lastMounted = humanize.Time(*s.LastMountedAt)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", s.StorePath, s.MountPoint, autoMount, lastMounted)
		}
		w.Flush()
	},
}

var registryRemoveCmd = &cobra.Command{
	Use:   "remove <store>",
	Short: "Remove a store from the registry",
	Long: `Remove a store from the registry without deleting the store itself.

This only removes the registry entry - the store directory remains intact.
Use 'agentfs delete' to fully delete a store.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		storeName := args[0]

		// Resolve the store path
		var storePath string
		if filepath.IsAbs(storeName) {
			storePath = storeName
		} else {
			// Try adding .fs suffix if needed
			if filepath.Ext(storeName) != ".fs" {
				storeName = storeName + ".fs"
			}
			cwd, err := os.Getwd()
			if err != nil {
				exitWithError(ExitError, "failed to get current directory: %v", err)
			}
			storePath = filepath.Join(cwd, storeName)
		}

		reg, err := registry.Open()
		if err != nil {
			exitWithError(ExitError, "failed to open registry: %v", err)
		}
		defer reg.Close()

		if err := reg.Unregister(storePath); err != nil {
			if err == registry.ErrNotFound {
				exitWithError(ExitError, "store not found in registry: %s", storePath)
			}
			exitWithError(ExitError, "failed to remove store: %v", err)
		}

		fmt.Printf("Removed %s from registry\n", filepath.Base(storePath))
	},
}

var registryCleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove stale registry entries",
	Long:  `Remove entries for stores that no longer exist on disk.`,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		reg, err := registry.Open()
		if err != nil {
			exitWithError(ExitError, "failed to open registry: %v", err)
		}
		defer reg.Close()

		removed, err := reg.RemoveStale()
		if err != nil {
			exitWithError(ExitError, "failed to clean registry: %v", err)
		}

		if len(removed) == 0 {
			fmt.Println("No stale entries found.")
			return
		}

		fmt.Printf("Removed %d stale entries:\n", len(removed))
		for _, path := range removed {
			fmt.Printf("  - %s\n", path)
		}
	},
}

func init() {
	registryCmd.AddCommand(registryListCmd)
	registryCmd.AddCommand(registryRemoveCmd)
	registryCmd.AddCommand(registryCleanCmd)
	rootCmd.AddCommand(registryCmd)
}
