# AgentFS — Brainstorm

> A filesystem layer that makes checkpointing instant, invisible, and analyzable.

---

## The Problems

### Problem 1: Version Control is Too Slow for Agents

Git was designed for humans who commit a few times per hour. Agents operate differently:

- An agent might make 50+ file changes in a minute
- Each "logical step" should be a checkpoint, but `git add && git commit` takes seconds
- Large repos with many files make git operations even slower
- Worktrees add cognitive overhead — "which directory am I in?"

**What we want:** Checkpointing at the speed of thought. An agent says "checkpoint" and it's done in milliseconds, not seconds.

**Current pain:**
```
# This is too slow when you're doing it 100x per session
git add -A && git commit -m "step 47: added error handling"  # 2-5 seconds
```

**What we want:**
```
agentfs checkpoint "added error handling"  # <50ms
```

### Problem 2: No Audit Trail for Causality

When something breaks, we need to answer:
- What changed?
- When did it change?
- Why did it change? (which agent action? which user request?)
- What was the state before?

Git gives us *what* but not *why*. And diffing between commits requires knowing which commits to compare.

**What we want:** A time-travel debugger for your filesystem.

```
# What files changed in the last 5 minutes?
agentfs changes --since 5m

# What was this file 10 checkpoints ago?
agentfs show src/app.ts@-10

# What agent action caused this change?
agentfs blame src/app.ts:42

# Restore to before the agent broke everything
agentfs restore @checkpoint-47
```

### Problem 3: Installation Friction Kills Adoption

macOS has aggressive security policies:
- Kernel extensions (kexts) require reboots, security setting changes, user trust dialogs
- macFUSE requires all of the above
- Users hate this. Developers hate this. IT departments hate this.

**Current pain:**
```
1. Install macFUSE
2. Go to System Preferences → Security & Privacy
3. Click "Allow" for the kernel extension
4. Reboot
5. Hope it works
6. Debug when it doesn't
```

**What we want:**
```
brew install agentfs
# Done. Works immediately.
```

---

## What We're Building

**AgentFS** is a filesystem layer that provides:

1. **Instant checkpoints** — Sub-100ms snapshots of your working directory
2. **Rich metadata** — Every checkpoint knows *why* it was created (agent action, user request, etc.)
3. **Time-travel** — Restore, diff, and explore any point in history
4. **Zero-config installation** — No kexts, no security dialogs, just `brew install`
5. **Git compatibility** — Works alongside git, not instead of it

### Non-Goals (for v1)

- Replacing git for collaboration/pushing/PRs
- Remote sync (S3, etc.) — that's a later concern
- Cross-platform — macOS first, others later
- Performance optimization for huge monorepos (>100GB)

---

## Design Principles

### 1. Checkpoints are Cheap

If checkpoints are expensive, people won't use them. Checkpoints must be:
- **Fast:** <100ms for typical project (10k files, 500MB)
- **Automatic:** Can be triggered programmatically without user thought
- **Stackable:** 1000 checkpoints shouldn't slow anything down

### 2. Metadata is First-Class

Every checkpoint carries structured metadata:
```json
{
  "id": "cp_abc123",
  "timestamp": "2024-01-15T10:30:00Z",
  "parent": "cp_xyz789",
  "message": "Added error handling to API routes",
  "context": {
    "agent": "claude-code",
    "action": "edit",
    "files_modified": ["src/api/routes.ts"],
    "user_request": "make the API more robust",
    "session_id": "sess_123"
  }
}
```

### 3. The Working Directory is Sacred

Users work in ONE directory. No worktrees, no switching, no confusion.
```
~/projects/myapp/          ← User always works here
~/projects/myapp/.agentfs/ ← AgentFS stores its data here (hidden)
```

### 4. No Kernel Extensions

We will not require:
- macFUSE
- Any kext
- Any System Preferences changes
- Any reboot

---

## Implementation Directions

### Direction A: FSEvents Watcher + Content-Addressed Store

**How it works:**
1. A daemon watches the directory using macOS FSEvents API
2. On each change, content is hashed and stored in `.agentfs/objects/`
3. Checkpoints are manifests pointing to object hashes
4. Restore = rewrite files from objects

**Architecture:**
```
┌─────────────────────────────────────────────────────────────┐
│  User's Working Directory                                   │
│  ~/projects/myapp/                                          │
│                                                             │
│  ┌─────────────┐    FSEvents API    ┌─────────────────┐    │
│  │   Files     │ ←─────────────────→ │  agentfs daemon │    │
│  │  (normal)   │                     │                 │    │
│  └─────────────┘                     └────────┬────────┘    │
│                                               │             │
│                                               ▼             │
│                                      ┌─────────────────┐    │
│                                      │  .agentfs/      │    │
│                                      │  ├── objects/   │    │
│                                      │  │   └── abc... │    │
│                                      │  ├── checkpoints/│   │
│                                      │  │   └── cp_1   │    │
│                                      │  └── index.db   │    │
│                                      └─────────────────┘    │
└─────────────────────────────────────────────────────────────┘
```

**Checkpoint operation:**
```
1. Pause watcher
2. Scan all files, compute hashes
3. Store any new content in objects/
4. Write manifest to checkpoints/
5. Resume watcher
```

**Pros:**
- No kernel extensions — FSEvents is a standard macOS API
- Simple mental model
- Works with any editor/tool
- Easy to install (`brew install`)

**Cons:**
- Not truly instant — requires scanning files
- Scanning 10k files takes time (though can be optimized)
- Race conditions between scan and new writes

**Optimization ideas:**
- Use FSEvents to track dirty files since last checkpoint
- Only hash dirty files, reuse hashes for unchanged
- Maintain in-memory file tree with cached hashes
- Could get checkpoint down to <100ms for typical workloads

---

### Direction B: Overlay Filesystem via NFS Loopback

**How it works:**
1. AgentFS runs an NFS server locally (no kext needed!)
2. User mounts their project via NFS loopback
3. All file operations go through AgentFS
4. AgentFS tracks every write, enabling instant snapshots

**Architecture:**
```
┌─────────────────────────────────────────────────────────────┐
│                                                             │
│   User sees:  ~/projects/myapp/  (NFS mount)               │
│                      │                                      │
│                      ▼                                      │
│              ┌───────────────┐                              │
│              │  AgentFS NFS  │                              │
│              │    Server     │                              │
│              │  (localhost)  │                              │
│              └───────┬───────┘                              │
│                      │                                      │
│         ┌────────────┼────────────┐                         │
│         ▼            ▼            ▼                         │
│   ┌──────────┐ ┌──────────┐ ┌──────────┐                   │
│   │ objects/ │ │ metadata │ │ current/ │                   │
│   │ (blobs)  │ │ (index)  │ │ (HEAD)   │                   │
│   └──────────┘ └──────────┘ └──────────┘                   │
│                                                             │
│   Stored in: ~/.agentfs/repos/myapp/                       │
└─────────────────────────────────────────────────────────────┘
```

