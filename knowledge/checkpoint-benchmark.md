# Checkpoint Performance Benchmark Results

## Test Environment

| Property | Value |
|----------|-------|
| Date | January 27, 2026 |
| macOS Version | 15.7.1 |
| CPU | Apple M4 Max |
| Cores | 16 |
| RAM | 48 GB |
| Filesystem | APFS |

## Test Data

| Metric | Value |
|--------|-------|
| Sparse Bundle Size | 10GB (configured) |
| Band Size | 8MB (APFS default) |
| Files | 36,076 |
| Data Size | 626MB |
| Total Bands | 87 |
| Bands Directory Size | 681MB |

Data source: Next.js repository clone with node_modules in examples.

---

## Benchmark Results

### Baseline: Full Scan (Slow Path)

This is what we want to AVOID - scanning and hashing everything on each checkpoint.

| Operation | Time (avg of 3) |
|-----------|-----------------|
| List all bands | ~14ms |
| Stat all bands (mtime) | ~14ms |
| Hash ALL bands (SHA256, 681MB) | **1,285ms** |

**Finding:** Full hash is the dominant cost at 1.3 seconds. This is unacceptable for frequent checkpoints.

---

### Incremental Detection (Fast Path)

Using mtime-based dirty detection + selective hashing.

#### Single File Change

| Metric | Value |
|--------|-------|
| Dirty Bands | 3 |
| Dirty Size | 16.3MB |
| mtime scan | 12ms |
| Hash dirty bands | 52ms |
| **Total** | **64ms** ✅ |

#### 5 File Changes

| Metric | Value |
|--------|-------|
| Dirty Bands | 6 |
| Dirty Size | 40.4MB |
| mtime scan | 12ms |
| Hash dirty bands | 100ms |
| **Total** | **112ms** |

#### 20 File Changes (same area)

| Metric | Value |
|--------|-------|
| Dirty Bands | 5 |
| Dirty Size | 32.5MB |
| mtime scan | 11ms |
| Hash dirty bands | 19ms |
| **Total** | **30ms** ✅ |

#### 60 Files Across Directories

| Metric | Value |
|--------|-------|
| Dirty Bands | 8 |
| Dirty Size | 56.5MB |
| mtime scan | 12ms |
| Hash dirty bands | 20ms |
| **Total** | **32ms** ✅ |

**Key Finding:** mtime scan is consistent at ~12ms regardless of change volume. Hash time scales with dirty band count and whether data is cached.

---

### APFS Reflink (Clone) Performance

Testing `cp -c` (clone) for instant snapshots.

#### Single Band Clone (8MB)

| Operation | Time |
|-----------|------|
| Clone (cp -c) | 12ms |
| Regular copy | 13ms |

Note: For small files, clone overhead is similar to copy.

#### 100MB File Clone

| Operation | Time |
|-----------|------|
| Clone (cp -c) | **0.002s** |
| Regular copy | 0.014s |

**7x faster** for larger files.

#### Full Bands Directory Clone (680MB, 87 files)

| Metric | Value |
|--------|-------|
| Clone time | **19ms** |
| Disk space used | **0 bytes** |

**Critical Finding:** We can snapshot 680MB of bands in 19ms with zero additional disk usage.

---

## Performance Summary vs Targets

| Operation | Target | Actual | Status |
|-----------|--------|--------|--------|
| mtime scan (all bands) | <10ms | 12ms | ⚠️ Close |
| Hash dirty bands (3-10 bands) | <50ms | 20-100ms | ⚠️ Varies |
| Total checkpoint | <100ms | 30-112ms | ⚠️ Borderline |

---

## Analysis

### What Works Well

1. **mtime detection is fast and consistent** - Always ~12ms regardless of how many files changed. The filesystem handles this efficiently.

2. **Band locality helps** - When changes are in related files (common in refactors), they often land in the same bands. 20 file changes only touched 5 bands.

3. **APFS clones are instant** - Creating a full snapshot of all bands takes 19ms and uses zero disk space. This enables cheap snapshot history.

4. **Cached data hashes fast** - After first access, band hashing is significantly faster (19ms vs 100ms for same data volume).

### Bottlenecks Identified

1. **Cold cache hash performance** - First hash of dirty bands can be slow (~100ms for 40MB). Mitigated by filesystem caching in practice.

2. **mtime scan slightly over target** - 12ms vs 10ms target. Could optimize with:
   - Parallel stat() calls
   - Caching directory listing
   - Using FSEvents to track changes instead of scanning

3. **Band count scales with project** - 87 bands for 680MB means larger projects will have more bands to scan.

### Scaling Analysis

| Files Changed | Dirty Bands | Checkpoint Time |
|---------------|-------------|-----------------|
| 1 | 3 | 64ms |
| 5 | 6 | 112ms |
| 20 (local) | 5 | 30ms |
| 60 (spread) | 8 | 32ms |

**Pattern:** Checkpoint time depends more on dirty band data size than file count. Cached data hashes very fast.

---

## Recommendations

### 1. Use mtime + Selective Hashing (Confirmed)

The spike validates this approach. mtime scan is cheap, and hashing only dirty bands keeps us near the 100ms target.

### 2. Leverage APFS Clones for Snapshots

Instead of copying bands for history, use `cp -c` to create instant, space-free clones. This enables:
- Cheap snapshot history (keep 10+ snapshots at zero disk cost)
- Instant rollback (just swap snapshot paths)
- Full checkpoint = mtime scan + clone dirty bands (not hash)

### 3. Consider FSEvents for Change Detection

Instead of scanning all bands for mtime, use macOS FSEvents to watch the bands directory. This would give us:
- O(1) change detection (notified immediately)
- No scan overhead
- Even faster checkpoints

### 4. Implement Band-Level Caching

Keep a small hash cache of recently-touched bands. Since most editing happens in the same files, we often re-hash the same bands.

### 5. Warm the Cache Proactively

Background hash bands that were recently modified. When checkpoint is requested, hashes are already computed.

---

## Recommended Checkpoint Strategy

```
On Checkpoint:
1. [0ms] Check FSEvents queue for changed bands (or scan mtime: 12ms)
2. [19ms] Clone dirty bands to snapshot location (APFS instant clone)
3. [0ms] Write manifest with band -> clone mappings
4. [0ms] Queue background hash computation for dirty bands

Total: ~20-30ms for typical edits
```

This beats the 100ms target significantly by deferring hashing.

---

## Raw Benchmark Data

See `ralph-explore.log` for complete timing output.
