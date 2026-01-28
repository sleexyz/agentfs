# AgentFS Daemon Specification

> Version 0.2 — Revised architecture with file-level sync

---

## Overview

AgentFS is a daemon that provides:
1. **Instant checkpointing** via sparse bundles + APFS reflinks (~20ms for any project size)
2. **Instant restore** — rollback to any checkpoint in <500ms
3. **Causality tracking** — know *why* files changed, not just *what*
4. **Zero configuration** — single install, no security dialogs, no FUSE

**Primary use case:** Agent workflows where you need to checkpoint frequently (before risky operations) and restore quickly (when things go wrong).

**Explicitly deferred:** Remote sync (Phase N+1). The MVP is purely local.

---

## Key Architectural Insight

**Why sparse bundles?**

```
Without sparse bundle:
  36,000 files → clone all 36,000 inodes → ~3 seconds

With sparse bundle:
  36,000 files → stored in 87 bands → clone 87 bands → 19ms
```

Sparse bundles **collapse thousands of files into ~100 bands**, making APFS reflinks scale to any project size.

**Why file-level sync (not band-level)?**

```
Band-level sync problem:
  Machine A modifies band 5
  Machine B modifies band 5
  Syncthing creates: band_5.sync-conflict-...
  Sparse bundle is CORRUPTED (expects exact filenames)

File-level sync solution:
  Machine A modifies src/app.ts
  Machine B modifies src/app.ts
  Syncthing creates: src/app.ts.sync-conflict-...
  User resolves conflict normally (both files accessible)
```

**Architecture: Sparse bundles for checkpointing, file sync for distribution.**

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│  USER SPACE                                                             │
│                                                                         │
│  ~/projects/myapp/          ← User works here (mounted sparse bundle)  │
│  ├── src/                                                               │
│  ├── node_modules/          (36k files, but only ~100 bands underneath)│
│  ├── .git/                  (works fine inside sparse bundle)          │
│  └── ...                                                                │
│                                                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │  AgentFS CLI (agentfs)                                           │   │
│  │                                                                   │   │
│  │  ┌─────────────────────┐  ┌─────────────────────┐               │   │
│  │  │  Store Manager      │  │  Checkpoint Manager │               │   │
│  │  │                     │  │                     │               │   │
│  │  │  - create           │  │  - create (~20ms)   │               │   │
│  │  │  - mount            │  │  - restore (<500ms) │               │   │
│  │  │  - unmount          │  │  - list             │               │   │
│  │  │  - list             │  │  - diff             │               │   │
│  │  │  - delete           │  │  - show             │               │   │
│  │  └──────────┬──────────┘  └──────────┬──────────┘               │   │
│  │             │                        │                           │   │
│  │             └────────────┬───────────┘                           │   │
│  │                          │                                        │   │
│  │  ┌───────────────────────┴───────────────────────┐               │   │
│  │  │  Metadata Store (SQLite)                       │               │   │
│  │  │  - stores, checkpoints, causality              │               │   │
│  │  └─────────────────────────────────────────────────┘               │   │
│  └───────────────────────────────────────────────────────────────────┘   │
│                                                                         │
│  Note: MVP is a CLI tool, not a daemon. Daemon may come later.         │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────┐
│  FILESYSTEM LAYOUT                                                      │
│                                                                         │
│  ~/.agentfs/                                                            │
│  ├── agentfs.db                    # SQLite metadata                    │
│  ├── config.toml                   # Configuration                      │
│  ├── syncthing/                    # Syncthing installation             │
│  │   ├── syncthing                 # Binary                             │
│  │   └── config/                   # Syncthing config                   │
│  │                                                                      │
│  └── stores/                                                            │
│      └── myapp/                                                         │
│          ├── myapp.sparsebundle/   # The sparse bundle                 │
│          │   ├── Info.plist                                            │
│          │   ├── bands/            # ~100 bands (NOT synced)           │
│          │   │   ├── 0                                                 │
│          │   │   ├── 1                                                 │
│          │   │   └── ...                                               │
│          │   └── token                                                 │
│          │                                                              │
│          └── checkpoints/          # APFS reflink clones of bands/     │
│              ├── v1/                                                   │
│              ├── v2/                                                   │
│              ├── v3/                                                   │
│              └── latest → v3                                           │
│                                                                         │
│  ~/projects/myapp/                 # MOUNT POINT (Syncthing syncs this)│
│  ├── src/app.ts                    ← Syncthing syncs these files       │
│  ├── package.json                  ← File-level conflicts handled      │
│  └── ...                                                                │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────┐
│  SYNC TOPOLOGY                                                          │
│                                                                         │
│  Mac (local)                        Cloud/Other Mac                     │
│  ┌─────────────────────┐            ┌─────────────────────┐            │
│  │ ~/projects/myapp/   │◄──────────►│ ~/projects/myapp/   │            │
│  │ (mounted bundle)    │ Syncthing  │ (regular directory  │            │
│  │                     │ file-level │  or mounted bundle) │            │
│  │ Checkpoints: local  │            │ Checkpoints: local  │            │
│  └─────────────────────┘            └─────────────────────┘            │
│                                                                         │
│  Note: Checkpoints are LOCAL ONLY. Sync is FILE-LEVEL.                 │
│  Each machine has its own checkpoint history.                          │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## Component Design

