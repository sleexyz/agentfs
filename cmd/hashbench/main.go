// hashbench benchmarks file hashing strategies for content-addressing spike
package main

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type FileHash struct {
	Path   string
	Hash   string
	Size   int64
	Mtime  time.Time
}

type BenchResult struct {
	Name       string
	Duration   time.Duration
	FileCount  int
	TotalBytes int64
	Throughput float64 // MB/s
}

func main() {
	dir := flag.String("dir", ".", "Directory to scan")
	workers := flag.Int("workers", runtime.NumCPU(), "Number of parallel workers")
	skipDotDirs := flag.Bool("skip-dot", true, "Skip .git, node_modules, etc.")
	runIncremental := flag.Bool("incremental", false, "Run incremental benchmark (simulate changed files)")
	changePercent := flag.Float64("change-pct", 5.0, "Percent of files to mark as 'changed' for incremental")
	flag.Parse()

	absDir, err := filepath.Abs(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("=== File Hashing Benchmark ===\n")
	fmt.Printf("Directory: %s\n", absDir)
	fmt.Printf("Workers: %d\n", *workers)
	fmt.Printf("Skip dot dirs: %v\n", *skipDotDirs)
	fmt.Println()

	// Collect all file paths first
	fmt.Print("Collecting files... ")
	collectStart := time.Now()
	var files []string
	err = filepath.WalkDir(absDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if d.IsDir() {
			name := d.Name()
			if *skipDotDirs && (name == ".git" || name == "node_modules" || name == ".next" || name == "vendor" || name == "__pycache__" || name == ".venv") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error walking: %v\n", err)
		os.Exit(1)
	}
	collectDur := time.Since(collectStart)
	fmt.Printf("found %d files in %v\n\n", len(files), collectDur)

	// Get file sizes for throughput calculation
	var totalBytes int64
	for _, f := range files {
		if info, err := os.Stat(f); err == nil {
			totalBytes += info.Size()
		}
	}
	fmt.Printf("Total data: %.2f MB\n\n", float64(totalBytes)/(1024*1024))

	var results []BenchResult

	// Benchmark 1: Sequential hashing
	fmt.Println("--- Benchmark 1: Sequential Hashing ---")
	seqResult := benchSequential(files)
	seqResult.TotalBytes = totalBytes
	seqResult.Throughput = float64(totalBytes) / (1024 * 1024) / seqResult.Duration.Seconds()
	results = append(results, seqResult)
	fmt.Printf("Time: %v | Files: %d | Throughput: %.2f MB/s\n\n", seqResult.Duration, seqResult.FileCount, seqResult.Throughput)

	// Benchmark 2: Parallel hashing (various worker counts)
	workerCounts := []int{2, 4, 8, 16, 32}
	if *workers > 32 {
		workerCounts = append(workerCounts, *workers)
	}

	for _, w := range workerCounts {
		fmt.Printf("--- Benchmark 2.%d: Parallel Hashing (%d workers) ---\n", w, w)
		parResult := benchParallel(files, w)
		parResult.TotalBytes = totalBytes
		parResult.Throughput = float64(totalBytes) / (1024 * 1024) / parResult.Duration.Seconds()
		parResult.Name = fmt.Sprintf("Parallel (%d workers)", w)
		results = append(results, parResult)
		fmt.Printf("Time: %v | Throughput: %.2f MB/s | Speedup: %.2fx\n\n", parResult.Duration, parResult.Throughput, float64(seqResult.Duration)/float64(parResult.Duration))
	}

	// Benchmark 3: Mtime-only check (simulate incremental)
	fmt.Println("--- Benchmark 3: Mtime-only Check ---")
	mtimeResult := benchMtimeOnly(files)
	results = append(results, mtimeResult)
	fmt.Printf("Time: %v | Files: %d\n\n", mtimeResult.Duration, mtimeResult.FileCount)

	if *runIncremental {
		// Benchmark 4: Incremental hashing (only "changed" files)
		changedCount := int(float64(len(files)) * (*changePercent / 100.0))
		if changedCount < 1 {
			changedCount = 1
		}
		fmt.Printf("--- Benchmark 4: Incremental Hashing (%.1f%% = %d files) ---\n", *changePercent, changedCount)
		incResult := benchIncremental(files, changedCount, *workers)
		incResult.Name = fmt.Sprintf("Incremental (%.1f%% changed)", *changePercent)
		results = append(results, incResult)
		fmt.Printf("Time: %v | Files hashed: %d\n\n", incResult.Duration, incResult.FileCount)
	}

	// Benchmark 5: Hash + stat combined
	fmt.Println("--- Benchmark 5: Hash + Stat Combined (parallel) ---")
	combResult := benchCombined(files, *workers)
	combResult.TotalBytes = totalBytes
	combResult.Throughput = float64(totalBytes) / (1024 * 1024) / combResult.Duration.Seconds()
	results = append(results, combResult)
	fmt.Printf("Time: %v | Throughput: %.2f MB/s\n\n", combResult.Duration, combResult.Throughput)

	// Summary
	fmt.Println("=== SUMMARY ===")
	fmt.Printf("%-35s %15s %12s %12s\n", "Benchmark", "Duration", "Throughput", "Speedup")
	fmt.Println(string(make([]byte, 80)))
	for i, r := range results {
		speedup := "-"
		if r.Duration > 0 && seqResult.Duration > 0 {
			speedup = fmt.Sprintf("%.2fx", float64(seqResult.Duration)/float64(r.Duration))
		}
		throughput := "-"
		if r.Throughput > 0 {
			throughput = fmt.Sprintf("%.2f MB/s", r.Throughput)
		}
		fmt.Printf("%-35s %15v %12s %12s\n", r.Name, r.Duration.Round(time.Millisecond), throughput, speedup)
		if i == 0 {
			fmt.Println(string(make([]byte, 80)))
		}
	}

	// Feasibility check
	fmt.Println()
	fmt.Println("=== FEASIBILITY CHECK ===")
	targetFiles := 10000
	targetTime := 200 * time.Millisecond

	// Use the best parallel result
	var bestParallel BenchResult
	for _, r := range results {
		if r.Throughput > bestParallel.Throughput {
			bestParallel = r
		}
	}

	timePerFile := bestParallel.Duration / time.Duration(len(files))
	projectedTime := time.Duration(targetFiles) * timePerFile

	fmt.Printf("Target: %d files in <%v\n", targetFiles, targetTime)
	fmt.Printf("Best parallel: %s\n", bestParallel.Name)
	fmt.Printf("Projected time for %d files: %v\n", targetFiles, projectedTime.Round(time.Millisecond))
	if projectedTime < targetTime {
		fmt.Println("✓ FEASIBLE: File hashing should meet performance target")
	} else {
		fmt.Printf("✗ NEEDS OPTIMIZATION: Projected time exceeds target by %.2fx\n", float64(projectedTime)/float64(targetTime))
		fmt.Printf("  - Consider incremental hashing (mtime-based dirty detection)\n")
		fmt.Printf("  - Mtime-only check: %v for %d files\n", mtimeResult.Duration, len(files))
	}
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), n, nil
}

func benchSequential(files []string) BenchResult {
	start := time.Now()
	count := 0
	for _, f := range files {
		if _, _, err := hashFile(f); err == nil {
			count++
		}
	}
	return BenchResult{
		Name:      "Sequential",
		Duration:  time.Since(start),
		FileCount: count,
	}
}

func benchParallel(files []string, workers int) BenchResult {
	start := time.Now()

	var wg sync.WaitGroup
	fileCh := make(chan string, workers*2)
	var count atomic.Int64

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range fileCh {
				if _, _, err := hashFile(f); err == nil {
					count.Add(1)
				}
			}
		}()
	}

	for _, f := range files {
		fileCh <- f
	}
	close(fileCh)
	wg.Wait()

	return BenchResult{
		Name:      "Parallel",
		Duration:  time.Since(start),
		FileCount: int(count.Load()),
	}
}