**Checkpoint operation:**
```
1. All writes already captured by NFS layer
2. Checkpoint = snapshot current file tree metadata
3. O(1) operation — just record current state
```

**Pros:**
- True interception of all file operations
- Instant checkpoints (metadata only)
- No scanning needed
- NFS is built into macOS — no kext!

**Cons:**
- NFS mount might confuse some tools
- Slight latency on file operations
- More complex implementation
- Some editors handle NFS mounts poorly

**Key insight:** macOS supports NFS mounts natively. `mount_nfs` requires no special permissions if connecting to localhost.

---

### Direction C: FUSE-T Based (Kext-less FUSE)

**How it works:**
1. Use FUSE-T, a kext-less FUSE implementation for macOS
2. Implement a custom filesystem that wraps the real directory
3. Intercept all operations for journaling/snapshotting

**Architecture:**
```
┌─────────────────────────────────────────────────────────────┐
│                                                             │
│   User sees:  ~/projects/myapp/  (FUSE-T mount)            │
│                      │                                      │
│                      ▼                                      │
│              ┌───────────────┐                              │
│              │   FUSE-T      │                              │
│              │  AgentFS      │                              │
│              │  Filesystem   │                              │
│              └───────┬───────┘                              │
│                      │                                      │
│                      ▼                                      │
│              ┌───────────────┐                              │
│              │  Real Files   │                              │
│              │  + Journal    │                              │
│              │  + Snapshots  │                              │
│              └───────────────┘                              │
└─────────────────────────────────────────────────────────────┘
```

**Pros:**
- FUSE-T doesn't require kext (uses macOS's native NFS client internally)
- Full control over filesystem semantics
- Can implement COW snapshots properly
- Mature ecosystem (FUSE has many implementations to learn from)

**Cons:**
- FUSE-T is still relatively new, has quirks
- Requires `brew install fuse-t` (but no security dialogs)
- Some applications don't play well with FUSE mounts
- rclone has reported issues with FUSE-T

---

### Direction D: SQLite VFS + Symlink Farm

**How it works:**
1. Store all file content in SQLite database (content-addressed)
2. Working directory is a "symlink farm" pointing to extracted files
3. A daemon watches for changes and syncs back to SQLite
4. Checkpoints are just SQLite savepoints/snapshots

**Architecture:**
```
┌─────────────────────────────────────────────────────────────┐
│                                                             │
│   ~/projects/myapp/           ~/.agentfs/myapp.db          │
│   (working directory)         (SQLite database)            │
│                                                             │
│   src/                        ┌─────────────────────┐       │
│   ├── app.ts ──extract──────→ │ files table         │       │
│   ├── utils.ts ─────────────→ │ ├── path            │       │
│   └── ...                     │ ├── content_hash    │       │
│          │                    │ └── metadata        │       │
│          │                    ├─────────────────────┤       │
│   daemon watches              │ blobs table         │       │
│   for changes                 │ ├── hash            │       │
│          │                    │ └── content         │       │
│          ▼                    ├─────────────────────┤       │
│   sync back to DB             │ checkpoints table   │       │
│                               │ ├── id              │       │
│                               │ ├── file_snapshot   │       │
│                               │ └── metadata        │       │
│                               └─────────────────────┘       │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

**Checkpoint operation:**
```sql
-- Checkpoint is literally a SQL transaction
BEGIN;
INSERT INTO checkpoints (id, timestamp, metadata)
VALUES ('cp_123', NOW(), '{"message": "..."}');
INSERT INTO checkpoint_files (checkpoint_id, path, blob_hash)
SELECT 'cp_123', path, content_hash FROM files;
COMMIT;
```

**Pros:**
- SQLite is rock solid
- ACID guarantees for checkpoints
- Rich querying capabilities (find all files changed between checkpoints)
- Litestream compatible for remote backup
- No kernel extensions, no mounts

**Cons:**
- Two-way sync is tricky (working dir ↔ database)
- Symlinks might not work (would need real files)
- Large binary files don't belong in SQLite

---

### Direction E: Hybrid — FSEvents + Sparse Bundle

**How it works:**
1. Project lives inside a mounted sparse bundle (native macOS)
2. FSEvents daemon tracks changes
3. Checkpoints = unmount, snapshot bands, remount
4. Bands are content-addressed chunks

**Architecture:**
```
┌─────────────────────────────────────────────────────────────┐
│                                                             │
│   ~/projects/myapp/                                         │
│   (mount point for sparse bundle)                           │
│                     ↑                                       │
│                     │ hdiutil attach                        │
│                     │                                       │
│   ~/.agentfs/myapp.sparsebundle/                           │
│   ├── Info.plist                                            │
│   ├── bands/                                                │
│   │   ├── 0  ─────────────────┐                            │
│   │   ├── 1  ─────────────────┤                            │
│   │   └── 2  ─────────────────┤                            │
│   └── token                   │                            │
│                               ▼                             │
│                      ┌─────────────────┐                   │
│                      │ .agentfs/       │                   │
│                      │ ├── objects/    │  content-addressed│
│                      │ │   └── {hash}  │  band storage     │
│                      │ └── snapshots/  │                   │
│                      │     └── v1.json │  manifests        │
│                      └─────────────────┘                   │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

**Pros:**
- Sparse bundles are native macOS (hdiutil, no kext)
- Bands provide natural chunking for COW
- APFS snapshots available inside the bundle
- Familiar disk image model

**Cons:**
- Mounting/unmounting adds friction
- Sparse bundles can corrupt on unclean unmount
- Not as elegant as pure filesystem solution
- Disk images feel "weird" for development

---

## Comparison Matrix

| Direction | Install Ease | Checkpoint Speed | Implementation Complexity | Editor Compatibility |
|-----------|-------------|------------------|--------------------------|---------------------|
| **A: FSEvents + CAS** | ★★★★★ | ★★★☆☆ | ★★★★☆ | ★★★★★ |
| **B: NFS Loopback** | ★★★★☆ | ★★★★★ | ★★☆☆☆ | ★★★☆☆ |
| **C: FUSE-T** | ★★★★☆ | ★★★★★ | ★★☆☆☆ | ★★★☆☆ |
| **D: SQLite VFS** | ★★★★★ | ★★★★☆ | ★★★☆☆ | ★★★★★ |
| **E: Sparse Bundle** | ★★★★☆ | ★★★☆☆ | ★★★☆☆ | ★★★★★ |

---

## Recommended Path: Start with Direction A