### 1. Store Manager

Manages sparse bundle lifecycle.

```go
type StoreManager interface {
    Create(name string, opts CreateOpts) (*Store, error)
    List() ([]*Store, error)
    Get(name string) (*Store, error)
    Mount(name string) error
    Unmount(name string) error
    Delete(name string, force bool) error
}

type CreateOpts struct {
    Size      string // e.g., "50G"
    MountPath string // e.g., ~/projects/myapp
}

type Store struct {
    ID         string
    Name       string
    BundlePath string    // ~/.agentfs/stores/myapp/myapp.sparsebundle
    MountPath  string    // ~/projects/myapp
    Size       int64
    CreatedAt  time.Time
    MountedAt  *time.Time
    SyncEnabled bool
}
```

**Commands:**
```bash
# Create sparse bundle with 8MB bands (macOS default)
hdiutil create -size 50G -type SPARSEBUNDLE -fs APFS \
  -volname myapp ~/.agentfs/stores/myapp/myapp.sparsebundle

# Mount
hdiutil attach ~/.agentfs/stores/myapp/myapp.sparsebundle \
  -mountpoint ~/projects/myapp

# Unmount
hdiutil detach ~/projects/myapp
```

### 2. Checkpoint Manager

Instant snapshots via APFS reflinks on bands.

```go
type CheckpointManager interface {
    Create(storeName string, opts CheckpointOpts) (*Checkpoint, error)
    List(storeName string) ([]*Checkpoint, error)
    Restore(storeName string, checkpointID string) error
    Diff(storeName string, from, to string) (*Diff, error)
    Show(storeName string, checkpointID string, path string) ([]byte, error)
    Prune(storeName string, keep int) error
}

type CheckpointOpts struct {
    Message   string
    Causality *CausalityContext
}

type CausalityContext struct {
    Agent     string            // e.g., "claude-code"
    SessionID string
    Action    string            // e.g., "Edit", "Write"
    Prompt    string            // User prompt (optional)
    Metadata  map[string]string
}

type Checkpoint struct {
    ID          string
    StoreID     string
    Message     string
    CreatedAt   time.Time
    ParentID    *string
    Causality   *CausalityContext
    BandCount   int
    TotalSize   int64
}
```

**Checkpoint flow (~20ms total):**
```bash
# 1. Sync filesystem buffers
sync ~/projects/myapp

# 2. Get next version number from DB
next_version=$(sqlite3 agentfs.db "SELECT MAX(version)+1 FROM checkpoints WHERE store_id='myapp'")

# 3. Clone bands directory (APFS reflink - instant!)
cp -Rc ~/.agentfs/stores/myapp/myapp.sparsebundle/bands/ \
       ~/.agentfs/stores/myapp/checkpoints/v${next_version}/

# 4. Update symlink
ln -sf v${next_version} ~/.agentfs/stores/myapp/checkpoints/latest

# 5. Record metadata
sqlite3 agentfs.db "INSERT INTO checkpoints (store_id, version, message, created_at) ..."
```