func benchMtimeOnly(files []string) BenchResult {
	start := time.Now()
	count := 0
	for _, f := range files {
		if _, err := os.Stat(f); err == nil {
			count++
		}
	}
	return BenchResult{
		Name:      "Mtime-only check",
		Duration:  time.Since(start),
		FileCount: count,
	}
}

func benchIncremental(files []string, changedCount int, workers int) BenchResult {
	// Simulate: stat all files, hash only "changed" ones
	start := time.Now()

	// First: stat all files to check mtime
	for _, f := range files {
		os.Stat(f)
	}

	// Then: hash only the "changed" files (first N)
	changedFiles := files[:changedCount]

	var wg sync.WaitGroup
	fileCh := make(chan string, workers*2)
	var count atomic.Int64

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range fileCh {
				if _, _, err := hashFile(f); err == nil {
					count.Add(1)
				}
			}
		}()
	}

	for _, f := range changedFiles {
		fileCh <- f
	}
	close(fileCh)
	wg.Wait()

	return BenchResult{
		Name:      "Incremental",
		Duration:  time.Since(start),
		FileCount: int(count.Load()),
	}
}

func benchCombined(files []string, workers int) BenchResult {
	start := time.Now()

	type result struct {
		path  string
		hash  string
		size  int64
		mtime time.Time
	}

	var wg sync.WaitGroup
	fileCh := make(chan string, workers*2)
	resultCh := make(chan result, workers*2)
	var count atomic.Int64

	// Workers: hash and stat
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range fileCh {
				hash, size, err := hashFile(f)
				if err != nil {
					continue
				}
				info, err := os.Stat(f)
				if err != nil {
					continue
				}
				resultCh <- result{
					path:  f,
					hash:  hash,
					size:  size,
					mtime: info.ModTime(),
				}
				count.Add(1)
			}
		}()
	}

	// Collector
	var results []result
	done := make(chan struct{})
	go func() {
		for r := range resultCh {
			results = append(results, r)
		}
		close(done)
	}()

	for _, f := range files {
		fileCh <- f
	}
	close(fileCh)
	wg.Wait()
	close(resultCh)
	<-done

	// Sort by path (simulate DB insert order)
	sort.Slice(results, func(i, j int) bool {
		return results[i].path < results[j].path
	})

	return BenchResult{
		Name:      "Combined (hash+stat+sort)",
		Duration:  time.Since(start),
		FileCount: int(count.Load()),
	}
}