**Why FSEvents + Content-Addressed Store?**

1. **Simplest to install** — Pure userspace, no mounts, no kernel anything
2. **Best editor compatibility** — Editors see normal files
3. **Incremental complexity** — Can add NFS/FUSE layer later if needed
4. **Fastest to prototype** — Can have working version in days

**Initial implementation plan:**

```
Week 1: Core primitives
├── Content-addressed blob store (.agentfs/objects/)
├── File tree snapshot format (JSON manifest)
├── Checkpoint create/restore commands

Week 2: FSEvents integration
├── Daemon that watches directory
├── Tracks dirty files since last checkpoint
├── Optimized scanning (only hash dirty files)

Week 3: CLI and UX
├── agentfs init
├── agentfs checkpoint "message" --context '{"agent": "..."}'
├── agentfs restore <checkpoint>
├── agentfs log
├── agentfs diff <cp1> <cp2>

Week 4: Agent integration
├── SDK/API for programmatic checkpoints
├── Claude Code hook integration
├── Metadata schema for agent context
```

---

## Open Questions

1. **How do we handle `.git` directory?**
   - Ignore it? Include it? Separate handling?

2. **What about node_modules and other large generated directories?**
   - Need `.agentfsignore` similar to `.gitignore`?

3. **How do we handle file permissions, symlinks, special files?**
   - Store full metadata? Or just content?

4. **What's the checkpoint retention policy?**
   - Keep forever? Auto-prune? User-configured?

5. **Should checkpoints be named or numbered?**
   - Git uses hashes, which are hard to remember
   - Sequential numbers? User-provided names? Both?

6. **How does this interact with git?**
   - AgentFS for fast iteration, git for collaboration?
   - Can we auto-commit to git periodically?

---

## JuiceFS Architecture Deep Dive

After studying the JuiceFS codebase, here are the key architectural insights that inform our design:

### How JuiceFS Achieves Instant Clones

```
File (160 MB)
├── Chunk 0 (64 MB): references Slice ID 100 → blocks in S3
├── Chunk 1 (64 MB): references Slice ID 101 → blocks in S3
└── Chunk 2 (32 MB): references Slice ID 102 → blocks in S3

juicefs clone file.txt clone.txt  # INSTANT (<10ms)

Clone (160 MB)
├── Chunk 0: references Slice ID 100 → SAME blocks (shared!)
├── Chunk 1: references Slice ID 101 → SAME blocks (shared!)
└── Chunk 2: references Slice ID 102 → SAME blocks (shared!)

After modifying Chunk 1 in clone:
Clone (160 MB)
├── Chunk 0: Slice ID 100 (still shared with original)
├── Chunk 1: Slice ID 999 (NEW blocks created) ← COW happened
└── Chunk 2: Slice ID 102 (still shared with original)
```

**The magic:** Clone is a metadata-only operation. It just copies the chunk→slice references. The actual blocks in object storage are shared until modified.

### JuiceFS Data Hierarchy

```
File
  └── Chunk (64 MiB logical unit, for fast lookup)
        └── Slice (write unit, variable size, gets unique ID)
              └── Block (4 MiB physical unit, IMMUTABLE)
                    └── Object in S3/GCS (actual bytes)
```

**Critical insight: Blocks are IMMUTABLE.**

When you modify a file:
1. New slice created with new slice ID
2. New blocks uploaded to object storage
3. Metadata updated to point to new blocks
4. Old blocks eventually garbage collected

This immutability is what enables instant snapshots.

### JuiceFS Write Path

```
┌─────────────────────────────────────────────────────────────────┐
│  WRITE: app writes to /mnt/jfs/file.txt                         │
│                                                                 │
│  1. Data buffered in client memory (default 300 MB buffer)     │
│                                                                 │
│  2. Auto-flush triggers:                                        │
│     • Block full (4 MB)                                        │
│     • Chunk full (64 MB)                                       │
│     • Time elapsed (5 seconds)                                 │
│     • Buffer pressure (>300 MB pending)                        │
│                                                                 │
│  3. Blocks uploaded to object storage                          │
│     Object name: {fsname}/chunks/{hash}/{sliceId}_{block}_{size}
│                                                                 │
│  4. AFTER upload completes: metadata updated                   │
│     Slice record: {id, size, offset, length}                   │
│                                                                 │
│  Consistency: Metadata updated AFTER data is durable           │
└─────────────────────────────────────────────────────────────────┘
```

### JuiceFS Read Path (Cache Hierarchy)

```
┌─────────────────────────────────────────────────────────────────┐
│  READ: app reads from /mnt/jfs/file.txt                         │
│                                                                 │
│  Cache lookup order:                                            │
│                                                                 │
│  1. Kernel page cache (µs latency)                             │
│     └── Automatic via FUSE, no JuiceFS code needed             │
│                                                                 │
│  2. Client memory buffer (µs latency)                          │
│     └── Recently written data not yet flushed                  │
│                                                                 │
│  3. Local disk cache (ms latency)                              │
│     └── --cache-dir, --cache-size flags                        │
│     └── LRU eviction when full                                 │
│                                                                 │
│  4. Object storage (100ms+ latency)                            │
│     └── Download block, cache locally for next time            │
│                                                                 │
│  Readahead: Sequential reads trigger prefetch of next blocks   │
└─────────────────────────────────────────────────────────────────┘
```

### JuiceFS Reference Counting

```
SliceRef tracks how many files reference each slice:

• File A created: slice 100 (refs=1, not stored)
• Clone B created: slice 100 (refs=2, stored as refs-1=1)
• File A deleted: slice 100 (refs=1, entry deleted)
• Clone B deleted: slice 100 (refs=0, block eligible for GC)

Garbage collection runs periodically to clean up unreferenced blocks.
```

### Key Takeaways for AgentFS

1. **Immutable blocks enable instant snapshots** — Never modify in place, always create new
2. **Metadata is source of truth** — All access goes through metadata layer
3. **Local-first with async sync** — Buffer locally, flush to remote asynchronously
4. **Reference counting for cleanup** — Track which snapshots reference which blocks
5. **Content-addressing for deduplication** — Same content = same block, stored once

---

## Revised Direction E: Sparse Bundle + JuiceFS-Inspired Architecture

Based on JuiceFS learnings, here's a refined architecture that combines:
- Native macOS sparse bundles (no kext)
- JuiceFS-style immutable blocks and metadata-only snapshots
- Object store backend for durability

### Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│  USER'S VIEW                                                    │
│                                                                 │
│  ~/projects/myapp/          ← Normal directory, user works here │
│  (mounted APFS sparse bundle)                                   │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
         ↑ hdiutil attach (automatic on agentfs open)
         │
