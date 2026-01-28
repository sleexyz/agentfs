# Heuristics

Transferable patterns across codebases. Append-only with changelog.

**How this works:**
- Each heuristic has a slug (the `###` heading) for reference
- Predictions cite heuristics: `[HEURISTICS#auth-complexity]`
- When predictions are violated, heuristics get updated or qualified
- When predictions are confirmed, observation count increases

**Heuristics vs Facts:**
- Facts are codebase-specific: "Auth is in lib/auth/"
- Heuristics are transferable: "Token refresh is often harder than session handling"

---

## Entries

### immutable-blocks-enable-snapshots
- **Heuristic**: Systems with immutable blocks (COW, append-only) enable instant, cheap snapshots by sharing unchanged data
- **Confidence**: high
- **Observations**: 3 (JuiceFS, APFS, ZFS)
- **Added**: 2025-01-27
- **Context**: Storage systems, filesystems, databases

### content-addressing-enables-cow
- **Heuristic**: Content-addressed storage (hash → data) gives you COW semantics for free: unchanged content shares the same hash
- **Confidence**: high
- **Observations**: 2 (Git, JuiceFS)
- **Added**: 2025-01-27
- **Context**: Any system storing versioned data

### mtime-before-hash
- **Heuristic**: For dirty detection, check mtime first (fast), then hash only changed items (accurate). Two-phase avoids unnecessary work
- **Confidence**: high
- **Observations**: 2 (sparse bundle spike, rsync)
- **Added**: 2026-01-27
- **Context**: Sync tools, backup systems, caches

### checkpoint-scales-with-data-not-files
- **Heuristic**: Checkpoint time depends on dirty data size, not file count. Many file changes in related code often land in same storage blocks
- **Confidence**: medium
- **Observations**: 1
- **Added**: 2026-01-27
- **Context**: Block-based storage, sparse bundles, COW filesystems

### band-sync-conflicts-corrupt
- **Heuristic**: Syncing opaque container formats (sparse bundles, databases) at the block/band level causes corruption when sync tools rename conflicting files
- **Confidence**: high
- **Observations**: 1
- **Added**: 2026-01-27
- **Context**: Syncthing, Dropbox with SQLite/sparse bundles

### kext-friction-blocks-adoption
- **Heuristic**: Kernel extensions (kexts) create installation friction: security dialogs, reboots, MDM blocking on corporate machines. Prefer userspace solutions
- **Confidence**: high
- **Observations**: 2 (macFUSE adoption issues)
- **Added**: 2025-01-27
- **Context**: macOS tools, filesystem drivers

### reflink-before-hash
- **Heuristic**: When creating checkpoints, clone first (instant via reflink), hash later in background. Decouples user-facing speed from sync accuracy
- **Confidence**: medium
- **Observations**: 1
- **Added**: 2026-01-27
- **Context**: APFS, Btrfs, XFS reflink support

---

## Changelog

| Date | Slug | Action | Detail |
|------|------|--------|--------|
| 2026-01-28 | * | ADD | Migrated from LEARNINGS.md |
