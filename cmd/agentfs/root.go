package main

import (
	"fmt"
	"os"

	"github.com/sleexyz/agentfs/internal/store"
	"github.com/spf13/cobra"
)

// Exit codes
const (
	ExitSuccess       = 0
	ExitError         = 1
	ExitUsageError    = 2
	ExitStoreNotFound = 3
	ExitCPNotFound    = 4
	ExitMountFailed   = 5
)

var (
	// Global flags
	storeFlag string
	jsonFlag  bool
	forceFlag bool

	// Shared store manager (no global database needed anymore)
	storeManager *store.Manager
)

var rootCmd = &cobra.Command{
	Use:   "agentfs",
	Short: "Instant checkpoint and restore for macOS projects",
	Long: `AgentFS provides instant checkpointing (~20ms) and fast restore (<500ms)
for macOS projects using sparse bundles and APFS reflinks.

Stores are self-contained in foo.fs/ directories with adjacent foo/ mount points.

Use 'agentfs init <name>' to create a new store, then 'agentfs checkpoint create'
to create checkpoints. Restore with 'agentfs restore <version>'.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Skip initialization for help and completion commands
		if cmd.Name() == "help" || cmd.Name() == "completion" {
			return nil
		}

		// Initialize store manager (no database needed - stores are self-contained)
		storeManager = store.NewManager()

		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&storeFlag, "store", "", "specify store name")
	rootCmd.PersistentFlags().BoolVar(&jsonFlag, "json", false, "output as JSON")
	rootCmd.PersistentFlags().BoolVarP(&forceFlag, "force", "f", false, "skip confirmation prompts")
}

// exitWithError prints an error message and exits with the given code
func exitWithError(code int, format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(code)
}

// confirmPrompt asks for confirmation and returns true if confirmed
func confirmPrompt(prompt string) bool {
	if forceFlag {
		return true
	}

	fmt.Printf("%s [y/N] ", prompt)
	var response string
	fmt.Scanln(&response)
	return response == "y" || response == "Y"
}