┌─────────────────────────────────────────────────────────────────┐
│  LOCAL STORAGE                                                  │
│                                                                 │
│  ~/.agentfs/stores/myapp/                                       │
│  ├── myapp.sparsebundle/        ← The actual disk image        │
│  │   ├── Info.plist                                            │
│  │   ├── bands/                  ← 4 MB chunks (like JuiceFS)  │
│  │   │   ├── 0                                                 │
│  │   │   ├── 1                                                 │
│  │   │   ├── 2                                                 │
│  │   │   └── ...                                               │
│  │   └── token                                                 │
│  │                                                              │
│  ├── metadata.db                 ← SQLite: band hashes, refs   │
│  │   ├── bands (band_idx, hash, refs, last_sync)              │
│  │   ├── snapshots (id, timestamp, metadata, manifest)        │
│  │   └── sync_queue (band_idx, status)                        │
│  │                                                              │
│  └── cache/                      ← Downloaded bands from remote │
│      └── {hash}                                                │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
         ↓ async sync (background daemon)
         │
┌─────────────────────────────────────────────────────────────────┐
│  OBJECT STORE (S3/GCS/R2)                                       │
│                                                                 │
│  s3://agentfs-{user}/myapp/                                    │
│  ├── blobs/                      ← Content-addressed bands     │
│  │   ├── sha256:abc123...        (band content, IMMUTABLE)     │
│  │   ├── sha256:def456...                                      │
│  │   └── sha256:xyz789...                                      │
│  │                                                              │
│  └── snapshots/                  ← Snapshot manifests          │
│      ├── cp_001.json                                           │
│      ├── cp_002.json                                           │
│      └── latest → cp_002.json                                  │
│                                                                 │
│  Snapshot manifest format:                                      │
│  {                                                              │
│    "id": "cp_002",                                             │
│    "parent": "cp_001",                                         │
│    "timestamp": "2024-01-15T10:30:00Z",                        │
│    "message": "Added error handling",                          │
│    "context": { "agent": "claude-code", ... },                 │
│    "bands": {                                                   │
│      "0": "sha256:abc123",      ← Same as cp_001 (shared!)     │
│      "1": "sha256:xyz789",      ← Changed since cp_001         │
│      "2": "sha256:def456"       ← Same as cp_001 (shared!)     │
│    },                                                           │
│    "band_size": 4194304,                                       │
│    "total_bands": 3                                            │
│  }                                                              │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

### Checkpoint Flow (Local - Instant)

```
agentfs checkpoint "added error handling"

┌─────────────────────────────────────────────────────────────────┐
│  Step 1: Flush writes (10-50ms)                                 │
│  └── sync /Volumes/myapp   OR   brief fsync                    │
│                                                                 │
│  Step 2: Identify changed bands (1-10ms)                        │
│  └── Check mtime on bands/ directory                           │
│  └── Compare with last checkpoint's band hashes                │
│                                                                 │
│  Step 3: Hash changed bands only (10-100ms for typical changes)│
│  └── SHA256 only the bands that changed                        │
│  └── Reuse cached hashes for unchanged bands                   │
│                                                                 │
│  Step 4: Write snapshot manifest (1ms)                          │
│  └── INSERT INTO snapshots (id, manifest_json, metadata)       │
│                                                                 │
│  Step 5: Queue changed bands for sync (async, non-blocking)    │
│  └── INSERT INTO sync_queue (band_idx, hash, status='pending') │
│                                                                 │
│  Total: 20-150ms for typical workload                          │
│  (Most time is in hashing changed bands)                       │
└─────────────────────────────────────────────────────────────────┘
```

### Sync Flow (Background - Async)

```
agentfs daemon (runs in background)

┌─────────────────────────────────────────────────────────────────┐
│  Loop every 5 seconds (or on-demand trigger):                   │
│                                                                 │
│  1. Check sync_queue for pending bands                         │
│                                                                 │
│  2. For each pending band:                                     │
│     a. Check if hash already exists in S3 (HEAD request)       │
│     b. If not exists: upload band content                      │
│     c. Mark as synced in metadata.db                           │
│                                                                 │
│  3. Upload any new snapshot manifests                          │
│                                                                 │
│  Deduplication happens automatically:                           │
│  - Same content = same hash = skip upload                      │
│  - Bands shared across snapshots are uploaded once             │
└─────────────────────────────────────────────────────────────────┘
```

### Restore Flow

```
agentfs restore cp_001

┌─────────────────────────────────────────────────────────────────┐
│  Option A: Local restore (if bands cached)                      │
│                                                                 │
│  1. Read snapshot manifest from metadata.db                    │
│  2. For each band in manifest:                                 │
│     - If band exists locally with correct hash: skip           │
│     - If band differs: restore from cache or download          │
│  3. Remount sparse bundle                                      │
│                                                                 │
│  Option B: Full restore from remote                            │
│                                                                 │
│  1. Download snapshot manifest from S3                         │
│  2. Download all bands referenced by manifest                  │
│  3. Reconstruct sparse bundle                                  │
│  4. Mount                                                      │
│                                                                 │
│  Option C: Clone restore (COW)                                 │
│                                                                 │
│  1. Create new sparse bundle                                   │
│  2. Symlink/reflink bands from cache (COW on APFS!)           │
│  3. Mount as separate project                                  │
│  4. agentfs open myapp@cp_001                                  │
└─────────────────────────────────────────────────────────────────┘
```

### Band Size Tradeoffs

| Band Size | Pros | Cons |
|-----------|------|------|
| 1 MB | Fine-grained dedup, small uploads | Many objects, more S3 requests |
| 4 MB | Good balance (JuiceFS default) | Reasonable for most workloads |
| 8 MB | Fewer objects (sparse bundle default) | Larger uploads on small changes |
| 16 MB | Minimal S3 overhead | Poor dedup for small changes |

**Recommendation:** 4 MB bands (matching JuiceFS) for good balance.

Configure at creation:
```bash
agentfs init myapp --band-size 4M
```

### Reference Counting for Cleanup

```sql
-- metadata.db schema

CREATE TABLE bands (
    band_idx INTEGER PRIMARY KEY,
    hash TEXT NOT NULL,
    refs INTEGER DEFAULT 1,      -- How many snapshots reference this
    last_modified INTEGER,
    synced_at INTEGER            -- NULL if not yet synced to remote
);

CREATE TABLE snapshots (
    id TEXT PRIMARY KEY,
    parent_id TEXT,
    timestamp INTEGER NOT NULL,
    message TEXT,
    metadata JSON,
    manifest JSON NOT NULL,      -- { "bands": { "0": "hash", ... } }
    synced_at INTEGER
);

CREATE TABLE sync_queue (
    band_hash TEXT PRIMARY KEY,
    status TEXT DEFAULT 'pending',  -- pending, uploading, synced, failed
    attempts INTEGER DEFAULT 0,
    last_attempt INTEGER
);
```

