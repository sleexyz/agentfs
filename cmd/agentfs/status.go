package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/agentfs/agentfs/internal/context"
	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current context and status",
	Long: `Show the current store context and status.

Displays information about the currently selected store including
mount status and checkpoint information.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		name, fromContext, err := context.ResolveStore(storeFlag, "")
		if err != nil {
			exitWithError(ExitError, "%v", err)
		}

		if name == "" {
			if jsonFlag {
				fmt.Println("{}")
				return
			}
			fmt.Println("No store selected. Use --store or run 'agentfs use <name>'")
			return
		}

		s, err := storeManager.Get(name)
		if err != nil {
			exitWithError(ExitError, "%v", err)
		}
		if s == nil {
			exitWithError(ExitStoreNotFound, "store '%s' not found", name)
		}

		// Get checkpoint info
		count, _ := cpManager.Count(name)
		latest, _ := cpManager.GetLatest(name)

		if jsonFlag {
			type statusJSON struct {
				Store           string `json:"store"`
				MountPath       string `json:"mount_path"`
				Mounted         bool   `json:"mounted"`
				Checkpoints     int    `json:"checkpoints"`
				LatestVersion   string `json:"latest_version,omitempty"`
				LatestMessage   string `json:"latest_message,omitempty"`
				LatestCreatedAt string `json:"latest_created_at,omitempty"`
				FromContext     bool   `json:"from_context"`
			}

			output := statusJSON{
				Store:       s.Name,
				MountPath:   s.MountPath,
				Mounted:     s.MountedAt != nil,
				Checkpoints: count,
				FromContext: fromContext,
			}

			if latest != nil {
				output.LatestVersion = fmt.Sprintf("v%d", latest.Version)
				output.LatestMessage = latest.Message
				output.LatestCreatedAt = latest.CreatedAt.Format(time.RFC3339)
			}

			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			enc.Encode(output)
			return
		}

		mounted := "Yes"
		if s.MountedAt == nil {
			mounted = "No"
		}

		// Shorten home directory in mount path for display
		mountPath := s.MountPath
		if home, err := os.UserHomeDir(); err == nil {
			mountPath = strings.Replace(mountPath, home, "~", 1)
		}

		fmt.Printf("Store:       %s\n", s.Name)
		fmt.Printf("Mount:       %s\n", mountPath)
		fmt.Printf("Mounted:     %s\n", mounted)
		fmt.Printf("Checkpoints: %d\n", count)

		if latest != nil {
			latestInfo := fmt.Sprintf("v%d", latest.Version)
			if latest.Message != "" {
				latestInfo += fmt.Sprintf(" %q", latest.Message)
			}
			latestInfo += fmt.Sprintf(" (%s)", humanize.Time(latest.CreatedAt))
			fmt.Printf("Latest:      %s\n", latestInfo)
		}
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
