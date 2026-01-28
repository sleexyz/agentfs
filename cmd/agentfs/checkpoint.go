package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	cpkg "github.com/agentfs/agentfs/internal/checkpoint"
	"github.com/agentfs/agentfs/internal/context"
	"github.com/agentfs/agentfs/internal/db"
	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
)

var checkpointCmd = &cobra.Command{
	Use:   "checkpoint",
	Short: "Checkpoint operations",
	Long:  `Create, list, and manage checkpoints.`,
}

var cpAutoFlag bool
var cpFromHookFlag bool

// HookInput represents the JSON input from Claude Code hooks
type HookInput struct {
	SessionID     string                 `json:"session_id"`
	ToolName      string                 `json:"tool_name"`
	ToolInput     map[string]interface{} `json:"tool_input"`
	HookEventName string                 `json:"hook_event_name"`
}

var cpCreateCmd = &cobra.Command{
	Use:   "create [message]",
	Short: "Create a new checkpoint",
	Long: `Create a new checkpoint of the current state.

Uses APFS reflinks to create instant (~20ms) snapshots of the sparse bundle bands.

With --auto flag, the command:
  - Detects store from current directory (via .agentfs file)
  - Skips silently if not in an agentfs directory
  - Skips silently if store is not mounted
  - Skips silently if no changes since last checkpoint
  - Uses "auto" as the message if none provided`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var storePath string
		var err error

		if cpAutoFlag {
			// Auto mode: detect store from cwd
			storePath, err = context.FindStoreFromCwd()
			if err != nil {
				// Error reading context - exit silently in auto mode
				os.Exit(0)
			}
			if storePath == "" {
				// Not in agentfs directory - silent exit
				os.Exit(0)
			}
		} else {
			// Normal mode: resolve store
			storePath, err = context.MustResolveStore(storeFlag, "")
			if err != nil {
				exitWithError(ExitUsageError, "%v", err)
			}
		}

		// Get store info
		s, err := storeManager.GetFromPath(storePath)
		if err != nil {
			if cpAutoFlag {
				os.Exit(0) // Silent exit in auto mode
			}
			exitWithError(ExitError, "%v", err)
		}
		if s == nil {
			if cpAutoFlag {
				os.Exit(0) // Silent exit in auto mode
			}
			exitWithError(ExitStoreNotFound, "store not found")
		}

		// Check if mounted
		if !storeManager.IsMounted(s.MountPath) {
			if cpAutoFlag {
				os.Exit(0) // Silent exit in auto mode
			}
			exitWithError(ExitError, "store '%s' is not mounted", s.Name)
		}

		// Open per-store database
		database, err := db.OpenFromStorePath(storePath)
		if err != nil {
			if cpAutoFlag {
				os.Exit(1) // Error exit in auto mode (store exists but can't open)
			}
			exitWithError(ExitError, "failed to open database: %v", err)
		}
		defer database.Close()

		// Create checkpoint manager
		cpManager := cpkg.NewManager(storeManager, database, s)

		// In auto mode, check for changes
		if cpAutoFlag {
			hasChanges, err := cpManager.HasChanges()
			if err != nil {
				os.Exit(1) // Error exit
			}
			if !hasChanges {
				os.Exit(0) // No changes - silent exit
			}
		}

		var message string
		if len(args) > 0 {
			message = args[0]
		} else if cpAutoFlag {
			message = generateAutoMessage()
		}

		cp, duration, err := cpManager.Create(cpkg.CreateOpts{
			Message: message,
		})
		if err != nil {
			if cpAutoFlag {
				os.Exit(1) // Error exit in auto mode
			}
			exitWithError(ExitError, "%v", err)
		}

		// In auto mode, silent success
		if cpAutoFlag {
			os.Exit(0)
		}

		if jsonFlag {
			type createJSON struct {
				Version    string `json:"version"`
				Message    string `json:"message,omitempty"`
				CreatedAt  string `json:"created_at"`
				DurationMs int64  `json:"duration_ms"`
			}

			output := createJSON{
				Version:    fmt.Sprintf("v%d", cp.Version),
				Message:    cp.Message,
				CreatedAt:  cp.CreatedAt.Format(time.RFC3339),
				DurationMs: duration.Milliseconds(),
			}

			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			enc.Encode(output)
			return
		}

		output := fmt.Sprintf("Created v%d", cp.Version)
		if message != "" {
			output += fmt.Sprintf(" %q", message)
		}
		output += fmt.Sprintf(" (%dms)", duration.Milliseconds())
		fmt.Println(output)
	},
}

