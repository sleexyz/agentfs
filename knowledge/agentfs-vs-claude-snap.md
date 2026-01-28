# agentfs vs claude-snap Comparison

**Date:** 2026-01-27
**Test project:** opensprite (13,567 files, ~938MB)

## Summary

agentfs's sparse bundle approach provides significant performance advantages over direct file cloning for large projects.

## Benchmark Results

| Metric | agentfs | claude-snap | Winner |
|--------|---------|-------------|--------|
| **Checkpoint** | **70ms** | 1,600ms | agentfs (23x faster) |
| **Restore** | **1,043ms** | 7,337ms | agentfs (7x faster) |
| **Space** | ~974MB/checkpoint | ~938MB/checkpoint | Similar (COW) |

## Why agentfs is Faster

The key insight: **APFS clone speed scales with item count, not data size.**

```
agentfs:      13,567 files → 124 bands → clone 124 items (~70ms)
claude-snap:  13,567 files → clone 13,567 items (~1,600ms)
```

Sparse bundles collapse many files into fewer bands (8MB chunks). Cloning 124 bands is much faster than cloning 13,567 files, even though both use the same APFS copy-on-write mechanism.

### Raw cp -cR Benchmarks

```bash
# Clone 13k files directly
/bin/cp -cR /tmp/project /tmp/clone
# Result: ~1,700ms

# Clone 124 bands
/bin/cp -cR bands/ checkpoint/
# Result: ~5ms
```

## Architecture Comparison

| Aspect | agentfs | claude-snap |
|--------|---------|-------------|
| **Mechanism** | Sparse bundle + band clones | Direct APFS clones (`cp -cR`) |
| **Project location** | Inside mount point | Existing directory |
| **node_modules** | Included (compressed to bands) | Excluded |
| **Metadata** | SQLite | JSON |
| **Claude Code hooks** | Not yet (Phase 2) | Built-in |
| **Language** | Go | TypeScript/Node |

## When to Use Each

### agentfs is better for:
- Large projects (>1k files)
- Projects where you need full state capture (including node_modules)
- Consistent checkpoint performance regardless of file count

### claude-snap is better for:
- Small projects (<1k files)
- Existing workflows (no mount requirement)
- Quick setup with Claude Code hooks already integrated

## Key Learnings

1. **Band compression matters**: 13,567 files → 124 bands = 110x reduction in clone operations

2. **Mount trade-off is worth it**: The requirement to work inside a mount buys us O(bands) instead of O(files) performance

3. **Restore is I/O bound**: agentfs restore (~1s) is dominated by hdiutil unmount/remount (~750ms), not the band swap itself

4. **Space efficiency is similar**: Both use APFS COW, so actual disk usage is approximately 1x project size regardless of checkpoint count

5. **Scaling prediction**:
   - 10k files: agentfs ~70ms, claude-snap ~1.2s
   - 50k files: agentfs ~70ms (more bands, still fast), claude-snap ~8s
   - 100k files: agentfs ~100ms, claude-snap ~16s

## Test Commands Used

```bash
# Checkpoint benchmark
time ./agentfs --store NAME checkpoint create "test"
time claude-snap snapshot

# Restore benchmark
time ./agentfs --store NAME restore v1 -f
time claude-snap restore SNAPSHOT_ID

# Check band count
ls ~/.agentfs/stores/NAME/NAME.sparsebundle/bands/ | wc -l
```

## Conclusion

The sparse bundle architecture hypothesis is validated: **band-level cloning provides a real, significant performance advantage for projects with many files.** The 23x checkpoint speedup and 7x restore speedup justify the mount requirement trade-off for the target use case (AI agent workflows on real codebases).