When deleting old snapshots:
```sql
-- Decrement refs for bands only referenced by deleted snapshot
UPDATE bands SET refs = refs - 1
WHERE band_idx IN (
    SELECT band_idx FROM snapshot_bands WHERE snapshot_id = 'cp_old'
);

-- Bands with refs=0 can be deleted from local and remote
DELETE FROM bands WHERE refs = 0;
```

### Advantages of This Architecture

1. **Instant local checkpoints** — Just hash changed bands + write manifest
2. **True COW semantics** — Unchanged bands shared across snapshots
3. **No kext required** — Sparse bundles are native macOS
4. **Async durability** — Don't block on S3 uploads
5. **Efficient sync** — Content-addressed means automatic dedup
6. **Works offline** — Local-first, sync when connected
7. **Multi-machine** — Restore from S3 on any machine

### Challenges and Mitigations

| Challenge | Mitigation |
|-----------|------------|
| Sparse bundle corruption on crash | Frequent checkpoints + remote backup = recovery point |
| Band hashing overhead | Only hash changed bands (track via mtime) |
| S3 costs | Lifecycle rules for old blobs, use R2/Backblaze for cheap storage |
| Large repos | Increase band size, use .agentfsignore for node_modules |
| Mount point confusion | agentfs handles mount/unmount automatically |

### CLI Design

```bash
# Initialize a new agentfs project
agentfs init myapp --size 50G --band-size 4M

# Open project (mounts sparse bundle)
agentfs open myapp
# → Mounted at ~/projects/myapp

# Create checkpoint (instant, local)
agentfs checkpoint "added error handling" \
  --context '{"agent": "claude-code", "session": "abc123"}'

# List checkpoints
agentfs log myapp
# cp_003  2m ago    "added error handling"
# cp_002  5m ago    "refactored utils"
# cp_001  10m ago   "initial setup"

# Diff between checkpoints
agentfs diff cp_001 cp_003

# Restore to checkpoint
agentfs restore cp_002

# View file at specific checkpoint
agentfs show src/app.ts@cp_001

# Sync status
agentfs sync status
# Local: 47 bands, 3 pending upload
# Remote: 44 bands synced

# Force sync now
agentfs sync push

# Close project (unmounts)
agentfs close myapp
```

---

## Updated Comparison Matrix

| Direction | Install | Checkpoint Speed | Remote Sync | COW | Complexity |
|-----------|---------|------------------|-------------|-----|------------|
| **A: FSEvents + CAS** | ★★★★★ | ★★★☆☆ (scan) | ★★★☆☆ | ❌ | ★★★★☆ |
| **B: NFS Loopback** | ★★★★☆ | ★★★★★ | ★★☆☆☆ | ✅ | ★★☆☆☆ |
| **C: FUSE-T** | ★★★★☆ | ★★★★★ | ★★☆☆☆ | ✅ | ★★☆☆☆ |
| **D: SQLite VFS** | ★★★★★ | ★★★★☆ | ★★★★☆ | ❌ | ★★★☆☆ |
| **E: Sparse Bundle** | ★★★★☆ | ★★★★☆ | ★★★★★ | ✅ | ★★★☆☆ |

**Direction E now recommended** because:
1. Native macOS (no kext, no FUSE)
2. JuiceFS-inspired architecture (proven at scale)
3. True COW via content-addressed bands
4. Built-in chunking via sparse bundle bands
5. Natural fit for object store sync

---

## Implementation Roadmap

### Phase 1: Local MVP (Week 1-2)

```
Goal: Local checkpoints work, no remote sync

├── Core
│   ├── Sparse bundle creation/mounting (hdiutil wrapper)
│   ├── Band hashing (SHA256)
│   ├── SQLite metadata store
│   └── Snapshot manifest format
│
├── CLI
│   ├── agentfs init
│   ├── agentfs open / close
│   ├── agentfs checkpoint
│   ├── agentfs log
│   └── agentfs restore
│
└── Testing
    └── Benchmark: checkpoint speed with 10k files
```

### Phase 2: Remote Sync (Week 3-4)

```
Goal: Sync to S3/GCS/R2

├── Sync daemon
│   ├── Background band upload
│   ├── Dedup via content-addressing
│   └── Manifest upload
│
├── CLI
│   ├── agentfs sync push
│   ├── agentfs sync pull
│   └── agentfs sync status
│
└── Remote restore
    └── agentfs clone myapp --from s3://bucket/myapp
```

### Phase 3: Agent Integration (Week 5-6)

```
Goal: Seamless Claude Code integration

├── Hooks
│   ├── Pre-tool checkpoint
│   ├── Post-tool checkpoint
│   └── Session metadata attachment
│
├── SDK
│   ├── agentfs.checkpoint(message, context)
│   ├── agentfs.restore(checkpoint_id)
│   └── agentfs.diff(cp1, cp2)
│
└── Analysis
    ├── agentfs blame <file>
    ├── agentfs changes --since 5m
    └── agentfs timeline (visual)
```

---

## Sync Tools Deep Dive (2026-01-27)

Researched major sync tools to understand how to achieve seamless local↔cloud sync.

### Ecosystem Comparison

| Tool | GitHub Stars | Architecture | Best For |
|------|-------------|--------------|----------|
| **Syncthing** | 79,412 ⭐ | P2P mesh, BEP protocol | Long-term multi-device sync |
| **Unison** | 5,068 | Archive-based bidirectional | Cross-platform, careful sync |
| **Mutagen** | 3,833 | Client-agent over SSH/Docker | Dev workflows, containers |

### Syncthing (Winner for Ecosystem)

**Architecture:**
- Block Exchange Protocol (BEP) over TLS
- Dynamic block sizes: 128KB-16MB based on file size
- Vector clocks for conflict detection
- NAT traversal via community relay servers

**Conflict Resolution:**
- Creates `.sync-conflict-<date>-<device>` files
- Latest modification time wins
- Both versions preserved

**Key Insight:** Can run headless, control via REST API:
```bash
syncthing --no-browser --gui-address=127.0.0.1:8384
curl -H "X-API-Key: $KEY" http://localhost:8384/rest/config/folders
```

### Mutagen (Best for Dev Workflows)

**Architecture:**
- Three-way merge reconciliation (ancestor + alpha + beta)
- rsync-style delta transfer
- XXH128 hashing (10x faster than SHA-1)
- Transports: SSH, Docker, local

**Conflict Modes:**
- `two-way-safe`: Both sides equal, manual conflict resolution
- `two-way-resolved`: Alpha wins conflicts
- `one-way-safe`: Alpha→Beta, Beta changes block
- `one-way-replica`: Complete mirroring

