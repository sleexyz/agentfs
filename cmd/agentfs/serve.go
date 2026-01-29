package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sleexyz/agentfs/internal/context"
	"github.com/sleexyz/agentfs/internal/db"
	"github.com/spf13/cobra"
)

var (
	servePortFlag    string
	serveCorsFlag    bool
	serveNoCacheFlag bool
	serveWorkersFlag int
)

// Index holds the pre-computed data for the timeline visualizer
type Index struct {
	MountPath   string                 `json:"mountPath"`
	StorePath   string                 `json:"storePath"`
	StoreName   string                 `json:"storeName"`
	Checkpoints []CheckpointInfo       `json:"checkpoints"`
	Manifests   map[int]*Manifest      `json:"-"` // version -> manifest (not serialized directly)
	Deltas      map[string]*Delta      `json:"-"` // "v1:v2" -> delta (not serialized directly)
}

// CheckpointInfo holds checkpoint metadata for the API
type CheckpointInfo struct {
	Version       int       `json:"version"`
	Message       string    `json:"message,omitempty"`
	Timestamp     time.Time `json:"timestamp"`
	FileCount     int       `json:"fileCount"`
	ParentVersion *int      `json:"parentVersion,omitempty"`
	Summary       Summary   `json:"summary"`
}

// Summary holds change counts
type Summary struct {
	Added    int `json:"added"`
	Modified int `json:"modified"`
	Deleted  int `json:"deleted"`
}

// Manifest holds the file tree for a checkpoint
type Manifest struct {
	Version int                  `json:"version"`
	Files   map[string]*FileInfo `json:"files"` // path -> file info
}

// FileInfo holds metadata about a file
type FileInfo struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Mtime     int64  `json:"mtime"` // Unix timestamp
	Mode      uint32 `json:"mode"`
	IsDir     bool   `json:"isDir"`
	IsSymlink bool   `json:"isSymlink"`
}

// Delta holds changes between two versions
type Delta struct {
	FromVersion int      `json:"fromVersion"`
	ToVersion   int      `json:"toVersion"`
	Added       []string `json:"added"`
	Modified    []string `json:"modified"`
	Deleted     []string `json:"deleted"`
}

// Server holds the HTTP server state
type Server struct {
	index    *Index
	mu       sync.RWMutex
	staticFS http.FileSystem
}

// IndexCache holds the cached index data
type IndexCache struct {
	Version            int                    `json:"version"`            // Cache format version
	GeneratedAt        time.Time              `json:"generatedAt"`        // When the cache was generated
	CheckpointVersions []int                  `json:"checkpointVersions"` // List of checkpoint versions in cache
	Checkpoints        []CheckpointInfo       `json:"checkpoints"`        // Checkpoint metadata
	Manifests          map[string]*Manifest   `json:"manifests"`          // "v1" -> manifest
	Deltas             map[string]*Delta      `json:"deltas"`             // "v1:v2" -> delta
}

const indexCacheVersion = 1
const indexCacheFile = "serve-index.json"

// saveIndexCache saves the index to a cache file in the store
func saveIndexCache(index *Index, storePath string) error {
	cache := &IndexCache{
		Version:            indexCacheVersion,
		GeneratedAt:        time.Now(),
		CheckpointVersions: make([]int, 0, len(index.Manifests)),
		Checkpoints:        index.Checkpoints,
		Manifests:          make(map[string]*Manifest),
		Deltas:             index.Deltas,
	}

	// Collect checkpoint versions and convert manifest keys
	for v, m := range index.Manifests {
		cache.CheckpointVersions = append(cache.CheckpointVersions, v)
		cache.Manifests[fmt.Sprintf("v%d", v)] = m
	}
	sort.Ints(cache.CheckpointVersions)

	cachePath := filepath.Join(storePath, indexCacheFile)
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal cache: %w", err)
	}

	if err := os.WriteFile(cachePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write cache: %w", err)
	}

	return nil
}

// loadIndexCache loads the index cache from disk
func loadIndexCache(storePath string) (*IndexCache, error) {
	cachePath := filepath.Join(storePath, indexCacheFile)

	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil, err
	}

	var cache IndexCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, fmt.Errorf("failed to parse cache: %w", err)
	}

	// Check cache format version
	if cache.Version != indexCacheVersion {
		return nil, fmt.Errorf("cache version mismatch: got %d, want %d", cache.Version, indexCacheVersion)
	}

	return &cache, nil
}

