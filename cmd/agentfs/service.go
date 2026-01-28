package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"

	"github.com/sleexyz/agentfs/internal/registry"
	"github.com/spf13/cobra"
)

const (
	plistName = "com.agentfs.mount.plist"
	plistDir  = "Library/LaunchAgents"
)

var serviceForceFlag bool

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Manage the agentfs LaunchAgent service",
	Long: `Manage the agentfs LaunchAgent service for auto-remount on login.

The service runs 'agentfs mount --all' at login to remount registered stores.

Commands:
  install    Install and load the LaunchAgent
  uninstall  Unload and remove the LaunchAgent
  status     Show service status`,
}

var serviceInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the LaunchAgent service",
	Long: `Install and load the LaunchAgent for auto-remount on login.

Creates ~/Library/LaunchAgents/com.agentfs.mount.plist and loads it.
Use --force to reinstall if already installed.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		plistPath := getPlistPath()

		// Check if already installed
		if _, err := os.Stat(plistPath); err == nil {
			if !serviceForceFlag {
				exitWithError(ExitError, "Service already installed. Use --force to reinstall.")
			}
			// Unload existing service before reinstalling
			fmt.Println("Unloading existing service...")
			exec.Command("launchctl", "unload", plistPath).Run()
		}

		// Get agentfs binary path
		binaryPath, err := getAgentfsBinaryPath()
		if err != nil {
			exitWithError(ExitError, "failed to get agentfs path: %v", err)
		}

		// Ensure LaunchAgents directory exists
		launchAgentsDir := filepath.Dir(plistPath)
		if err := os.MkdirAll(launchAgentsDir, 0755); err != nil {
			exitWithError(ExitError, "failed to create LaunchAgents directory: %v", err)
		}

		// Generate and write plist
		fmt.Println("Creating LaunchAgent...")
		if err := writePlist(plistPath, binaryPath); err != nil {
			exitWithError(ExitError, "failed to write plist: %v", err)
		}

		// Load the service
		fmt.Println("Loading service...")
		loadCmd := exec.Command("launchctl", "load", plistPath)
		if output, err := loadCmd.CombinedOutput(); err != nil {
			exitWithError(ExitError, "failed to load service: %v\n%s", err, output)
		}

		fmt.Println("Service installed. Stores will auto-mount on login.")
	},
}

var serviceUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Uninstall the LaunchAgent service",
	Long:  `Unload and remove the LaunchAgent.`,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		plistPath := getPlistPath()

		// Check if installed
		if _, err := os.Stat(plistPath); os.IsNotExist(err) {
			fmt.Println("Service is not installed.")
			return
		}

		// Unload the service
		fmt.Println("Unloading service...")
		unloadCmd := exec.Command("launchctl", "unload", plistPath)
		if output, err := unloadCmd.CombinedOutput(); err != nil {
			// Don't fail if unload fails (might not be loaded)
			fmt.Fprintf(os.Stderr, "warning: unload: %v\n%s", err, output)
		}

		// Remove plist file
		fmt.Println("Removing LaunchAgent...")
		if err := os.Remove(plistPath); err != nil {
			exitWithError(ExitError, "failed to remove plist: %v", err)
		}

		fmt.Println("Service uninstalled.")
	},
}

var serviceStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show service status",
	Long:  `Show the current status of the LaunchAgent service and registered stores.`,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		plistPath := getPlistPath()

		// Check if installed
		if _, err := os.Stat(plistPath); os.IsNotExist(err) {
			fmt.Println("Service: not installed")
		} else {
			fmt.Println("Service: installed")
			fmt.Printf("LaunchAgent: %s\n", plistPath)
		}

		// Show registry info
		reg, err := registry.Open()
		if err != nil {
			fmt.Printf("Registry: error (%v)\n", err)
			return
		}
		defer reg.Close()

		stores, err := reg.List()
		if err != nil {
			fmt.Printf("Registry: error (%v)\n", err)
			return
		}

		fmt.Printf("Registered stores: %d\n", len(stores))
		for _, s := range stores {
			autoMount := "yes"
			if !s.AutoMount {
				autoMount = "no"
			}
			fmt.Printf("  - %s (auto-mount: %s)\n", s.StorePath, autoMount)
		}
	},
}

func getPlistPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		exitWithError(ExitError, "failed to get home directory: %v", err)
	}
	return filepath.Join(home, plistDir, plistName)
}

func getAgentfsBinaryPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.agentfs.mount</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.BinaryPath}}</string>
        <string>mount</string>
        <string>--all</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/tmp/agentfs-mount.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/agentfs-mount.log</string>
</dict>
</plist>
`

func writePlist(path, binaryPath string) error {
	tmpl, err := template.New("plist").Parse(plistTemplate)
	if err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	return tmpl.Execute(f, struct {
		BinaryPath string
	}{
		BinaryPath: binaryPath,
	})
}

func init() {
	serviceInstallCmd.Flags().BoolVar(&serviceForceFlag, "force", false, "reinstall even if already installed")

	serviceCmd.AddCommand(serviceInstallCmd)
	serviceCmd.AddCommand(serviceUninstallCmd)
	serviceCmd.AddCommand(serviceStatusCmd)
	rootCmd.AddCommand(serviceCmd)
}