**Owned by Docker** — good for container workflows.

### Unison (Most Mature)

**Architecture:**
- Archive-based state tracking (20+ years of development)
- MD5 fingerprinting with rsync-style compression
- External merge tool support

**Key Feature:** True bidirectional with archive — knows what changed on each side since last sync.

### Critical Finding: None Support S3 Directly

All three require an **agent running on the remote end**. For direct cloud storage:
- **rclone** — 70+ cloud providers, but not real-time
- **Custom build** — FSEvents → S3 PUT (mini-Dropbox)

### Architecture Decision: File-Level Sync

**The Problem:** Band-level sync + Syncthing = corruption

If two machines modify the same sparse bundle:
1. Syncthing creates `.sync-conflict-<date>-<device>` file
2. Sparse bundle expects `bands/0`, gets `bands/0.sync-conflict-...`
3. **Filesystem is corrupted**

**The Solution:** Sync files, not bands

```
Sparse bundles    → Local checkpointing only (APFS reflinks)
Syncthing         → Syncs files INSIDE the mounted volume
Conflicts         → Per-file, manageable (.sync-conflict files)
```

**Trade-offs:**
- ✅ No band corruption risk
- ✅ No FUSE required
- ✅ Simple architecture
- ⚠️ Can't restore exact band state from remote
- ⚠️ Remote sync is file-level, not block-level

This is the right trade-off for v1.

See `knowledge/sync-tools-comparison.md` for full analysis.

---

## Provenance Tracking Research (2026-01-27)

Researched precedent for tracking "who did what" at the filesystem level.

### Existing Systems

| System | Tracks What | Tracks Who (PID) | Tracks Causality | Platform |
|--------|-------------|------------------|------------------|----------|
| **Linux auditd** | ✅ | ✅ | ❌ | Linux |
| **CamFlow** | ✅ | ✅ | ✅ (provenance graphs) | Linux kernel |
| **Harvard PASS** | ✅ | ✅ | ✅ (file lineage) | Linux (research) |
| **macOS Endpoint Security** | ✅ | ✅ | ❌ | macOS |
| **OpenBSM** | ✅ | ✅ | ❌ | macOS (deprecated) |
| **NILFS2** | ✅ | ❌ | ❌ | Linux |
| **FSEvents** | ✅ | ❌ | ❌ | macOS |

### Linux: auditd

Built-in audit subsystem tracks process info for every file operation:

```bash
# Watch a file for writes
auditctl -w /path/to/file -p wa -k my-watch

# Search audit log — shows pid, exe, comm, uid
ausearch -f /path/to/file -i
```

**Captures:** pid, ppid, exe path (`/usr/bin/vim`), command name, uid/gid, timestamp.

### Linux: CamFlow (Whole-System Provenance)

Kernel module that builds **provenance graphs**:

```
Process A (pid 1234)
    ↓ write
File /tmp/foo.txt
    ↓ read
Process B (pid 5678)
    ↓ write
File /tmp/bar.txt
```

Can answer: "What files did process X affect?" and "What processes touched file Y?"

**Source:** https://camflow.org/

### Harvard PASS (Provenance-Aware Storage)

Research system that built provenance into the filesystem:

> "The operating system should be responsible for the collection of provenance and the storage system should be responsible for its management."

Records the "ancestry" of every file — where it came from, what transformed it.

**Applications:** Scientific reproducibility, intrusion detection, debugging.

**Source:** https://syrah.eecs.harvard.edu/pass

### macOS: Endpoint Security Framework

Modern Apple API for file operation tracking:

```c
// Subscribe to file write events
es_subscribe(client, ES_EVENT_TYPE_NOTIFY_WRITE, ...);

// Each event includes process info (pid, path, code signature)
```

**Caveat:** Requires Apple entitlement (`com.apple.developer.endpoint-security.client`).

### macOS: FSEvents (Limited)

FSEvents tracks **what changed** but **NOT who changed it**:

```
✅ File /path/to/foo.txt was modified
❌ Process 1234 (/usr/bin/vim) modified it  <- NOT available
```

### NILFS2: Automatic Continuous Snapshots

Linux filesystem with automatic checkpointing:

```bash
# List all checkpoints (created every few seconds)
lscp

# Mount a checkpoint from the past
mount -t nilfs2 -o cp=1234 /dev/sda1 /mnt/old
```

**Key Feature:** Copy-on-write, never overwrites data. Can recover any file from any point in time.

**Limitation:** Doesn't track *who* made changes, just *what* changed.

### Implications for AgentFS

**Option A: Use Endpoint Security Framework**
- Get process info for every write
- Correlate with Claude Code session
- **Requires Apple entitlement** (harder distribution)

**Option B: Correlate by Timing**
- FSEvents tells us *when* files changed
- Claude Code logs tell us what tool ran *when*
- Match them up: "file X changed at T, tool Y ran at T"
- **No special entitlements needed**

**Option C: Use Hooks (Recommended for v1)**
- Claude Code hooks fire before/after tool calls
- Checkpoint on each hook → know exactly what each tool changed
- **Simplest, most portable**

Each checkpoint captures rich context:
```json
{
  "checkpoint_id": "cp_047",
  "timestamp": "2026-01-27T10:32:15Z",
  "trigger": "post_tool_hook",
  "tool": "Edit",
  "tool_input": {"file": "src/auth.ts", "old_string": "...", "new_string": "..."},
  "session_id": "abc123",
  "user_prompt_hash": "sha256:...",
  "files_changed": ["src/auth.ts"]
}
```

This gives causality without kernel-level access or Apple entitlements.

---

## Architecture Summary (2026-01-27)

After research and spikes, here's the final architecture:

```
┌─────────────────────────────────────────────────────────────────┐
│  LOCAL (macOS)                                                  │
│                                                                 │
│  ~/projects/myapp/           ← User works here (mounted)       │
│         ↑                                                       │
│    sparse bundle             ← 36k files → ~100 bands          │
│         ↑                                                       │
│    APFS reflinks             ← Instant snapshots (~20ms)       │
│         ↑                                                       │
│    AgentFS daemon            ← Checkpointing + causality       │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
                              ↕
                    Syncthing (file-level sync)
                              ↕
┌─────────────────────────────────────────────────────────────────┐
│  REMOTE (Cloud VM or another Mac)                               │
│                                                                 │
│  Syncthing                   ← Receives synced files           │
│  ~/projects/myapp/           ← Mirror of local project         │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

**Key Decisions:**
1. **Sparse bundles for checkpointing** — Collapses 36k files into ~100 bands
2. **APFS reflinks for snapshots** — ~20ms instant COW
3. **File-level sync via Syncthing** — Avoids band corruption on conflicts
4. **Hook-based causality** — No special entitlements needed

**What We're NOT Building:**
- Band-level remote sync (corruption risk)
- Kernel extensions (installation friction)
- Custom sync protocol (reinventing 79K-star wheel)

---

## Next Steps

1. ~~Prototype sparse bundle + band hashing~~ ✅ Done (Spike 1)
2. ~~Test APFS reflinks~~ ✅ Done (Spike 2) — ~20ms for 680MB
3. ~~Research sync tools~~ ✅ Done — Syncthing recommended
4. ~~Research provenance~~ ✅ Done — Hook-based approach
5. **Build Phase 1: Core MVP** ← Current

---

---

## COW Maximalism: The Full Picture (2026-01-27)

### The Vignette

```
You open Claude Code in ~/projects/myapp (mounted via agentfs).

