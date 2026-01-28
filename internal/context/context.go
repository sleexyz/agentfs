package context

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const ContextFileName = ".agentfs"

// Context holds the resolved store context
type Context struct {
	StorePath   string // Full path to foo.fs/
	StoreName   string // Just the name (foo)
	ContextFile string // Path to the .agentfs file that was found
}

// FindContext searches for a .agentfs file starting from startDir and walking up
// The .agentfs file now contains the full path to the store
func FindContext(startDir string) (*Context, error) {
	dir := startDir
	for {
		contextFile := filepath.Join(dir, ContextFileName)
		info, err := os.Stat(contextFile)
		if err == nil && !info.IsDir() {
			// Found a .agentfs file (not directory)
			content, err := os.ReadFile(contextFile)
			if err != nil {
				return nil, fmt.Errorf("failed to read context file: %w", err)
			}
			storePath := strings.TrimSpace(string(content))
			if storePath == "" {
				return nil, fmt.Errorf("context file is empty: %s", contextFile)
			}

			// Extract store name from path
			storeName := strings.TrimSuffix(filepath.Base(storePath), ".fs")

			return &Context{
				StorePath:   storePath,
				StoreName:   storeName,
				ContextFile: contextFile,
			}, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root without finding context
			return nil, nil
		}
		dir = parent
	}
}

// WriteContext writes a .agentfs file with the full store path
func WriteContext(mountDir, storePath string) error {
	contextFile := filepath.Join(mountDir, ContextFileName)
	return os.WriteFile(contextFile, []byte(storePath+"\n"), 0644)
}

// ResolveStore resolves the store path from:
// 1. Explicit --store flag (name) -> look for name.fs/ in cwd
// 2. .agentfs context file (searched up from cwd)
// 3. Scan for single *.fs/ in cwd
// Returns: storePath (full path to foo.fs/), error
func ResolveStore(storeFlag, startDir string) (string, error) {
	if startDir == "" {
		var err error
		startDir, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("failed to get current directory: %w", err)
		}
	}

	// Priority 1: explicit --store flag (treat as name, look for name.fs/)
	if storeFlag != "" {
		storePath := filepath.Join(startDir, storeFlag+".fs")
		if _, err := os.Stat(storePath); err == nil {
			return storePath, nil
		}
		// Also try if storeFlag is already a full path
		if strings.HasSuffix(storeFlag, ".fs") {
			if _, err := os.Stat(storeFlag); err == nil {
				return storeFlag, nil
			}
		}
		return "", fmt.Errorf("store '%s' not found (looked for %s)", storeFlag, storeFlag+".fs")
	}

	// Priority 2: .agentfs context file
	ctx, err := FindContext(startDir)
	if err != nil {
		return "", err
	}
	if ctx != nil {
		// Verify the store still exists
		if _, err := os.Stat(ctx.StorePath); err == nil {
			return ctx.StorePath, nil
		}
		return "", fmt.Errorf("store at %s no longer exists (referenced by %s)", ctx.StorePath, ctx.ContextFile)
	}

	// Priority 3: scan for single *.fs/ in cwd
	entries, err := os.ReadDir(startDir)
	if err != nil {
		return "", fmt.Errorf("failed to read directory: %w", err)
	}

	var fsStores []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasSuffix(entry.Name(), ".fs") {
			// Verify it's a valid store (has data.sparsebundle)
			storePath := filepath.Join(startDir, entry.Name())
			bundlePath := filepath.Join(storePath, "data.sparsebundle")
			if _, err := os.Stat(bundlePath); err == nil {
				fsStores = append(fsStores, storePath)
			}
		}
	}

	if len(fsStores) == 1 {
		return fsStores[0], nil
	}

	if len(fsStores) > 1 {
		return "", fmt.Errorf("multiple stores found in current directory. Use --store to specify one or cd into a mount directory")
	}

	return "", nil
}

// MustResolveStore is like ResolveStore but returns an error if no store is found
func MustResolveStore(storeFlag, startDir string) (string, error) {
	storePath, err := ResolveStore(storeFlag, startDir)
	if err != nil {
		return "", err
	}
	if storePath == "" {
		return "", fmt.Errorf("no store found. Use --store or run from a store directory")
	}
	return storePath, nil
}

// StoreNameFromPath extracts the store name from a .fs path
func StoreNameFromPath(storePath string) string {
	return strings.TrimSuffix(filepath.Base(storePath), ".fs")
}

// FindStoreFromCwd finds a store by walking up from cwd looking for .agentfs file
// Returns storePath if found, empty string if not in an agentfs directory
// This is for use with --auto flag where "not found" is not an error
func FindStoreFromCwd() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	ctx, err := FindContext(cwd)
	if err != nil {
		return "", err
	}
	if ctx == nil {
		return "", nil // Not in agentfs directory
	}

	// Verify the store still exists
	if _, err := os.Stat(ctx.StorePath); os.IsNotExist(err) {
		return "", nil // Store doesn't exist anymore
	}

	return ctx.StorePath, nil
}