// isCacheValid checks if the cache is valid for the current checkpoints
func isCacheValid(cache *IndexCache, currentVersions []int) bool {
	if cache == nil {
		return false
	}

	// Sort current versions for comparison
	sorted := make([]int, len(currentVersions))
	copy(sorted, currentVersions)
	sort.Ints(sorted)

	// Check if the versions match exactly
	if len(cache.CheckpointVersions) != len(sorted) {
		return false
	}

	for i, v := range cache.CheckpointVersions {
		if v != sorted[i] {
			return false
		}
	}

	return true
}

// indexFromCache converts a cache back to an Index
func indexFromCache(cache *IndexCache, mountPath, storePath, storeName string) *Index {
	index := &Index{
		MountPath:   mountPath,
		StorePath:   storePath,
		StoreName:   storeName,
		Checkpoints: cache.Checkpoints,
		Manifests:   make(map[int]*Manifest),
		Deltas:      cache.Deltas,
	}

	// Convert string keys back to int keys
	for key, m := range cache.Manifests {
		var v int
		fmt.Sscanf(key, "v%d", &v)
		index.Manifests[v] = m
	}

	return index
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve timeline visualizer web UI",
	Long: `Start an HTTP server that serves the timeline visualizer.

On startup, the server:
1. Scans all checkpoints
2. Builds file manifests by mounting each checkpoint
3. Computes deltas between adjacent checkpoints
4. Serves a web UI for visualizing changes over time

The API endpoints are:
  GET /api/checkpoints         - List all checkpoints with summary stats
  GET /api/manifest/:version   - Full file tree for a checkpoint
  GET /api/diff/:v1/:v2        - Delta between two versions`,
	Args: cobra.NoArgs,
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

		// Check if mounted
		if !storeManager.IsMounted(s.MountPath) {
			exitWithError(ExitError, "store '%s' is not mounted. Run 'agentfs mount' first.", s.Name)
		}

		var index *Index
		start := time.Now()

		// Try to load from cache first (unless --no-cache is set)
		if !serveNoCacheFlag {
			cache, cacheErr := loadIndexCache(storePath)
			if cacheErr == nil {
				// Get current checkpoint versions to validate cache
				checkpoints, err := database.ListCheckpoints(0)
				if err == nil {
					currentVersions := make([]int, len(checkpoints))
					for i, cp := range checkpoints {
						currentVersions[i] = cp.Version
					}

					if isCacheValid(cache, currentVersions) {
						index = indexFromCache(cache, s.MountPath, storePath, s.Name)
						fmt.Printf("Loaded index from cache in %v (%d checkpoints)\n",
							time.Since(start).Round(time.Millisecond), len(index.Checkpoints))
					}
				}
			}
		}

		// Build index if not loaded from cache
		if index == nil {
			fmt.Printf("Building index for %s...\n", s.Name)

			index, err = buildIndex(storePath, s.MountPath, database, serveWorkersFlag)
			if err != nil {
				exitWithError(ExitError, "failed to build index: %v", err)
			}

			fmt.Printf("Index built in %v (%d checkpoints)\n",
				time.Since(start).Round(time.Millisecond), len(index.Checkpoints))

			// Save cache for next time
			if err := saveIndexCache(index, storePath); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to save index cache: %v\n", err)
			}
		}

		// Create server
		server := &Server{
			index: index,
		}

		// Set up routes
		mux := http.NewServeMux()

		// API routes
		mux.HandleFunc("/api/checkpoints", server.handleCheckpoints)
		mux.HandleFunc("/api/manifest/", server.handleManifest)
		mux.HandleFunc("/api/diff/", server.handleDiff)
		mux.HandleFunc("/api/index", server.handleIndex)

		// Try to serve static files from client/dist/ (relative to cwd or store path)
		staticPaths := []string{
			"client/dist",
			filepath.Join(filepath.Dir(storePath), "client/dist"),
		}

		var staticDir string
		for _, p := range staticPaths {
			if info, err := os.Stat(p); err == nil && info.IsDir() {
				staticDir = p
				break
			}
		}

		if staticDir != "" {
			server.staticFS = http.Dir(staticDir)
			mux.HandleFunc("/", server.handleStatic)
		} else {
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>AgentFS Timeline</title></head>
<body>
<h1>AgentFS Timeline API</h1>
<p>No client build found. Run <code>cd client && npm run build</code> to build the UI.</p>
<h2>API Endpoints</h2>
<ul>
<li><a href="/api/checkpoints">/api/checkpoints</a> - List checkpoints</li>
<li>/api/manifest/:version - Get manifest for a version</li>
<li>/api/diff/:v1/:v2 - Get diff between versions</li>
<li><a href="/api/index">/api/index</a> - Full index data</li>
</ul>
</body>
</html>`)
			})
		}

		// Wrap with CORS middleware if enabled
		var handler http.Handler = mux
		if serveCorsFlag {
			handler = corsMiddleware(mux)
		}

		addr := ":" + servePortFlag
		fmt.Printf("Serving at http://localhost%s\n", addr)
		if err := http.ListenAndServe(addr, handler); err != nil {
			exitWithError(ExitError, "server error: %v", err)
		}
	},
}

func init() {
	serveCmd.Flags().StringVar(&servePortFlag, "port", "3000", "port to serve on")
	serveCmd.Flags().BoolVar(&serveCorsFlag, "cors", false, "enable CORS headers (for dev mode)")
	serveCmd.Flags().BoolVar(&serveNoCacheFlag, "no-cache", false, "force rebuild index, ignoring cache")
	serveCmd.Flags().IntVar(&serveWorkersFlag, "workers", 4, "number of parallel workers for building index")
	rootCmd.AddCommand(serveCmd)
}

// buildIndex builds the index by scanning checkpoints and computing deltas
func buildIndex(storePath, mountPath string, database *db.DB, workers int) (*Index, error) {
	storeName := context.StoreNameFromPath(storePath)

	index := &Index{
		MountPath: mountPath,
		StorePath: storePath,
		StoreName: storeName,
		Manifests: make(map[int]*Manifest),
		Deltas:    make(map[string]*Delta),
	}

	// List all checkpoints
	checkpoints, err := database.ListCheckpoints(0) // 0 = no limit
	if err != nil {
		return nil, fmt.Errorf("failed to list checkpoints: %w", err)
	}

	if len(checkpoints) == 0 {
		return index, nil
	}

	// Sort by version ascending for iteration
	sort.Slice(checkpoints, func(i, j int) bool {
		return checkpoints[i].Version < checkpoints[j].Version
	})

	// Build manifests in parallel
	manifests, err := buildManifestsParallel(checkpoints, storePath, workers)
	if err != nil {
		return nil, err
	}
	index.Manifests = manifests

	// Compute deltas between adjacent checkpoints
	var prevVersion int
	for _, cp := range checkpoints {
		manifest := index.Manifests[cp.Version]
		if manifest == nil {
			continue
		}

		var delta *Delta
		if prevVersion > 0 {
			prevManifest := index.Manifests[prevVersion]
			if prevManifest != nil {
				delta = computeDelta(prevManifest, manifest)
				key := fmt.Sprintf("v%d:v%d", prevVersion, cp.Version)
				index.Deltas[key] = delta
			}
		}

		// Build checkpoint info
		cpInfo := CheckpointInfo{
			Version:       cp.Version,
			Message:       cp.Message,
			Timestamp:     cp.CreatedAt,
			FileCount:     len(manifest.Files),
			ParentVersion: cp.ParentVersion,
		}

		if delta != nil {
			cpInfo.Summary = Summary{
				Added:    len(delta.Added),
				Modified: len(delta.Modified),
				Deleted:  len(delta.Deleted),
			}
		}

		index.Checkpoints = append(index.Checkpoints, cpInfo)
		prevVersion = cp.Version
	}

	return index, nil
}

// buildManifestsParallel builds manifests for all checkpoints using a worker pool
func buildManifestsParallel(checkpoints []*db.Checkpoint, storePath string, workers int) (map[int]*Manifest, error) {
	if workers < 1 {
		workers = 1
	}

	manifests := make(map[int]*Manifest)
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Semaphore to limit concurrent workers
	sem := make(chan struct{}, workers)

	// Progress tracking
	total := len(checkpoints)
	var completed atomic.Int32

	// Progress printer goroutine (single writer to avoid garbled output)
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				current := completed.Load()
				fmt.Printf("\rBuilding index... %d/%d checkpoints", current, total)
			}
		}
	}()

	for _, cp := range checkpoints {
		wg.Add(1)
		go func(cp *db.Checkpoint) {
			defer wg.Done()

			// Acquire semaphore
			sem <- struct{}{}
			defer func() { <-sem }()

			manifest, err := buildManifest(cp.Version, storePath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "\nwarning: failed to build manifest for v%d: %v\n", cp.Version, err)
				return
			}

			mu.Lock()
			manifests[cp.Version] = manifest
			mu.Unlock()

			completed.Add(1)
		}(cp)
	}

	wg.Wait()
	close(done)

	// Final progress update
	fmt.Printf("\rBuilding index... %d/%d checkpoints\n", completed.Load(), total)

	return manifests, nil
}

// buildManifest builds a file manifest for a checkpoint version
func buildManifest(version int, storePath string) (*Manifest, error) {
	checkpointsPath := filepath.Join(storePath, "checkpoints")
	cpPath := filepath.Join(checkpointsPath, fmt.Sprintf("v%d", version))

	// Verify checkpoint exists
	if _, err := os.Stat(cpPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("checkpoint v%d not found", version)
	}

	manifest := &Manifest{
		Version: version,
		Files:   make(map[string]*FileInfo),
	}

	// Mount the checkpoint temporarily using the differ's method (via reflection/copy)
	// For simplicity, we'll walk the checkpoint bands directly without mounting
	// This won't give us the full filesystem view, but for now let's use the diff package

	// Actually, we need to mount to walk. Let's use differ's internal method pattern
	// but create our own temporary mount

	tmpMount, cleanup, err := mountCheckpointForWalk(cpPath, storePath, version)
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		defer cleanup()
	}

	// Walk the mounted filesystem
	err = filepath.WalkDir(tmpMount, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		relPath, err := filepath.Rel(tmpMount, path)
		if err != nil || relPath == "." {
			return nil
		}

		// Skip system files
		if shouldSkipFile(relPath) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return nil
		}

		manifest.Files[relPath] = &FileInfo{
			Path:      relPath,
			Size:      info.Size(),
			Mtime:     info.ModTime().Unix(),
			Mode:      uint32(info.Mode()),
			IsDir:     info.IsDir(),
			IsSymlink: info.Mode()&os.ModeSymlink != 0,
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk checkpoint: %w", err)
	}

	return manifest, nil
}

// mountCheckpointForWalk creates a temporary mount of a checkpoint for walking
func mountCheckpointForWalk(cpPath, storePath string, version int) (string, func(), error) {
	bundlePath := filepath.Join(storePath, "data.sparsebundle")

	// Create temp bundle
	timestamp := time.Now().UnixNano()
	tmpBundle := filepath.Join(os.TempDir(), fmt.Sprintf("agentfs-serve-v%d-%d.sparsebundle", version, timestamp))
	tmpMount := filepath.Join(os.TempDir(), fmt.Sprintf("agentfs-serve-v%d-%d-mount", version, timestamp))

	// Create bundle directory
	if err := os.MkdirAll(tmpBundle, 0755); err != nil {
		return "", nil, err
	}

	// Copy Info.plist from original bundle
	infoPlist := filepath.Join(bundlePath, "Info.plist")
	infoDst := filepath.Join(tmpBundle, "Info.plist")
	if data, err := os.ReadFile(infoPlist); err == nil {
		os.WriteFile(infoDst, data, 0644)
	}

	// Copy token if exists
	tokenFile := filepath.Join(bundlePath, "token")
	tokenDst := filepath.Join(tmpBundle, "token")
	if data, err := os.ReadFile(tokenFile); err == nil {
		os.WriteFile(tokenDst, data, 0644)
	}

	// Clone bands from checkpoint using APFS reflink
	bandsDir := filepath.Join(tmpBundle, "bands")
	cmd := exec.Command("/bin/cp", "-Rc", cpPath+"/", bandsDir+"/")
	if output, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(tmpBundle)
		return "", nil, fmt.Errorf("failed to clone bands: %w\n%s", err, output)
	}

	// Create mount point
	if err := os.MkdirAll(tmpMount, 0755); err != nil {
		os.RemoveAll(tmpBundle)
		return "", nil, err
	}

	// Mount
	cmd = exec.Command("hdiutil", "attach", tmpBundle,
		"-mountpoint", tmpMount,
		"-nobrowse",
		"-quiet")
	if output, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(tmpBundle)
		os.RemoveAll(tmpMount)
		return "", nil, fmt.Errorf("failed to mount: %w\n%s", err, output)
	}

	// Cleanup function
	cleanup := func() {
		exec.Command("hdiutil", "detach", tmpMount, "-quiet").Run()
		os.RemoveAll(tmpMount)
		os.RemoveAll(tmpBundle)
	}

	return tmpMount, cleanup, nil
}

// shouldSkipFile returns true if the file should be skipped
func shouldSkipFile(path string) bool {
	base := filepath.Base(path)
	skipPatterns := []string{
		".DS_Store",
		".Spotlight-V100",
		".Trashes",
		".fseventsd",
		".TemporaryItems",
	}

	for _, pattern := range skipPatterns {
		if base == pattern {
			return true
		}
	}

	// Skip files starting with ._
	if strings.HasPrefix(base, "._") {
		return true
	}

	return false
}

// computeDelta computes the delta between two manifests
func computeDelta(from, to *Manifest) *Delta {
	delta := &Delta{
		FromVersion: from.Version,
		ToVersion:   to.Version,
		Added:       []string{},
		Modified:    []string{},
		Deleted:     []string{},
	}

	// Find modified and deleted files
	for path, fromInfo := range from.Files {
		if toInfo, exists := to.Files[path]; exists {
			// Check if modified (size or mtime changed)
			if fromInfo.Size != toInfo.Size || fromInfo.Mtime != toInfo.Mtime {
				delta.Modified = append(delta.Modified, path)
			}
		} else {
			delta.Deleted = append(delta.Deleted, path)
		}
	}

	// Find added files
	for path := range to.Files {
		if _, exists := from.Files[path]; !exists {
			delta.Added = append(delta.Added, path)
		}
	}

	// Sort for consistent output
	sort.Strings(delta.Added)
	sort.Strings(delta.Modified)
	sort.Strings(delta.Deleted)

	return delta
}

// HTTP Handlers

func (s *Server) handleCheckpoints(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.index.Checkpoints)
}

func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	// Parse version from URL: /api/manifest/5 or /api/manifest/v5
	path := strings.TrimPrefix(r.URL.Path, "/api/manifest/")
	path = strings.TrimPrefix(path, "v")

	version, err := strconv.Atoi(path)
	if err != nil {
		http.Error(w, "invalid version", http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	manifest, exists := s.index.Manifests[version]
	s.mu.RUnlock()

	if !exists {
		http.Error(w, "manifest not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(manifest)
}

func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request) {
	// Parse versions from URL: /api/diff/3/5 or /api/diff/v3/v5
	path := strings.TrimPrefix(r.URL.Path, "/api/diff/")
	parts := strings.Split(path, "/")

	if len(parts) != 2 {
		http.Error(w, "expected /api/diff/:v1/:v2", http.StatusBadRequest)
		return
	}

	v1Str := strings.TrimPrefix(parts[0], "v")
	v2Str := strings.TrimPrefix(parts[1], "v")

	v1, err1 := strconv.Atoi(v1Str)
	v2, err2 := strconv.Atoi(v2Str)

	if err1 != nil || err2 != nil {
		http.Error(w, "invalid version numbers", http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	// Try to find exact delta
	key := fmt.Sprintf("v%d:v%d", v1, v2)
	delta, exists := s.index.Deltas[key]

	if !exists {
		// Compute delta on the fly from manifests
		m1, ok1 := s.index.Manifests[v1]
		m2, ok2 := s.index.Manifests[v2]
		s.mu.RUnlock()

		if !ok1 || !ok2 {
			http.Error(w, "manifest not found for one or both versions", http.StatusNotFound)
			return
		}

		delta = computeDelta(m1, m2)
	} else {
		s.mu.RUnlock()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(delta)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return full index including manifests
	type fullIndex struct {
		MountPath   string                `json:"mountPath"`
		StorePath   string                `json:"storePath"`
		StoreName   string                `json:"storeName"`
		Checkpoints []CheckpointInfo      `json:"checkpoints"`
		Manifests   map[string]*Manifest  `json:"manifests"`
		Deltas      map[string]*Delta     `json:"deltas"`
	}

	idx := fullIndex{
		MountPath:   s.index.MountPath,
		StorePath:   s.index.StorePath,
		StoreName:   s.index.StoreName,
		Checkpoints: s.index.Checkpoints,
		Manifests:   make(map[string]*Manifest),
		Deltas:      s.index.Deltas,
	}

	// Convert int keys to string keys for JSON
	for v, m := range s.index.Manifests {
		idx.Manifests[fmt.Sprintf("v%d", v)] = m
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(idx)
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	// Serve index.html for root and any non-file paths (SPA routing)
	path := r.URL.Path
	if path == "/" {
		path = "/index.html"
	}

	// Check if the file exists
	f, err := s.staticFS.Open(path)
	if err != nil {
		// Serve index.html for SPA routing
		path = "/index.html"
	} else {
		f.Close()
	}

	http.ServeFile(w, r, filepath.Join("client/dist", path))
}

// corsMiddleware adds CORS headers for development
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