Claude makes 47 changes across a 2-hour session:
- Refactors auth module
- Adds error handling
- Writes tests
- Fixes bugs from the tests
- Refactors again

Each change triggers a checkpoint. Each checkpoint knows:
- What tool ran (Edit, Write, Bash)
- What files changed
- What prompt led to this
- What session it belongs to

At the end, you can:
1. See a timeline of the entire session
2. Scrub to any point in time
3. See the exact filesystem state at that moment
4. Trace why any line of code exists
5. Branch from any checkpoint to explore alternatives
```

### What This Would Look Like

```
┌─────────────────────────────────────────────────────────────────────────────┐
│  SESSION TIMELINE: myapp / 2026-01-27 afternoon                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  14:00 ─────●───────●──●──────●─────●●●──●───────●─────●──────● 16:00       │
│             │       │  │      │     │││  │       │     │      │             │
│             v1      v5 v6     v12   ││v20│       v35   v40    v47           │
│             init    ╰──┬──╯   auth  │││  tests   ╰─────┬──────╯             │
│                        │            ││╰─ error        refactor              │
│                     refactor        │╰── handling                           │
│                                     ╰─── fix                                │
│                                                                             │
│  [v35] 15:42 - Edit src/auth.ts                                            │
│  ├── Changed: +47 -12 lines                                                │
│  ├── Prompt: "make the auth more robust"                                   │
│  └── Tool: Edit { file: "src/auth.ts", ... }                               │
│                                                                             │
│  [Scrub to v35]  [Restore v35]  [Branch from v35]  [Diff v34..v35]         │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### What's Missing From Current Implementation

| Feature | Current State | Needed For Vision |
|---------|--------------|-------------------|
| **Checkpoints** | ✅ v1, v2, v3... | ✅ Works |
| **Checkpoint timing** | ✅ Timestamps | ✅ Works |
| **Checkpoint messages** | ✅ User-provided | Need: auto-generated from tool |
| **Session grouping** | ❌ None | Group checkpoints by Claude session |
| **Tool tracking** | ❌ None | Which tool (Edit/Write/Bash) |
| **File tracking** | ❌ None | Which files changed |
| **Prompt tracking** | ❌ None | What user asked for |
| **Claude Code hooks** | ❌ None | Auto-checkpoint on tool use |
| **File-level diff** | ❌ Band-level only | See actual file changes |
| **Content-addressing** | ❌ Implicit (APFS COW) | Explicit hashes for dedup |
| **Per-file history** | ❌ None | "What happened to auth.ts?" |
| **Branching** | ❌ None | Explore alternatives from any point |
| **Timeline viz** | ❌ None | Visual scrubber |

### Content-Addressing Maximalism

Current approach relies on APFS COW for dedup. But this is implicit — we can't query "which checkpoints share this content."

**Full content-addressing would mean:**

```
Checkpoint v35:
├── manifest.json
│   {
│     "files": {
│       "src/auth.ts": "sha256:abc123...",    ← Content hash
│       "src/utils.ts": "sha256:def456...",
│       "package.json": "sha256:xyz789..."
│     }
│   }
└── stored in bands (APFS handles physical dedup)

Checkpoint v36:
├── manifest.json
│   {
│     "files": {
│       "src/auth.ts": "sha256:NEW111...",    ← Changed
│       "src/utils.ts": "sha256:def456...",   ← Same hash = same content
│       "package.json": "sha256:xyz789..."    ← Same hash = same content
│     }
│   }
```

**Benefits:**
1. Know exactly which files changed between any two checkpoints
2. Dedup at file level, not just block level
3. Can reconstruct any file at any checkpoint
4. Can answer "when did this exact content exist?"
5. Enables branching (just create new manifest pointing to existing content)

**Challenge:** This requires tracking file hashes, which means scanning files. But we can optimize:
- Only hash files that FSEvents says changed
- Cache hashes in SQLite
- Still use APFS COW for actual storage

### Causality Schema

```sql
-- Extended schema for full causality tracking

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    claude_session_id TEXT,          -- From Claude Code
    started_at INTEGER NOT NULL,
    ended_at INTEGER,
    project_id TEXT NOT NULL
);

CREATE TABLE checkpoints (
    id INTEGER PRIMARY KEY,
    store_id TEXT NOT NULL,
    version INTEGER NOT NULL,
    session_id TEXT REFERENCES sessions(id),

    -- Causality
    trigger TEXT,                     -- 'manual', 'pre_tool', 'post_tool', 'auto'
    tool_name TEXT,                   -- 'Edit', 'Write', 'Bash', etc.
    tool_input JSON,                  -- The actual tool call
    prompt_hash TEXT,                 -- Hash of user prompt that led here

    -- What changed
    files_changed JSON,               -- ["src/auth.ts", "src/utils.ts"]

    -- Metadata
    message TEXT,
    created_at INTEGER NOT NULL,

    UNIQUE(store_id, version)
);

CREATE TABLE file_versions (
    id INTEGER PRIMARY KEY,
    checkpoint_id INTEGER REFERENCES checkpoints(id),
    path TEXT NOT NULL,
    content_hash TEXT NOT NULL,       -- SHA256 of file content
    size INTEGER,
    mode INTEGER,

    UNIQUE(checkpoint_id, path)
);

CREATE INDEX idx_file_versions_hash ON file_versions(content_hash);
CREATE INDEX idx_file_versions_path ON file_versions(path);
```

### Claude Code Integration

```bash
# .claude/settings.json (installed by agentfs)
{
  "hooks": {
    "PreToolUse": [{
      "matcher": "Edit|Write|MultiEdit|NotebookEdit",
      "hooks": [{
        "type": "command",
        "command": "agentfs hook pre-tool"
      }]
    }],
    "PostToolUse": [{
      "matcher": "Edit|Write|MultiEdit|NotebookEdit",
      "hooks": [{
        "type": "command",
        "command": "agentfs hook post-tool"
      }]
    }],
    "Stop": [{
      "hooks": [{
        "type": "command",
        "command": "agentfs hook session-end"
      }]
    }]
  }
}
```

