package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/agentfs/agentfs/internal/context"
	"github.com/agentfs/agentfs/internal/db"
	"github.com/agentfs/agentfs/internal/store"
	"github.com/spf13/cobra"
)

var (
	initSize string
)

var initCmd = &cobra.Command{
	Use:   "init [name]",
	Short: "Create and mount a new store",
	Long: `Create a new sparse bundle store and mount it.

The store will be created as <name>.fs/ in the current directory
and mounted at ./<name>/.

A .agentfs context file will be created in the mount directory.

If no name is provided, you will be prompted for one.`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var name string

		if len(args) > 0 {
			name = args[0]
		} else {
			// Interactive mode - prompt for name
			reader := bufio.NewReader(os.Stdin)
			fmt.Print("Name: ")
			input, err := reader.ReadString('\n')
			if err != nil {
				exitWithError(ExitError, "failed to read input: %v", err)
			}
			name = strings.TrimSpace(input)
			if name == "" {
				exitWithError(ExitUsageError, "name is required")
			}
		}

		// Validate name
		if strings.Contains(name, "/") || strings.Contains(name, "\\") {
			exitWithError(ExitUsageError, "name cannot contain path separators")
		}
		if strings.HasSuffix(name, ".fs") {
			name = strings.TrimSuffix(name, ".fs")
		}

		opts := store.CreateOpts{
			Size: initSize,
		}

		s, err := storeManager.Create(name, opts)
		if err != nil {
			exitWithError(ExitError, "%v", err)
		}

		// Initialize per-store database
		database, err := db.OpenFromStorePath(s.StorePath)
		if err != nil {
			// Clean up on failure
			storeManager.Delete(s)
			exitWithError(ExitError, "failed to create database: %v", err)
		}
		defer database.Close()

		// Record store info in database
		if err := database.InitStore(name, s.SizeBytes); err != nil {
			storeManager.Delete(s)
			exitWithError(ExitError, "failed to initialize store database: %v", err)
		}

		// Create .agentfs context file in the mount point
		if err := context.WriteContext(s.MountPath, s.StorePath); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to create .agentfs file: %v\n", err)
		}

		fmt.Printf("Created %s/\n", name+".fs")
		fmt.Printf("Mounted at ./%s/\n", name)
	},
}

func init() {
	initCmd.Flags().StringVar(&initSize, "size", "50G", "size of the sparse bundle")
	rootCmd.AddCommand(initCmd)
}