var cpListLimit int

var cpListCmd = &cobra.Command{
	Use:   "list",
	Short: "List checkpoints",
	Long:  `List all checkpoints for the current store.`,
	Args:  cobra.NoArgs,
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

		// Open per-store database
		database, err := db.OpenFromStorePath(storePath)
		if err != nil {
			exitWithError(ExitError, "failed to open database: %v", err)
		}
		defer database.Close()

		// Create checkpoint manager
		cpManager := cpkg.NewManager(storeManager, database, s)

		checkpoints, err := cpManager.List(cpListLimit)
		if err != nil {
			exitWithError(ExitError, "%v", err)
		}

		if jsonFlag {
			type cpJSON struct {
				Version   string `json:"version"`
				Message   string `json:"message,omitempty"`
				CreatedAt string `json:"created_at"`
			}

			var output []cpJSON
			for _, cp := range checkpoints {
				output = append(output, cpJSON{
					Version:   fmt.Sprintf("v%d", cp.Version),
					Message:   cp.Message,
					CreatedAt: cp.CreatedAt.Format(time.RFC3339),
				})
			}

			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			enc.Encode(output)
			return
		}

		if len(checkpoints) == 0 {
			fmt.Println("No checkpoints found. Use 'agentfs checkpoint create' to create one.")
			return
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "VERSION\tMESSAGE\tCREATED")

		for _, cp := range checkpoints {
			message := cp.Message
			if len(message) > 40 {
				message = message[:37] + "..."
			}

			fmt.Fprintf(w, "v%d\t%s\t%s\n",
				cp.Version,
				message,
				humanize.Time(cp.CreatedAt),
			)
		}
		w.Flush()
	},
}

var cpInfoCmd = &cobra.Command{
	Use:   "info <version>",
	Short: "Show checkpoint details",
	Long:  `Show detailed information about a specific checkpoint.`,
	Args:  cobra.ExactArgs(1),
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

		// Open per-store database
		database, err := db.OpenFromStorePath(storePath)
		if err != nil {
			exitWithError(ExitError, "failed to open database: %v", err)
		}
		defer database.Close()

		// Create checkpoint manager
		cpManager := cpkg.NewManager(storeManager, database, s)

		version, err := parseVersion(args[0])
		if err != nil {
			exitWithError(ExitUsageError, "invalid version: %v", err)
		}

		cp, err := cpManager.Get(version)
		if err != nil {
			exitWithError(ExitError, "%v", err)
		}
		if cp == nil {
			exitWithError(ExitCPNotFound, "checkpoint v%d not found", version)
		}

		if jsonFlag {
			type infoJSON struct {
				Version   string `json:"version"`
				Store     string `json:"store"`
				Message   string `json:"message,omitempty"`
				CreatedAt string `json:"created_at"`
			}

			output := infoJSON{
				Version:   fmt.Sprintf("v%d", cp.Version),
				Store:     s.Name,
				Message:   cp.Message,
				CreatedAt: cp.CreatedAt.Format(time.RFC3339),
			}

			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			enc.Encode(output)
			return
		}

		fmt.Printf("Checkpoint:  v%d\n", cp.Version)
		fmt.Printf("Store:       %s\n", s.Name)
		if cp.Message != "" {
			fmt.Printf("Message:     %s\n", cp.Message)
		}
		fmt.Printf("Created:     %s\n", cp.CreatedAt.Format("2006-01-02 15:04:05"))
	},
}