Hook receives JSON via stdin:
```json
{
  "session_id": "abc123",
  "tool_name": "Edit",
  "tool_input": {
    "file_path": "/path/to/file.ts",
    "old_string": "...",
    "new_string": "..."
  }
}
```

### New CLI Commands

```bash
# Session-aware commands
agentfs sessions                      # List Claude sessions
agentfs session abc123                # Show session details
agentfs session abc123 --timeline     # Visual timeline

# Per-file history
agentfs history src/auth.ts           # All changes to this file
agentfs history src/auth.ts --blame   # Who/what changed each section

# Branching
agentfs branch v35 --name experiment  # Create branch from checkpoint
agentfs branches                      # List branches
agentfs switch experiment             # Switch to branch

# Visualization (future: opens web UI)
agentfs viz                           # Timeline visualization
agentfs viz --session abc123          # Focus on one session
```

### Implementation Path

**Phase 2a: Causality Foundation**
```
├── Add session_id, tool_name, tool_input to checkpoints table
├── Add file_versions table with content hashes
├── CLI: agentfs checkpoint --tool Edit --session abc123
└── CLI: agentfs history <file>
```

**Phase 2b: Claude Code Hooks**
```
├── agentfs hook pre-tool (create checkpoint before)
├── agentfs hook post-tool (record what changed)
├── agentfs hook session-end (close session)
└── Installer: agentfs install-hooks
```

**Phase 2c: Timeline & Viz**
```
├── agentfs sessions / agentfs session <id>
├── agentfs timeline (CLI ascii art)
└── agentfs viz (web UI, future)
```

**Phase 2d: Branching**
```
├── agentfs branch <checkpoint> --name <name>
├── agentfs branches
├── agentfs switch <branch>
└── Merge? (complex, maybe defer)
```

### The Dream State

```bash
$ agentfs viz

╔══════════════════════════════════════════════════════════════════╗
║  myapp - Session Timeline                                        ║
╠══════════════════════════════════════════════════════════════════╣
║                                                                  ║
║  Jan 27, 14:00                                           16:00  ║
║  ════════════════════════════════════════════════════════════   ║
║                                                                  ║
║  Session: claude-abc123 (2h 4m)                                 ║
║  ●──●──●────●──●●●──●──────●────●──●                           ║
║  │  │  │    │  │││  │      │    │  └─ v47: "final cleanup"     ║
║  │  │  │    │  │││  │      │    └─── v40: refactored tests     ║
║  │  │  │    │  │││  │      └──────── v35: robust auth          ║
║  │  │  │    │  │││  └─────────────── v20: added tests          ║
║  │  │  │    │  ││└────────────────── v19: fixed edge case      ║
║  │  │  │    │  │└─────────────────── v18: error handling       ║
║  │  │  │    │  └──────────────────── v17: more error handling  ║
║  │  │  │    └─────────────────────── v12: auth refactor        ║
║  │  │  └──────────────────────────── v6: initial structure     ║
║  │  └─────────────────────────────── v5: package.json          ║
║  └────────────────────────────────── v1: init                  ║
║                                                                  ║
║  Files most changed: src/auth.ts (12x), src/test.ts (8x)        ║
║                                                                  ║
║  [←] [→] Scrub    [R] Restore    [D] Diff    [B] Branch         ║
║                                                                  ║
╚══════════════════════════════════════════════════════════════════╝

$ agentfs history src/auth.ts

src/auth.ts history (12 versions)

VERSION  SESSION        TOOL   CHANGE        MESSAGE
v47      claude-abc123  Edit   +2 -2         cleanup imports
v40      claude-abc123  Edit   +15 -8        add retry logic
v35      claude-abc123  Edit   +47 -12       robust error handling
v20      claude-abc123  Edit   +5 -0         export for tests
v19      claude-abc123  Edit   +3 -1         fix edge case
v18      claude-abc123  Edit   +23 -5        add error handling
v12      claude-abc123  Edit   +89 -45       major refactor
v6       claude-abc123  Write  +120 -0       initial implementation

$ agentfs blame src/auth.ts:42

Line 42: `if (token.expired()) throw new AuthError('Token expired');`

Introduced in v18 by Edit tool
Session: claude-abc123
Prompt: "add proper error handling to the auth module"
Changed: +23 -5 lines in this file
```

### Summary: What's Missing

1. **Session tracking** — group checkpoints by Claude session
2. **Tool tracking** — which tool made the change
3. **File-level content hashes** — know exactly what changed
4. **Claude Code hooks** — automatic checkpointing
5. **History commands** — per-file, per-session views
6. **Timeline visualization** — scrub through time
7. **Branching** — explore alternatives

This is Phase 2. Phase 1 (done) gives us the foundation: fast checkpoints and restore. Phase 2 adds the "why" and the visualization.

---

## Appendix: Research Links

### macOS Native
- [FSEvents Programming Guide](https://developer.apple.com/library/archive/documentation/Darwin/Conceptual/FSEvents_ProgGuide/)
- [Sparse Bundle Format](https://en.wikipedia.org/wiki/Sparse_image)
- [hdiutil man page](https://ss64.com/osx/hdiutil.html)
- [APFS Snapshots](https://developer.apple.com/documentation/foundation/file_system/about_apple_file_system)

### Filesystem Implementations
- [FUSE-T Project](https://github.com/macos-fuse-t/fuse-t) — Kext-less FUSE for macOS
- [NFS on macOS](https://support.apple.com/guide/mac-help/connect-to-a-computer-or-server-mchlp1180/mac)
- [JuiceFS Architecture](https://juicefs.com/docs/community/architecture) — Reference architecture
- [JuiceFS Internals](https://juicefs.com/docs/community/development/internals) — Data format details

### Content-Addressed Storage
- [Content-Addressable Storage](https://en.wikipedia.org/wiki/Content-addressable_storage)
- [Kopia Architecture](https://kopia.io/docs/advanced/architecture/) — Backup with CDC
- [Restic Design](https://restic.readthedocs.io/en/latest/100_references.html) — Another CDC backup tool

### Object Storage
- [S3 API](https://docs.aws.amazon.com/AmazonS3/latest/API/Welcome.html)
- [Cloudflare R2](https://developers.cloudflare.com/r2/) — S3-compatible, no egress fees
- [Backblaze B2](https://www.backblaze.com/b2/docs/) — Cheap S3-compatible storage

### Related Projects
- [Perkeep](https://perkeep.org/) — Content-addressed personal storage
- [IPFS](https://ipfs.tech/) — Distributed content-addressed storage
- [Bup](https://github.com/bup/bup) — Git-based backup with dedup