**Restore flow:**
```bash
# 1. Create checkpoint of current state first (v5 "pre-restore")
agentfs checkpoint create "pre-restore"

# 2. Unmount (required to swap bands)
hdiutil detach ~/projects/myapp

# 3. Backup current bands (safety)
mv .../myapp.sparsebundle/bands .../myapp.sparsebundle/bands.pre-restore

# 4. Clone target checkpoint to bands (instant!)
cp -Rc .../checkpoints/v3/ .../myapp.sparsebundle/bands/

# 5. Remount
hdiutil attach .../myapp.sparsebundle -mountpoint ~/projects/myapp

# 6. Clean up (after confirming success)
rm -rf .../myapp.sparsebundle/bands.pre-restore
```

---

## Data Model (SQLite)

```sql
CREATE TABLE stores (
    id TEXT PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    bundle_path TEXT NOT NULL,
    mount_path TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    mounted_at INTEGER
);

CREATE TABLE checkpoints (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    store_id TEXT NOT NULL REFERENCES stores(id),
    version INTEGER NOT NULL,           -- v1, v2, v3 (per-store incrementing)
    message TEXT,
    created_at INTEGER NOT NULL,

    UNIQUE(store_id, version)
);

CREATE INDEX idx_checkpoints_store ON checkpoints(store_id, version DESC);
```

**Version numbering:**
- Each store has independent version numbers: v1, v2, v3...
- Versions only increment, never reused (even after delete)
- Human-friendly: `agentfs restore v3` instead of `agentfs restore a1b2c3d4`

---

## CLI Interface

Inspired by OpenSprite CLI: subcommand grouping, context files, smart argument handling, staged output.

### Command Structure

```
agentfs
├── init <name>           Create and mount a new store
├── open <name>           Mount existing store
├── close [name]          Unmount store
├── delete <name>         Delete store
├── list                  List all stores
├── use <name>            Set context for current directory
├── status                Show current context and status
│
├── checkpoint            Checkpoint operations
│   ├── create [message]  Create checkpoint (~20ms)
│   ├── list              List checkpoints
│   ├── info <version>    Show checkpoint details
│   └── delete <version>  Delete checkpoint
│
├── restore <version>     Restore to checkpoint (<500ms)
└── diff [v1] [v2]        Show changes between checkpoints
```

### Context Resolution

