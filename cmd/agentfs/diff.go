package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/agentfs/agentfs/internal/context"
	"github.com/spf13/cobra"
)

var diffCmd = &cobra.Command{
	Use:   "diff [version] [version2]",
	Short: "Show changes between checkpoints",
	Long: `Show changes between two checkpoints or between current state and a checkpoint.

Usage:
  agentfs diff v3         # Diff current state vs v3
  agentfs diff v2 v4      # Diff v2 vs v4

Note: Diff on sparse bundles compares the band files, which may not show
individual file changes clearly. For best results, use git diff inside
the mounted store.`,
	Args: cobra.RangeArgs(1, 2),
	Run: func(cmd *cobra.Command, args []string) {
		name, err := context.MustResolveStore(storeFlag, "")
		if err != nil {
			exitWithError(ExitUsageError, "%v", err)
		}

		var fromVersion, toVersion int

		if len(args) == 1 {
			// Diff current vs checkpoint
			toVersion, err = parseVersion(args[0])
			if err != nil {
				exitWithError(ExitUsageError, "invalid version: %v", err)
			}
			fromVersion = 0 // 0 means current state
		} else {
			// Diff two checkpoints
			fromVersion, err = parseVersion(args[0])
			if err != nil {
				exitWithError(ExitUsageError, "invalid version: %v", err)
			}
			toVersion, err = parseVersion(args[1])
			if err != nil {
				exitWithError(ExitUsageError, "invalid version: %v", err)
			}
		}

		result, err := cpManager.Diff(name, fromVersion, toVersion)
		if err != nil {
			exitWithError(ExitError, "%v", err)
		}

		if jsonFlag {
			type diffJSON struct {
				Modified []string `json:"modified,omitempty"`
				Added    []string `json:"added,omitempty"`
				Deleted  []string `json:"deleted,omitempty"`
			}

			output := diffJSON{
				Added:   result.Added,
				Deleted: result.Deleted,
			}
			for _, m := range result.Modified {
				output.Modified = append(output.Modified, m.Path)
			}

			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			enc.Encode(output)
			return
		}

		if len(result.Modified) == 0 && len(result.Added) == 0 && len(result.Deleted) == 0 {
			fmt.Println("No differences found")
			return
		}

		for _, f := range result.Modified {
			fmt.Printf("Modified: %s\n", f.Path)
		}
		for _, f := range result.Added {
			fmt.Printf("Added:    %s\n", f)
		}
		for _, f := range result.Deleted {
			fmt.Printf("Deleted:  %s\n", f)
		}
	},
}

func init() {
	rootCmd.AddCommand(diffCmd)
}
