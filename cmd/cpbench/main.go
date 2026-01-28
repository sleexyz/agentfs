// cpbench benchmarks checkpoint creation with file hashing
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/agentfs/agentfs/internal/filehash"
)

func main() {
	dir := flag.String("dir", ".", "Directory to hash (simulating mount point)")
	workers := flag.Int("workers", 4, "Number of parallel workers")
	dbPath := flag.String("db", "/tmp/cpbench.db", "Path to test database")
	incremental := flag.Bool("incremental", false, "Run in incremental mode (second run)")
	flag.Parse()

	absDir, err := filepath.Abs(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("=== Checkpoint + File Hash Benchmark ===\n")
	fmt.Printf("Directory: %s\n", absDir)
	fmt.Printf("Workers: %d\n", *workers)
	fmt.Printf("Incremental: %v\n", *incremental)
	fmt.Println()

	// Open database
	db, err := sql.Open("sqlite3", *dbPath+"?_foreign_keys=on")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Create tables
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS checkpoints (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			store_id TEXT NOT NULL,
			version INTEGER NOT NULL,
			message TEXT,
			created_at INTEGER NOT NULL
		);
	`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating checkpoints table: %v\n", err)
		os.Exit(1)
	}

	manager := filehash.NewManager(db)
	if err := manager.MigrateSchema(); err != nil {
		fmt.Fprintf(os.Stderr, "Error migrating schema: %v\n", err)
		os.Exit(1)
	}

	// Get previous hashes if incremental
	var prevHashes map[string]*filehash.FileVersion
	var lastCheckpointID int64 = 0

	if *incremental {
		// Get the last checkpoint ID
		err := db.QueryRow("SELECT id FROM checkpoints ORDER BY id DESC LIMIT 1").Scan(&lastCheckpointID)
		if err == nil && lastCheckpointID > 0 {
			fmt.Printf("Loading previous hashes from checkpoint %d...\n", lastCheckpointID)
			prevHashes, err = manager.GetFileVersions(lastCheckpointID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error loading previous hashes: %v\n", err)
			} else {
				fmt.Printf("Loaded %d previous file hashes\n", len(prevHashes))
			}
		}
	}

	// Simulate checkpoint creation: create a new checkpoint record
	createStart := time.Now()
	result, err := db.Exec(`
		INSERT INTO checkpoints (store_id, version, message, created_at)
		VALUES ('test-store', (SELECT COALESCE(MAX(version), 0) + 1 FROM checkpoints WHERE store_id = 'test-store'), 'benchmark', ?)
	`, time.Now().Unix())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating checkpoint: %v\n", err)
		os.Exit(1)
	}
	checkpointID, _ := result.LastInsertId()
	fmt.Printf("Created checkpoint %d\n", checkpointID)

	// Hash all files
	fmt.Println("\n--- File Hashing ---")

	opts := filehash.HashOptions{
		Workers:    *workers,
		SkipDirs:   filehash.DefaultSkipDirs(),
		PrevHashes: prevHashes,
	}

	results, hashDur, err := manager.HashDirectory(absDir, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error hashing directory: %v\n", err)
		os.Exit(1)
	}

	var totalSize int64
	var successCount int
	for _, r := range results {
		if r.Error == nil {
			successCount++
			totalSize += r.Size
		}
	}

	fmt.Printf("Hashed %d files (%.2f MB) in %v\n", successCount, float64(totalSize)/(1024*1024), hashDur)

	// Store in database
	fmt.Println("\n--- Database Insert ---")
	storeStart := time.Now()
	if err := manager.StoreFileVersions(checkpointID, results); err != nil {
		fmt.Fprintf(os.Stderr, "Error storing file versions: %v\n", err)
		os.Exit(1)
	}
	storeDur := time.Since(storeStart)
	fmt.Printf("Stored %d file versions in %v\n", successCount, storeDur)

	// Total time
	totalDur := time.Since(createStart)
	fmt.Println("\n=== SUMMARY ===")
	fmt.Printf("%-25s %15v\n", "Checkpoint record:", time.Since(createStart)-hashDur-storeDur)
	fmt.Printf("%-25s %15v\n", "File hashing:", hashDur)
	fmt.Printf("%-25s %15v\n", "Database insert:", storeDur)
	fmt.Printf("%-25s %15v\n", "TOTAL:", totalDur)
	fmt.Println()

	// Check against target
	targetTime := 200 * time.Millisecond
	targetFiles := 10000

	// Project to 10k files
	timePerFile := totalDur / time.Duration(len(results))
	projectedTime := time.Duration(targetFiles) * timePerFile

	fmt.Println("=== FEASIBILITY ===")
	fmt.Printf("Target: %d files in <%v\n", targetFiles, targetTime)
	fmt.Printf("Actual: %d files in %v\n", len(results), totalDur)
	fmt.Printf("Projected for %d files: %v\n", targetFiles, projectedTime.Round(time.Millisecond))

	if projectedTime < targetTime {
		fmt.Println("✓ FEASIBLE: Checkpoint with file hashing meets target")
	} else {
		fmt.Printf("✗ EXCEEDS TARGET by %.2fx\n", float64(projectedTime)/float64(targetTime))

		// Show incremental projection
		if !*incremental && prevHashes == nil {
			hashTimePerFile := hashDur / time.Duration(len(results))
			mtimeCheckTime := 16 * time.Millisecond // From earlier benchmark
			changedPct := 0.05
			changedFiles := int(float64(targetFiles) * changedPct)

			incrementalHash := time.Duration(changedFiles) * hashTimePerFile
			incrementalTotal := mtimeCheckTime + incrementalHash + storeDur

			fmt.Printf("\n📊 Incremental projection (%.0f%% changed):\n", changedPct*100)
			fmt.Printf("   Mtime check: ~%v\n", mtimeCheckTime)
			fmt.Printf("   Hash %d files: ~%v\n", changedFiles, incrementalHash.Round(time.Millisecond))
			fmt.Printf("   DB insert: ~%v\n", storeDur.Round(time.Millisecond))
			fmt.Printf("   TOTAL: ~%v\n", incrementalTotal.Round(time.Millisecond))

			if incrementalTotal < targetTime {
				fmt.Println("   ✓ Incremental mode would meet target")
			}
		}
	}

	// Verify data integrity
	fmt.Println("\n--- Verification ---")
	count, _ := manager.CountFiles(checkpointID)
	totalStored, _ := manager.GetTotalSize(checkpointID)
	fmt.Printf("Files in DB: %d (expected %d)\n", count, successCount)
	fmt.Printf("Total size in DB: %.2f MB (expected %.2f MB)\n", float64(totalStored)/(1024*1024), float64(totalSize)/(1024*1024))
}
