// fswatch tests FSEvents behavior with mounted sparse bundles
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
)

// DirtyTracker accumulates file changes between checkpoints
type DirtyTracker struct {
	mu        sync.Mutex
	dirty     map[string]time.Time // path -> first dirty time
	watcher   *fsnotify.Watcher
	watchPath string
}

func NewDirtyTracker(path string) (*DirtyTracker, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create watcher: %w", err)
	}

	dt := &DirtyTracker{
		dirty:     make(map[string]time.Time),
		watcher:   watcher,
		watchPath: path,
	}

	return dt, nil
}

func (dt *DirtyTracker) Start() error {
	// Walk and add all directories
	err := filepath.WalkDir(dt.watchPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == ".next" {
				return filepath.SkipDir
			}
			if err := dt.watcher.Add(path); err != nil {
				log.Printf("Warning: could not watch %s: %v", path, err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk: %w", err)
	}

	// Start event loop
	go dt.eventLoop()

	return nil
}

func (dt *DirtyTracker) eventLoop() {
	for {
		select {
		case event, ok := <-dt.watcher.Events:
			if !ok {
				return
			}
			dt.handleEvent(event)
		case err, ok := <-dt.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("Watcher error: %v", err)
		}
	}
}

func (dt *DirtyTracker) handleEvent(event fsnotify.Event) {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	path := event.Name

	// Track the event
	if _, exists := dt.dirty[path]; !exists {
		dt.dirty[path] = time.Now()
	}

	fmt.Printf("[%s] %s: %s\n", time.Now().Format("15:04:05.000"), event.Op, path)

	// If a new directory was created, watch it
	if event.Op&fsnotify.Create == fsnotify.Create {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			if err := dt.watcher.Add(path); err == nil {
				fmt.Printf("  + Added watch for new directory\n")
			}
		}
	}
}

func (dt *DirtyTracker) GetDirtyFiles() []string {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	files := make([]string, 0, len(dt.dirty))
	for path := range dt.dirty {
		files = append(files, path)
	}
	return files
}

func (dt *DirtyTracker) ClearDirty() int {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	count := len(dt.dirty)
	dt.dirty = make(map[string]time.Time)
	return count
}

func (dt *DirtyTracker) Close() {
	dt.watcher.Close()
}

func main() {
	watchPath := flag.String("path", ".", "Path to watch")
	testMount := flag.Bool("test-mount", false, "Test sparse bundle mount detection")
	interactive := flag.Bool("interactive", true, "Run in interactive mode")
	flag.Parse()

	absPath, err := filepath.Abs(*watchPath)
	if err != nil {
		log.Fatalf("Error: %v", err)
	}

	fmt.Printf("=== FSEvents Test ===\n")
	fmt.Printf("Watching: %s\n", absPath)

	// Check if path is a mount point
	isMountPoint := checkMountPoint(absPath)
	fmt.Printf("Is mount point: %v\n", isMountPoint)

	if *testMount {
		testMountBehavior(absPath)
		return
	}

	// Create tracker
	tracker, err := NewDirtyTracker(absPath)
	if err != nil {
		log.Fatalf("Create tracker: %v", err)
	}
	defer tracker.Close()

	// Start watching
	if err := tracker.Start(); err != nil {
		log.Fatalf("Start tracker: %v", err)
	}

	fmt.Println("\nWatching for file changes...")
	fmt.Println("Commands:")
	fmt.Println("  Ctrl+C to quit")
	fmt.Println("  Modify files in the watched directory to see events")
	fmt.Println()

	if *interactive {
		// Periodically report status
		go func() {
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				files := tracker.GetDirtyFiles()
				fmt.Printf("\n--- Status: %d dirty files ---\n", len(files))
			}
		}()
	}

	// Wait for interrupt
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\n\n=== Final Status ===")
	files := tracker.GetDirtyFiles()
	fmt.Printf("Total dirty files: %d\n", len(files))
	if len(files) > 0 && len(files) <= 20 {
		for _, f := range files {
			fmt.Printf("  %s\n", f)
		}
	}
}

func checkMountPoint(path string) bool {
	var pathStat, parentStat syscall.Stat_t

	if err := syscall.Stat(path, &pathStat); err != nil {
		return false
	}

	parent := filepath.Dir(path)
	if err := syscall.Stat(parent, &parentStat); err != nil {
		return false
	}

	// Different device = mount point
	return pathStat.Dev != parentStat.Dev
}

func testMountBehavior(path string) {
	fmt.Println("\n=== Mount Point Test ===")

	// Get device info
	var stat syscall.Stat_t
	if err := syscall.Stat(path, &stat); err != nil {
		log.Fatalf("Stat error: %v", err)
	}

	fmt.Printf("Path device: %d\n", stat.Dev)

	parent := filepath.Dir(path)
	var parentStat syscall.Stat_t
	if err := syscall.Stat(parent, &parentStat); err != nil {
		log.Fatalf("Parent stat error: %v", err)
	}
	fmt.Printf("Parent device: %d\n", parentStat.Dev)

	if stat.Dev != parentStat.Dev {
		fmt.Println("Result: This IS a mount point (different devices)")
	} else {
		fmt.Println("Result: This is NOT a mount point (same device)")
	}

	// Check if it looks like a sparse bundle mount
	// Sparse bundles mount as HFS+ or APFS volumes
	fmt.Println("\nMount info:")
	// Note: We'd need to parse /proc/mounts or use diskutil on macOS
	// For now, just show basic info
}
