# Facts

Codebase-specific truths. Append-only with changelog.

**How this works:**
- Each fact has a slug (the `###` heading) for reference
- Predictions cite facts: `[FACTS#auth-location]`
- When predictions are violated, facts get updated
- When predictions are confirmed, confidence increases

---

## Entries

### sparse-bundle-structure
- **Fact**: Sparse bundle layout is `myapp.sparsebundle/{Info.plist, Info.bckup, token, lock, bands/}` where bands are hex-named files (0, 1, ..., f, 10, ...)
- **Confidence**: high
- **Added**: 2025-01-27
- **Last verified**: 2026-01-27 (session: spike)

### sparse-bundle-band-size
- **Fact**: Band size configurable at creation via `sparse-band-size` imagekey (in 512-byte sectors). Recommended: 4MB for general use, 8MB for large-file workloads
- **Confidence**: high
- **Added**: 2025-01-27
- **Last verified**: 2026-01-27 (session: spike)

### sparse-bundle-metadata-bands
- **Fact**: Initial bands 7f, 80, ff contain APFS volume metadata and are pre-allocated at creation
- **Confidence**: high
- **Added**: 2026-01-27
- **Last verified**: 2026-01-27 (session: spike)

### band-mtime-precision
- **Fact**: Band file mtime has nanosecond precision (e.g., 1769566926.091853016), sufficient for sub-second dirty detection
- **Confidence**: high
- **Added**: 2026-01-27
- **Last verified**: 2026-01-27 (session: spike)

### band-change-count
- **Fact**: A single file modification touches 2-3 bands: band 0 (metadata), data band, and journal band
- **Confidence**: high
- **Added**: 2026-01-27
- **Last verified**: 2026-01-27 (session: spike)

### apfs-reflink-works
- **Fact**: `cp -c` (APFS reflink) works for band cloning. 680MB clones in 19ms with zero additional disk usage
- **Confidence**: high
- **Added**: 2026-01-27
- **Last verified**: 2026-01-27 (session: checkpoint-benchmark)

### checkpoint-benchmark-results
- **Fact**: On M4 Max with 10GB sparse bundle (87 bands, 680MB): list/stat 14ms, full hash 1,285ms, incremental 32-112ms, clone 19ms
- **Confidence**: high
- **Added**: 2026-01-27
- **Last verified**: 2026-01-27 (session: checkpoint-benchmark)

### architecture-decision-sync
- **Fact**: AgentFS uses sparse bundles for local checkpointing only (APFS reflinks). Syncthing syncs files inside mounted volume (not bands). Conflicts are per-file, not band-level
- **Confidence**: high
- **Added**: 2026-01-27
- **Last verified**: 2026-01-27 (session: sync-analysis)

### macos-no-kext-options
- **Fact**: Without kext: sparse bundles, hdiutil, FSEvents, APFS snapshots work. FUSE-T works with quirks. NFS loopback works with tool issues. macFUSE requires kext
- **Confidence**: high
- **Added**: 2025-01-27
- **Last verified**: 2025-01-27

### juicefs-data-hierarchy
- **Fact**: JuiceFS hierarchy: File → Chunk (64 MiB) → Slice (write unit) → Block (4 MiB, immutable) → Object (S3)
- **Confidence**: high
- **Added**: 2025-01-27
- **Last verified**: 2025-01-27 (session: juicefs-research)

---

## To Be Validated

- [ ] S3 upload throughput for 4MB bands
- [ ] Sparse bundle corruption recovery

---

## Changelog

| Date | Slug | Action | Detail |
|------|------|--------|--------|
| 2026-01-28 | * | ADD | Migrated from LEARNINGS.md |