AgentFS uses a `.agentfs` context file (like OpenSprite's `.opensprite`):

```bash
# Set context for current directory
agentfs use myapp
# Created .agentfs

# Context resolution priority:
# 1. --store flag (explicit override)
# 2. .agentfs file in current/parent directories
# 3. Error: "No store selected. Use --store or run 'agentfs use <name>'"
```

### Store Management

```bash
# Initialize a new store
agentfs init myapp [--size 50G] [--mount ~/projects/myapp]
# Creating store...
# Created store 'myapp'
# Mounted at ~/projects/myapp

# List all stores
agentfs list
# NAME      SIZE    MOUNTED    CHECKPOINTS
# myapp     50G     Yes        47
# oldproj   20G     No         12

# Open (mount) a store
agentfs open myapp
# Mounted at ~/projects/myapp

# Close (unmount) a store
agentfs close [name]          # Uses context if no name
# Unmounted 'myapp'

# Delete a store (confirmation required)
agentfs delete myapp
# Delete store 'myapp' and all 47 checkpoints? [y/N] y
# Deleted 'myapp'

# Skip confirmation
agentfs delete myapp -f
```

### Checkpoint Operations

Grouped under `checkpoint` subcommand:

```bash
# Create checkpoint (uses context)
agentfs checkpoint create "before risky refactor"
# Creating checkpoint...
# Created v4 "before risky refactor" (19ms)

# Or with explicit store
agentfs checkpoint create --store myapp "initial setup"

# List checkpoints
agentfs checkpoint list [--limit N]
# VERSION   MESSAGE                  CREATED
# v4        before risky refactor    2m ago
# v3        added authentication     1h ago
# v2        initial setup            2h ago

# Show checkpoint details
agentfs checkpoint info v4
# Checkpoint:  v4
# Store:       myapp
# Message:     before risky refactor
# Created:     2026-01-27 10:32:15

# Delete checkpoint (confirmation required)
agentfs checkpoint delete v2
# Delete checkpoint v2? [y/N] y
# Deleted v2

# Skip confirmation
agentfs checkpoint delete v2 -f
```

### Restore

Top-level command for discoverability:

```bash
# Restore to checkpoint (uses context)
agentfs restore v3
# Restore to v3? Current state will be saved as v5. [y/N] y
# Creating checkpoint v5 "pre-restore"...
# Unmounting...
# Restoring from v3...
# Mounting...
# Restored to v3 "added authentication" (412ms)

# With explicit store
agentfs restore --store myapp v3

# Skip confirmation
agentfs restore v3 -f
```

### Diff

```bash
# Diff current state vs checkpoint
agentfs diff v3
# Modified: src/app.ts (+50 -10)
# Added:    src/utils.ts
# Deleted:  src/old.ts

# Diff between two checkpoints
agentfs diff v2 v4
```

### Status

```bash
# Show current context and status
agentfs status
# Store:       myapp
# Mount:       ~/projects/myapp
# Mounted:     Yes
# Checkpoints: 4
# Latest:      v4 "before risky refactor" (2m ago)
```

### Output Formatting

```bash
# Human-readable tables by default
agentfs checkpoint list
# VERSION   MESSAGE                  CREATED
# v4        before risky refactor    2m ago

# JSON for scripting
agentfs checkpoint list --json
# [{"version":"v4","message":"before risky refactor","created_at":"2026-01-27T10:32:15Z"}]
```

### Flags

```bash
# Global flags
--store <name>    # Override store context
--json            # Output as JSON
-f, --force       # Skip confirmation prompts
-h, --help        # Show help
```

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | General error |
| 2 | Usage error (bad arguments) |
| 3 | Store not found |
| 4 | Checkpoint not found |
| 5 | Mount/unmount failed |

### Staged Output

Long operations show progress:

```bash
agentfs restore v3
# Restore to v3? Current state will be saved as v5. [y/N] y
# Creating checkpoint v5 "pre-restore"...
# Unmounting...
# Restoring from v3...
# Mounting...
# Restored to v3 "added authentication" (412ms)
```

---

### Analysis (Causality) — Phase 2

```bash
# What agent actions affected this file?
agentfs blame src/auth.ts
# cp_048  2m    L1-50    claude-code/Edit   "added authentication"
# cp_032  2d    L51-80   manual             "initial file"

# What changed recently?
agentfs timeline --since 1h
# 10:32  cp_048  +147 -23  claude-code  src/auth.ts, src/api.ts
# 10:28  cp_047  +12  -0   manual       package.json

# Find checkpoints by message
agentfs search "auth"
# cp_048  "added authentication"
# cp_033  "auth placeholder"

# Find checkpoints by agent
agentfs log --agent claude-code --limit 10
```

---

## Integration: Claude Code Hooks

AgentFS can auto-checkpoint on agent actions:

```bash
# ~/.claude/hooks.json (proposed)
{
  "pre_tool_use": {
    "Edit": "agentfs checkpoint --auto --agent claude-code --action Edit",
    "Write": "agentfs checkpoint --auto --agent claude-code --action Write",
    "Bash": null  // Don't checkpoint on every bash command
  }
}
```

Or configure in AgentFS:

```toml
# ~/.agentfs/config.toml
[hooks]
claude_code = true
checkpoint_on = ["Edit", "Write", "MultiEdit", "NotebookEdit"]
min_interval = "10s"  # Don't checkpoint more than every 10s
```

---

## Performance Characteristics

| Operation | Time | Notes |
|-----------|------|-------|
| Checkpoint (any project size) | ~20ms | APFS reflink on bands |
| Restore | ~500ms | Unmount + clone + remount |
| Mount store | ~200ms | hdiutil attach |
| Unmount store | ~100ms | hdiutil detach |
| Sync status check | ~10ms | Syncthing REST API |

**Why checkpoints are always fast:**
- 36,000 files → ~100 bands → clone 100 inodes → 20ms
- File count doesn't matter, only band count
- Band count is O(data size / 8MB), not O(file count)

---

## Configuration

```toml
# ~/.agentfs/config.toml

[stores]
base_path = "~/.agentfs/stores"
default_size = "50G"
default_mount_base = "~/projects"

[checkpoints]
auto_prune = true
keep_count = 100

[hooks]  # Phase 2
claude_code = false
checkpoint_on = ["Edit", "Write", "MultiEdit"]
min_interval = "10s"
```

---

## Implementation Phases

### Phase 1: Core MVP (1-2 weeks)
- [ ] Store manager (create, mount, unmount, list, delete)
- [ ] Checkpoint manager (create, restore, list, diff)
- [ ] SQLite metadata store
- [ ] CLI: init, open, close, checkpoint, restore, log, diff
- [ ] Handle real projects (node_modules, .git, etc.)

**Deliverable:** Instant checkpoint (~20ms) and restore (<500ms) for real projects.

### Phase 2: Causality & Analysis (1 week)
- [ ] Causality fields in checkpoint schema
- [ ] CLI: blame, timeline, search
- [ ] Filtering by agent, action, time
- [ ] Claude Code hook integration (optional)

**Deliverable:** Know why files changed.

### Phase 3: Polish (1 week)
- [ ] `brew install agentfs` formula
- [ ] launchd plist for auto-start
- [ ] Documentation
- [ ] Error recovery (corruption detection, etc.)

**Deliverable:** Easy installation, production-ready.

### Phase N+1: Remote Sync (Future)
- [ ] Syncthing integration for file-level sync
- [ ] Multi-machine support
- [ ] Conflict handling

**Explicitly deferred.** The core value is local checkpoint/restore.

---

## MVP Scope

### In Scope (Phase 1)
- Create/mount/unmount sparse bundle stores
- Instant checkpoint (~20ms)
- Fast restore (<500ms)
- List checkpoints with messages
- Diff between checkpoints
- Works with real projects (node_modules, .git, etc.)

### Deferred (Phase 2+)
- Causality tracking (agent/action metadata)
- Claude Code hook integration
- blame/timeline commands

### Explicitly Out of Scope (Phase N+1)
- Remote sync (Syncthing integration)
- Multi-machine support
- Daemon mode with auto-checkpointing

**Philosophy:** Nail the core (checkpoint/restore) before adding features.

---

## Appendix: Why This Architecture

### Why sparse bundles?

```
Direct file clone (36k files):     ~3 seconds
Sparse bundle bands (100 bands):   ~20ms
```

Sparse bundles collapse thousands of files into ~100 bands. APFS reflinks on bands = instant checkpoints regardless of project size.

### Why not just copy files directly?

APFS reflinks are O(inodes), not O(bytes). A project with 36k files (node_modules + .git) would take seconds to checkpoint. Sparse bundles reduce this to ~100 inodes = 20ms.

### What about .git and node_modules?

They work fine inside the sparse bundle:
- `.git/` — All git operations work normally
- `node_modules/` — npm/yarn/pnpm work normally
- The sparse bundle is just a container; the filesystem inside is standard APFS

### Why not a daemon?

For MVP, a CLI tool is simpler:
- No socket management
- No background process to monitor
- Just run `agentfs checkpoint` when you want a checkpoint

A daemon may be added later for:
- Auto-checkpointing on file changes
- Integration with Claude Code hooks
- Status menubar indicator
