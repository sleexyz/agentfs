package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var openCmd = &cobra.Command{
	Use:   "open <name>",
	Short: "Mount an existing store",
	Long:  `Mount an existing sparse bundle store.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]

		s, err := storeManager.Get(name)
		if err != nil {
			exitWithError(ExitError, "%v", err)
		}
		if s == nil {
			exitWithError(ExitStoreNotFound, "store '%s' not found", name)
		}

		if err := storeManager.Mount(name); err != nil {
			exitWithError(ExitMountFailed, "%v", err)
		}

		fmt.Printf("Mounted at %s\n", s.MountPath)
	},
}

func init() {
	rootCmd.AddCommand(openCmd)
}