var cpDeleteCmd = &cobra.Command{
	Use:   "delete <version>",
	Short: "Delete a checkpoint",
	Long: `Delete a specific checkpoint.

Requires confirmation unless -f/--force is specified.`,
	Args: cobra.ExactArgs(1),
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

		// Open per-store database
		database, err := db.OpenFromStorePath(storePath)
		if err != nil {
			exitWithError(ExitError, "failed to open database: %v", err)
		}
		defer database.Close()

		// Create checkpoint manager
		cpManager := cpkg.NewManager(storeManager, database, s)

		version, err := parseVersion(args[0])
		if err != nil {
			exitWithError(ExitUsageError, "invalid version: %v", err)
		}

		cp, err := cpManager.Get(version)
		if err != nil {
			exitWithError(ExitError, "%v", err)
		}
		if cp == nil {
			exitWithError(ExitCPNotFound, "checkpoint v%d not found", version)
		}

		if !confirmPrompt(fmt.Sprintf("Delete checkpoint v%d?", version)) {
			fmt.Println("Cancelled")
			return
		}

		if err := cpManager.Delete(version); err != nil {
			exitWithError(ExitError, "%v", err)
		}

		fmt.Printf("Deleted v%d\n", version)
	},
}

func init() {
	cpCreateCmd.Flags().BoolVar(&cpAutoFlag, "auto", false, "auto-checkpoint mode (quiet, skip-if-unchanged)")
	cpCreateCmd.Flags().BoolVar(&cpFromHookFlag, "from-hook", false, "read hook context from stdin (use with --auto)")
	cpListCmd.Flags().IntVar(&cpListLimit, "limit", 0, "limit number of results")

	checkpointCmd.AddCommand(cpCreateCmd)
	checkpointCmd.AddCommand(cpListCmd)
	checkpointCmd.AddCommand(cpInfoCmd)
	checkpointCmd.AddCommand(cpDeleteCmd)
	rootCmd.AddCommand(checkpointCmd)
}

// parseVersion parses a version string like "v3" or "3" and returns the integer version
func parseVersion(s string) (int, error) {
	s = strings.TrimPrefix(s, "v")
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("version must be a number (e.g., v3 or 3)")
	}
	if v < 1 {
		return 0, fmt.Errorf("version must be positive")
	}
	return v, nil
}

// generateAutoMessage creates a checkpoint message, optionally reading hook context from stdin
func generateAutoMessage() string {
	if !cpFromHookFlag {
		return "auto"
	}

	// Read JSON from stdin
	data, err := io.ReadAll(os.Stdin)
	if err != nil || len(data) == 0 {
		return "auto"
	}

	var hookInput HookInput
	if err := json.Unmarshal(data, &hookInput); err != nil {
		return "auto"
	}

	// Build message parts
	var parts []string

	// Tool name
	if hookInput.ToolName != "" {
		parts = append(parts, hookInput.ToolName)
	}

	// Extract file path from tool_input if available
	if hookInput.ToolInput != nil {
		if filePath, ok := hookInput.ToolInput["file_path"].(string); ok && filePath != "" {
			// Use just the filename for brevity
			parts = append(parts, filepath.Base(filePath))
		} else if cmd, ok := hookInput.ToolInput["command"].(string); ok && cmd != "" {
			// For Bash, show truncated command
			if len(cmd) > 30 {
				cmd = cmd[:27] + "..."
			}
			parts = append(parts, fmt.Sprintf("`%s`", cmd))
		}
	}

	// Session ID (short form)
	if hookInput.SessionID != "" {
		sessionShort := hookInput.SessionID
		if len(sessionShort) > 8 {
			sessionShort = sessionShort[:8]
		}
		parts = append(parts, fmt.Sprintf("(%s)", sessionShort))
	}

	if len(parts) == 0 {
		return "auto"
	}

	return strings.Join(parts, " ")
}
