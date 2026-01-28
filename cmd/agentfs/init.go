package main

import (
	"fmt"
	"os"

	"github.com/agentfs/agentfs/internal/context"
	"github.com/agentfs/agentfs/internal/store"
	"github.com/spf13/cobra"
)

var (
	initSize  string
	initMount string
)

var initCmd = &cobra.Command{
	Use:   "init <name>",
	Short: "Create and mount a new store",
	Long: `Create a new sparse bundle store and mount it.

The store will be created at ~/.agentfs/stores/<name>/ and mounted
at the specified mount point (default: ~/projects/<name>).

A .agentfs context file will be created in the mount directory.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]

		fmt.Println("Creating store...")

		opts := store.CreateOpts{
			Size:      initSize,
			MountPath: initMount,
		}

		s, err := storeManager.Create(name, opts)
		if err != nil {
			exitWithError(ExitError, "%v", err)
		}

		fmt.Printf("Created store '%s'\n", name)
		fmt.Printf("Mounted at %s\n", s.MountPath)

		// Create .agentfs context file in the mount point
		if err := context.WriteContext(s.MountPath, name); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to create .agentfs file: %v\n", err)
		} else {
			fmt.Println("Created .agentfs")
		}
	},
}

func init() {
	initCmd.Flags().StringVar(&initSize, "size", "50G", "size of the sparse bundle")
	initCmd.Flags().StringVar(&initMount, "mount", "", "mount point (default: ~/projects/<name>)")
	rootCmd.AddCommand(initCmd)
}
