# Spike 2: Checkpoint Performance Benchmark

## Goal

Validate that we can checkpoint a realistic project in <100ms.

**Success looks like:** Measured data showing mtime scan + band hashing completes in <100ms for a medium-sized project (10k files, ~1GB).

---

## Prerequisites

- [x] Spike 1 complete (sparse bundle basics understood)

---

## Tasks

### Setup Realistic Test Data

- [ ] Create a 10GB sparse bundle with 4MB bands
- [ ] Populate with realistic project data (~10k files, ~1GB total)
  - Clone a real Next.js or similar project
  - Include: source files, node_modules, .git directory
- [ ] Record: total bands created, total size

### Benchmark: Baseline Full Scan

- [ ] Time: list all bands in bands/ directory
- [ ] Time: hash ALL bands with SHA256
- [ ] Record baseline numbers (this is the "slow path" we want to avoid)

### Benchmark: Incremental Detection

- [ ] Modify a single source file (simulate typical edit)
- [ ] Time: mtime-based dirty detection (find bands changed since last checkpoint)
- [ ] Time: hash only the dirty bands
- [ ] Record: total time for detect + hash

### Benchmark: Multiple Changes

- [ ] Modify 5 files across the project
- [ ] Repeat mtime + hash timing
- [ ] Modify 20 files (simulate larger refactor)
- [ ] Repeat timing
- [ ] Document: how does checkpoint time scale with number of changes?

### Test APFS Reflink

- [ ] Try `cp -c` (clone) on a single band file
- [ ] Verify: is the clone instant? Does it share blocks?
- [ ] Try cloning entire bands/ directory
- [ ] Document: can we use reflinks for instant local snapshots?

### Analysis

- [ ] Create performance summary table
- [ ] Compare against <100ms target
- [ ] Identify bottlenecks if target not met
- [ ] Write recommendations to `knowledge/checkpoint-benchmark.md`
- [ ] Update `LEARNINGS.md` with performance data

---

## Reference

### Target Metrics

| Operation | Target | Notes |
|-----------|--------|-------|
| mtime scan (all bands) | <10ms | Just stat() calls |
| Hash dirty bands (3-10 bands) | <50ms | ~12-40MB at 4MB/band |
| Total checkpoint | <100ms | mtime + hash + manifest write |

### Test Data Options

```bash
# Option 1: Clone a real project
git clone --depth 1 https://github.com/vercel/next.js /tmp/nextjs-test
# ~50k files, but we can use a subset

# Option 2: Generate synthetic data
# Mix of small files (source) and large files (assets)

# Option 3: Copy your own project
cp -r ~/projects/some-real-project /Volumes/testfs/
```

### Timing Commands

```bash
# Time mtime scan
time find bands/ -type f -newer /tmp/last-checkpoint-marker -print

# Time hashing
time shasum -a 256 bands/{0,1,2,3}  # specific bands

# High-resolution timing in script
start=$(python3 -c 'import time; print(time.time())')
# ... operation ...
end=$(python3 -c 'import time; print(time.time())')
echo "Elapsed: $(echo "$end - $start" | bc) seconds"
```

### Reflink Test

```bash
# Test if APFS supports cloning band files
cp -c bands/0 bands/0.clone
ls -la bands/0 bands/0.clone  # same size
# Check if they share blocks (no additional disk usage)
```

---

## Output

Create `knowledge/checkpoint-benchmark.md` with:
- Test environment specs (macOS version, disk type, CPU)
- Raw timing data for each operation
- Scaling analysis (1 file change vs 5 vs 20)
- Reflink test results
- Bottleneck analysis
- Recommendations for AgentFS implementation
