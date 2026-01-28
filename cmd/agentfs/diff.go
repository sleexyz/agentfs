package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/agentfs/agentfs/internal/context"
	"github.com/agentfs/agentfs/internal/diff"
	"github.com/spf13/cobra"
)

var (
	diffStatFlag     bool
	diffNameOnlyFlag bool
)

var diffCmd = &cobra.Command{
	Use:   "diff <version> [version2] [-- <path>]",
	Short: "Show changes between checkpoints",
	Long: `Show changes between two checkpoints or between a checkpoint and current state.

Usage:
  agentfs diff v3              # Diff v3 vs current state
  agentfs diff v2 v4           # Diff v2 vs v4
  agentfs diff v3 -- src/app.ts  # Show diff of specific file

Flags:
  --stat        Show summary statistics only
  --name-only   Just list changed file names`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		// Resolve store
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

		// Parse args
		// Cobra strips -- so args could be:
		// [v1]           -> diff v1 vs current
		// [v1, v2]       -> diff v1 vs v2
		// [v1, path]     -> diff v1 vs current, specific file (path doesn't start with v)
		// [v1, v2, path] -> diff v1 vs v2, specific file
		var fromVersion, toVersion int
		var specificPath string

		if len(args) == 0 {
			exitWithError(ExitUsageError, "at least one version is required")
		}

		fromVersion, err = parseVersion(args[0])
		if err != nil {
			exitWithError(ExitUsageError, "invalid version: %v", err)
		}

		if len(args) > 1 {
			// Try to parse second arg as version
			v2, err := parseVersion(args[1])
			if err == nil {
				// It's a version
				toVersion = v2
				// Check for path
				if len(args) > 2 {
					specificPath = args[2]
				}
			} else {
				// Not a version, must be a path
				specificPath = args[1]
			}
		}
		// toVersion == 0 means compare against current

		// Create differ
		differ := diff.NewDiffer(storeManager, s)

		// Handle specific file diff
		if specificPath != "" {
			if err := differ.DiffFile(fromVersion, toVersion, specificPath); err != nil {
				exitWithError(ExitError, "%v", err)
			}
			return
		}

		// Perform diff
		result, err := differ.Diff(fromVersion, toVersion)
		if err != nil {
			exitWithError(ExitError, "%v", err)
		}

		// Output
		if jsonFlag {
			outputJSON(result)
			return
		}

		if diffNameOnlyFlag {
			outputNameOnly(result)
			return
		}

		if diffStatFlag {
			outputStat(result)
			return
		}

		outputDefault(result)
	},
}

func outputJSON(result *diff.Result) {
	type changeJSON struct {
		Path string `json:"path"`
		Type string `json:"type"`
	}
	type diffJSON struct {
		Base    string       `json:"base"`
		Target  string       `json:"target"`
		Changes []changeJSON `json:"changes"`
		Summary struct {
			Added    int `json:"added"`
			Modified int `json:"modified"`
			Deleted  int `json:"deleted"`
		} `json:"summary"`
	}

	output := diffJSON{
		Base:   result.Base,
		Target: result.Target,
	}

	for _, c := range result.Changes {
		output.Changes = append(output.Changes, changeJSON{
			Path: c.Path,
			Type: strings.ToLower(c.Type.String()),
		})
	}

	added, modified, deleted := result.Summary()
	output.Summary.Added = added
	output.Summary.Modified = modified
	output.Summary.Deleted = deleted

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(output)
}

func outputNameOnly(result *diff.Result) {
	for _, c := range result.Changes {
		fmt.Println(c.Path)
	}
}

func outputStat(result *diff.Result) {
	fmt.Printf("Comparing %s → %s\n\n", result.Base, result.Target)

	for _, c := range result.Changes {
		prefix := ""
		switch c.Type {
		case diff.Added:
			prefix = "A"
		case diff.Modified:
			prefix = "M"
		case diff.Deleted:
			prefix = "D"
		}
		fmt.Printf("%s  %s\n", prefix, c.Path)
	}

	added, modified, deleted := result.Summary()
	fmt.Printf("\n%d files changed", added+modified+deleted)
	if added > 0 {
		fmt.Printf(", %d added", added)
	}
	if modified > 0 {
		fmt.Printf(", %d modified", modified)
	}
	if deleted > 0 {
		fmt.Printf(", %d deleted", deleted)
	}
	fmt.Println()
}

func outputDefault(result *diff.Result) {
	fmt.Printf("Comparing %s → %s\n\n", result.Base, result.Target)

	if len(result.Changes) == 0 {
		fmt.Println("No differences found")
		return
	}

	for _, c := range result.Changes {
		switch c.Type {
		case diff.Modified:
			fmt.Printf("Modified:  %s\n", c.Path)
		case diff.Added:
			fmt.Printf("Added:     %s\n", c.Path)
		case diff.Deleted:
			fmt.Printf("Deleted:   %s\n", c.Path)
		}
	}

	added, modified, deleted := result.Summary()
	fmt.Printf("\n%d files changed", added+modified+deleted)
	if added > 0 {
		fmt.Printf(", %d added", added)
	}
	if modified > 0 {
		fmt.Printf(", %d modified", modified)
	}
	if deleted > 0 {
		fmt.Printf(", %d deleted", deleted)
	}
	fmt.Println()
}

func init() {
	diffCmd.Flags().BoolVar(&diffStatFlag, "stat", false, "show summary statistics only")
	diffCmd.Flags().BoolVar(&diffNameOnlyFlag, "name-only", false, "just list changed file names")
	rootCmd.AddCommand(diffCmd)
}
