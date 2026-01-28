# AgentFS Learnings

Accumulated knowledge from research and spikes.

---

## JuiceFS Architecture (2025-01-27)

**Source:** Deep dive into JuiceFS codebase

### Key Insights

1. **Immutable blocks enable instant snapshots**
   - JuiceFS never modifies blocks in place
   - Write → new slice created → new blocks uploaded → metadata updated
   - Old blocks shared by snapshots until garbage collected

2. **Data hierarchy: File → Chunk → Slice → Block → Object**
   - Chunk: 64 MiB logical unit (for fast lookup by offset)
   - Slice: Write unit with unique ID (enables COW)
   - Block: 4 MiB physical unit (IMMUTABLE)
   - Object: Actual bytes in S3/GCS

3. **Clone is metadata-only**
   - `juicefs clone` copies chunk→slice references
   - Actual blocks shared until modified (COW)
   - Instant regardless of file size

4. **Local-first with async flush**
   - Writes buffered in memory (300MB default)
   - Auto-flush on: block full, time elapsed, buffer pressure
   - Metadata updated AFTER data is durable

5. **Reference counting for cleanup**
   - SliceRef tracks how many files reference each slice
   - refs=0 → eligible for garbage collection

### Applicability to AgentFS

- Sparse bundle bands ≈ JuiceFS blocks (already chunked!)
- Content-address bands ourselves (SHA256 → hash)
- Snapshot = manifest of {band_idx: hash}
- COW via content-addressing (unchanged bands share hash)

---

## Sparse Bundle Format (2025-01-27, updated 2026-01-27)

**Source:** Apple documentation, experimentation, spike validation

### Structure

```
myapp.sparsebundle/
├── Info.plist          # metadata (band size, total size)
├── Info.bckup          # backup of Info.plist
├── token               # mount lock
├── lock                # lock file
└── bands/
    ├── 0               # band files (hex naming)
    ├── 1
    ├── 7f              # special metadata bands
    └── ...
```

### Key Facts

- Band size configurable at creation via `sparse-band-size` imagekey (in 512-byte sectors)
- Bands are plain files → can hash/upload individually
- Only touched bands exist (sparse allocation)
- APFS inside bundle supports snapshots via `tmutil`
- Band names are **hexadecimal** (0, 1, ..., 9, a, ..., f, 10, ...)
- Initial bands (7f, 80, ff) contain APFS volume metadata

### Validated Answers (Spike 2026-01-27)

- [x] **How does APFS allocate across bands?** → **Contiguous allocation** from band 0 upward for data. Metadata bands (7f, 80, ff) are pre-allocated at creation.
- [x] **What's the mtime granularity for dirty detection?** → **Nanosecond precision** (e.g., 1769566926.091853016). Sufficient for sub-second change detection.
- [x] **Can we reflink/clone individual bands on APFS?** → **YES!** `cp -c` works perfectly. 680MB clones in 19ms with zero additional disk usage.

### Band Change Behavior

**Critical insight:** A single file modification touches 2-3 bands:
1. **Band 0** - Always changes (filesystem metadata: mtime, directory entries)
2. **Data band** - The band containing the modified bytes
3. **Journal band** - APFS transaction journal (location varies)

This means ~3 bands changed per write, regardless of write size. Plan for this in sync strategies.

---

## macOS Constraints (2025-01-27)

**Source:** Web research

### What Works Without Kext

- ✅ Sparse bundles (native)
- ✅ hdiutil mount/unmount
- ✅ FSEvents for file watching
- ✅ APFS snapshots inside mounted volumes
- ⚠️ FUSE-T (kext-less but has quirks)
- ⚠️ NFS loopback (works but some tool issues)

### What Requires Kext

- ❌ macFUSE (traditional)
- ❌ Custom filesystem drivers

### Installation Friction

- Kext requires: System Preferences → Security → Allow
- Sometimes requires reboot
- Corporate machines often have MDM blocking kexts

---

## Comparison: Approaches

| Approach | Checkpoint Speed | Install Friction | COW | Notes |
|----------|-----------------|------------------|-----|-------|
| FSEvents + CAS | Medium (scan) | None | No | Simplest |
| NFS Loopback | Fast | Low | Yes | Some tool issues |
| FUSE-T | Fast | Low | Yes | Quirks with some apps |
| Sparse Bundle | Fast | None | Yes* | *via content-addressing |

**Current recommendation:** Sparse Bundle (Direction E)

---

## To Be Validated

- [x] Band hashing performance at scale → **Validated: 680MB hashes in 1.3s full, 30-64ms incremental**
- [ ] S3 upload throughput for 4MB bands
- [x] APFS reflink support for band files → **Validated: Works perfectly, zero disk cost**
- [ ] Sparse bundle corruption recovery

---

## Sparse Bundle Spike Results (2026-01-27)

**Source:** Hands-on experimentation with hdiutil, SHA256 hashing

### Summary

Sparse bundles are viable for incremental sync. Key findings:

| Finding | Implication for AgentFS |
|---------|------------------------|
| Bands created lazily | Minimal initial storage, grows with data |
| Contiguous allocation | Predictable band usage patterns |
| mtime has ns precision | Fast dirty detection without hashing |
| ~3 bands change per write | Expect metadata overhead on every sync |
| Hash comparison works | Definitive change detection via SHA256 |
| Unmount/remount preserves data | Safe for sync workflows |

### Recommended Change Detection Strategy

```
1. Compare band mtimes with last sync (fast)
2. Hash only bands with changed mtime (accurate)
3. Sync bands with different hash (minimal transfer)
```

### Band Size Trade-offs

| Size | Pros | Cons |
|------|------|------|
| 1-4 MB | Fine-grained sync, less wasted transfer | More bands to track |
| 8-64 MB | Fewer bands, simpler management | More data per change |

**Recommendation:** 4 MB bands for general use, 8 MB for large-file workloads.

See `knowledge/sparse-bundle-spike.md` for full details and code examples.

---

## Checkpoint Performance Benchmark (2026-01-27)

**Source:** Spike 2 - hands-on benchmarking with 36k file project

### Test Environment

- Apple M4 Max, 16 cores, 48GB RAM
- APFS filesystem
- 10GB sparse bundle with 8MB bands
- Test data: Next.js repo + node_modules (36,076 files, 626MB, 87 bands)

### Key Results

| Operation | Time | Notes |
|-----------|------|-------|
| List all bands | 14ms | 87 bands |
| Stat all bands (mtime) | 14ms | Consistent |
| Hash ALL bands (full scan) | **1,285ms** | Slow path to avoid |
| Incremental (1 file change) | **64ms** | 3 dirty bands, 16MB |
| Incremental (5 files) | 112ms | 6 dirty bands, 40MB |
| Incremental (60 files spread) | **32ms** | 8 dirty bands, cached |
| Clone entire bands dir (680MB) | **19ms** | APFS reflink, 0 disk cost |

### APFS Reflink Discovery

**Major finding:** APFS `cp -c` creates instant, zero-space copies:

```bash
# Clone 680MB of bands in 19ms, using 0 bytes of additional space
cp -Rc bands/ snapshot/
```

This enables:
- Cheap snapshot history (keep 10+ snapshots at zero disk cost)
- Instant rollback (swap paths)
- Defer hashing until background sync

### Performance vs Targets

| Target | Actual | Status |
|--------|--------|--------|
| mtime scan <10ms | 12ms | ⚠️ Close |
| Hash dirty bands <50ms | 20-100ms | ⚠️ Varies |
| Total checkpoint <100ms | 30-112ms | ✅ Achievable |

### Recommended Strategy

```
On Checkpoint:
1. [12ms] mtime scan (or FSEvents for 0ms)
2. [19ms] Clone dirty bands with APFS reflink
3. [0ms] Write manifest
4. [background] Queue hash computation

Total: ~30ms typical
```

### Scaling Analysis

Checkpoint time depends on **dirty band data size**, not file count. Many file changes in related code often land in the same bands.

See `knowledge/checkpoint-benchmark.md` for full analysis.

---

## Sync Architecture Decision (2026-01-27)

**Source:** Deep analysis of Syncthing, Mutagen, JuiceFS conflict handling

### The Problem: Band-Level Sync + Conflicts = Corruption

If two machines modify the same sparse bundle band:
1. Syncthing creates `.sync-conflict-<date>-<device>` file
2. Sparse bundle expects `bands/0`, gets `bands/0.sync-conflict-...`
3. **Filesystem is corrupted**

This is a fundamental incompatibility between:
- Syncthing's conflict resolution (rename files)
- Sparse bundle's expectations (exact band filenames)

### How JuiceFS Solves This

JuiceFS uses a **metadata server** for coordination:
- POSIX locking (flock, fcntl)
- Close-to-open consistency
- Chunks are immutable (no conflicts at data layer)
- Conflicts resolved at metadata layer via locks

But JuiceFS requires FUSE (macFUSE = kext = security dialogs).

### Approaches Evaluated

| Approach | Multi-Writer | No FUSE | Complexity |
|----------|-------------|---------|------------|
| Single-Writer | ❌ | ✅ | Low |
| JuiceFS | ✅ | ❌ | Medium |
| File-Level Sync | ⚠️ Per-file conflicts | ✅ | Low |
| Event Sourcing | ✅ | ✅ | Very High |

### Decision: Path 1 — File-Level Sync

**Architecture:**
- Sparse bundles for **local checkpointing only** (instant, APFS reflinks)
- Syncthing syncs **files inside the mounted volume** (not bands)
- Conflicts are per-file, manageable (Syncthing's `.sync-conflict` files)

**Trade-offs:**
- ✅ No band corruption
- ✅ No FUSE required
- ✅ Simple architecture
- ⚠️ Can't restore exact band state from remote
- ⚠️ Remote sync is file-level, not block-level

**This is the right trade-off for v1.**

### Implications

1. **Checkpointing is local-only** — APFS reflinks for instant snapshots
2. **Syncthing syncs files** — ~/projects/myapp/**/* synced to remote
3. **Sparse bundle is invisible** — User works with files, checkpointing happens underneath
4. **Remote restore** — Individual files from Syncthing, not band-level restore

See `knowledge/sync-tools-comparison.md` for full analysis.
