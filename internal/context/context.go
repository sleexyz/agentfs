package context

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const ContextFileName = ".agentfs"

// FindContext searches for a .agentfs file in the current directory and parent directories
func FindContext(startDir string) (string, string, error) {
	dir := startDir
	for {
		contextFile := filepath.Join(dir, ContextFileName)
		info, err := os.Stat(contextFile)
		if err == nil && !info.IsDir() {
			// Found a .agentfs file (not directory)
			content, err := os.ReadFile(contextFile)
			if err != nil {
				return "", "", fmt.Errorf("failed to read context file: %w", err)
			}
			storeName := strings.TrimSpace(string(content))
			if storeName == "" {
				return "", "", fmt.Errorf("context file is empty: %s", contextFile)
			}
			return storeName, contextFile, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root without finding context
			return "", "", nil
		}
		dir = parent
	}
}

// WriteContext writes a .agentfs file with the store name
func WriteContext(dir, storeName string) error {
	contextFile := filepath.Join(dir, ContextFileName)
	return os.WriteFile(contextFile, []byte(storeName+"\n"), 0644)
}

// ResolveStore resolves the store name from flag or context file
// Returns: storeName, fromContext, error
func ResolveStore(storeFlag, startDir string) (string, bool, error) {
	// Priority 1: explicit --store flag
	if storeFlag != "" {
		return storeFlag, false, nil
	}

	// Priority 2: .agentfs context file
	if startDir == "" {
		var err error
		startDir, err = os.Getwd()
		if err != nil {
			return "", false, fmt.Errorf("failed to get current directory: %w", err)
		}
	}

	storeName, _, err := FindContext(startDir)
	if err != nil {
		return "", false, err
	}
	if storeName != "" {
		return storeName, true, nil
	}

	return "", false, nil
}

// MustResolveStore is like ResolveStore but returns an error if no store is found
func MustResolveStore(storeFlag, startDir string) (string, error) {
	storeName, _, err := ResolveStore(storeFlag, startDir)
	if err != nil {
		return "", err
	}
	if storeName == "" {
		return "", fmt.Errorf("no store selected. Use --store or run 'agentfs use <name>'")
	}
	return storeName, nil
}
