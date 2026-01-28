package main

import (
	"fmt"

	"github.com/agentfs/agentfs/internal/context"
	"github.com/spf13/cobra"
)

var closeCmd = &cobra.Command{
	Use:   "close [name]",
	Short: "Unmount a store",
	Long: `Unmount a store.

If no name is provided, uses the current context from .agentfs file.`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var name string
		var err error

		if len(args) > 0 {
			name = args[0]
		} else {
			name, err = context.MustResolveStore(storeFlag, "")
			if err != nil {
				exitWithError(ExitUsageError, "%v", err)
			}
		}

		if err := storeManager.Unmount(name); err != nil {
			exitWithError(ExitMountFailed, "%v", err)
		}

		fmt.Printf("Unmounted '%s'\n", name)
	},
}

func init() {
	rootCmd.AddCommand(closeCmd)
}
