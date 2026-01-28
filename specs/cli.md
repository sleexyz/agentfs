# AgentFS CLI Specification

> Version 1.0 — Phase 1 Implementation

---

## Overview

AgentFS is a CLI tool for instant checkpointing and restore of macOS projects using sparse bundles and APFS reflinks.

**Core concept:** Projects live inside mounted sparse bundles. Checkpoints clone the sparse bundle's bands (not individual files), making checkpoint speed O(bands) instead of O(files).

---

## Command Structure

```
agentfs
├── init <name>              # Create and mount a new store
├── open <name>              # Mount existing store
├── close [name]             # Unmount store
├── delete <name>            # Delete store and all checkpoints
├── list                     # List all stores
├── use <name>               # Set context for current directory
├── status                   # Show current context and status
│
├── checkpoint               # Checkpoint operations
│   ├── create [message]     # Create checkpoint
│   ├── list                 # List checkpoints
│   ├── info <version>       # Show checkpoint details
│   └── delete <version>     # Delete checkpoint
│
├── restore <version>        # Restore to checkpoint
└── diff [v1] [v2]           # Show changes between checkpoints
```

---

## Global Flags

| Flag | Short | Description |
|------|-------|-------------|
| `--store <name>` | | Override store context |
| `--json` | | Output as JSON (for scripting) |
| `--force` | `-f` | Skip confirmation prompts |
| `--help` | `-h` | Show help |

---

## Context System

AgentFS uses a `.agentfs` file to determine which store commands operate on.

### Resolution Order

1. `--store <name>` flag (explicit override)
2. `.agentfs` file in current directory
3. `.agentfs` file in parent directories (searched upward)
4. Error: "No store selected"

### The .agentfs File

- Plain text file containing store name
- Created by `agentfs init` (in mount point) and `agentfs use`
- Example contents: `myproject`

```bash
# Set context explicitly
agentfs use myproject
# Creates .agentfs in current directory

# Or init creates it in mount point
agentfs init myproject --mount ~/projects/myproject
# Creates ~/projects/myproject/.agentfs
```

---

## Store Commands

### `agentfs init <name>`

Create a new sparse bundle store and mount it.

**Arguments:**
- `<name>` — Store name (required, alphanumeric + hyphens)

**Flags:**
- `--size <size>` — Sparse bundle size (default: "50G")
- `--mount <path>` — Mount point (default: `~/projects/<name>`)

**Behavior:**
1. Create sparse bundle at `~/.agentfs/stores/<name>/<name>.sparsebundle`
2. Mount at specified path
3. Create `.agentfs` context file in mount point
4. Record store in SQLite database

**Output:**
```
Creating store...
Created store 'myproject'
Mounted at /Users/you/projects/myproject
Created .agentfs
```

**Exit codes:**
- 0: Success
- 1: Store already exists
- 5: Mount failed

---

### `agentfs open <name>`

Mount an existing store.

**Arguments:**
- `<name>` — Store name (required)

**Behavior:**
1. Look up store in database
2. Mount sparse bundle at recorded mount path
3. Update mounted_at timestamp

**Output:**
```
Mounted at /Users/you/projects/myproject
```

**Exit codes:**
- 0: Success
- 3: Store not found
- 5: Mount failed (already mounted, or hdiutil error)

---

### `agentfs close [name]`

Unmount a store.

**Arguments:**
- `[name]` — Store name (optional, uses context if omitted)

**Behavior:**
1. Resolve store from argument or context
2. Unmount sparse bundle
3. Clear mounted_at timestamp

**Output:**
```
Unmounted 'myproject'
```

**Exit codes:**
- 0: Success
- 3: Store not found
- 5: Unmount failed

---

### `agentfs delete <name>`

Delete a store and all its checkpoints.

**Arguments:**
- `<name>` — Store name (required)

**Flags:**
- `-f, --force` — Skip confirmation prompt

**Behavior:**
1. Prompt for confirmation (unless --force)
2. Unmount if mounted
3. Delete sparse bundle directory
4. Delete checkpoints directory
5. Remove from database

**Output:**
```
Delete store 'myproject' and all 47 checkpoints? [y/N] y
Deleted 'myproject'
```

**Exit codes:**
- 0: Success
- 3: Store not found
- 1: User cancelled

---

### `agentfs list`

List all stores.

**Flags:**
- `--json` — Output as JSON

**Output (table):**
```
NAME        SIZE     MOUNTED  CHECKPOINTS
myproject   50.0 GiB Yes      47
oldproject  20.0 GiB No       12
```

**Output (JSON):**
```json
[
  {
    "name": "myproject",
    "size_bytes": 53687091200,
    "mounted": true,
    "mount_path": "/Users/you/projects/myproject",
    "checkpoint_count": 47
  }
]
```

---

### `agentfs use <name>`

Set context for current directory.

**Arguments:**
- `<name>` — Store name (required)

**Behavior:**
1. Verify store exists
2. Create `.agentfs` file in current directory with store name

**Output:**
```
Created .agentfs
```

---

### `agentfs status`

Show current context and status.

**Behavior:**
1. Resolve store from context
2. Query checkpoint count and latest checkpoint

**Output:**
```
Store:       myproject
Mount:       /Users/you/projects/myproject
Mounted:     Yes
Checkpoints: 4
Latest:      v4 "before risky refactor" (2m ago)
```

**Exit codes:**
- 0: Success
- 1: No store selected (no context)

---

## Checkpoint Commands

### `agentfs checkpoint create [message]`

Create a new checkpoint.

**Arguments:**
- `[message]` — Optional checkpoint message

**Behavior:**
1. Resolve store from context or --store
2. Verify store is mounted
3. Sync filesystem buffers (`sync -f <mountpoint>`)
4. Clone bands directory with APFS reflink (`/bin/cp -Rc`)
5. Update `latest` symlink
6. Record in database with next version number

**Output:**
```
Creating checkpoint...
Created v4 "before risky refactor" (58ms)
```

**Performance:** ~60-80ms regardless of file count (clones bands, not files)

**Exit codes:**
- 0: Success
- 3: Store not found
- 5: Store not mounted

---

### `agentfs checkpoint list`

List checkpoints for current store.

**Flags:**
- `--limit <n>` — Limit results (default: all)
- `--json` — Output as JSON

**Output (table):**
```
VERSION  MESSAGE                  CREATED
v4       before risky refactor    2m ago
v3       added authentication     1h ago
v2       initial setup            2h ago
```

**Output (JSON):**
```json
[
  {
    "version": 4,
    "message": "before risky refactor",
    "created_at": "2026-01-27T10:32:15Z"
  }
]
```

---

### `agentfs checkpoint info <version>`

Show details for a specific checkpoint.

**Arguments:**
- `<version>` — Version number (e.g., `v4` or `4`)

**Output:**
```
Checkpoint:  v4
Store:       myproject
Message:     before risky refactor
Created:     2026-01-27 10:32:15
```

**Exit codes:**
- 0: Success
- 4: Checkpoint not found

---

### `agentfs checkpoint delete <version>`

Delete a checkpoint.

**Arguments:**
- `<version>` — Version number

**Flags:**
- `-f, --force` — Skip confirmation

**Behavior:**
1. Prompt for confirmation (unless --force)
2. Delete checkpoint directory
3. Remove from database
4. Update `latest` symlink if needed

**Output:**
```
Delete checkpoint v2? [y/N] y
Deleted v2
```

**Exit codes:**
- 0: Success
- 4: Checkpoint not found

---

## Restore Command

### `agentfs restore <version>`

Restore to a previous checkpoint.

**Arguments:**
- `<version>` — Version number to restore to

**Flags:**
- `-f, --force` — Skip confirmation

**Behavior:**
1. Prompt for confirmation (unless --force)
2. Create checkpoint of current state ("pre-restore")
3. Unmount sparse bundle
4. Replace bands directory with checkpoint clone
5. Remount sparse bundle

**Output:**
```
Restore to v3? Current state will be saved as v5. [y/N] y
Creating checkpoint v5 "pre-restore"...
Unmounting...
Restoring from v3...
Mounting...
Restored to v3 "added authentication" (582ms)
```

**Performance:** ~500-1000ms (dominated by hdiutil unmount/remount)

**Exit codes:**
- 0: Success
- 4: Checkpoint not found
- 5: Mount/unmount failed

---

## Diff Command

### `agentfs diff [v1] [v2]`

Show changes between checkpoints or between checkpoint and current state.

**Arguments:**
- `[v1]` — First version (optional, defaults to latest)
- `[v2]` — Second version (optional, defaults to current)

**Note:** Currently shows band-level diff, not file-level. Phase 2 will add file-level diff.

**Output:**
```
Added:    3
Deleted:  1
Modified: 2
```

---

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | General error |
| 2 | Usage error (bad arguments) |
| 3 | Store not found |
| 4 | Checkpoint not found |
| 5 | Mount/unmount failed |

---

## Filesystem Layout

```
~/.agentfs/
├── agentfs.db                          # SQLite metadata
└── stores/
    └── myproject/
        ├── myproject.sparsebundle/     # The sparse bundle
        │   ├── Info.plist
        │   ├── bands/                  # Data stored here (~8MB each)
        │   │   ├── 0
        │   │   ├── 1
        │   │   └── ...
        │   └── token
        └── checkpoints/
            ├── v1/                     # APFS reflink clone of bands/
            ├── v2/
            ├── v3/
            └── latest -> v3            # Symlink to latest

~/projects/myproject/                   # Mount point
├── .agentfs                            # Context file (contains "myproject")
├── src/
├── node_modules/
└── .git/
```

---

## Database Schema

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
    version INTEGER NOT NULL,
    message TEXT,
    created_at INTEGER NOT NULL,
    UNIQUE(store_id, version)
);

CREATE INDEX idx_checkpoints_store ON checkpoints(store_id, version DESC);
```

---

## Examples

### Basic Workflow

```bash
# Create a new project store
agentfs init myproject --mount ~/projects/myproject

# Work on your project
cd ~/projects/myproject
npm init -y
npm install express

# Create checkpoint before risky change
agentfs checkpoint create "before refactor"

# Make changes...
# Oops, something went wrong

# Restore to previous state
agentfs restore v1

# Check status
agentfs status
```

### Using Context

```bash
# Inside project directory, context is automatic
cd ~/projects/myproject
agentfs checkpoint create "wip"
agentfs checkpoint list

# From outside, use --store
agentfs --store myproject checkpoint list

# Or set context explicitly
agentfs use myproject
agentfs checkpoint list
```

### Scripting with JSON

```bash
# Get latest checkpoint version
latest=$(agentfs checkpoint list --json | jq -r '.[0].version')

# List all stores
agentfs list --json | jq -r '.[].name'
```

---

## Performance Characteristics

| Operation | Time | Notes |
|-----------|------|-------|
| checkpoint create | ~60-80ms | APFS reflink on bands |
| restore | ~500-1000ms | Unmount + clone + remount |
| init | ~500ms | Create + mount sparse bundle |
| open | ~200ms | hdiutil attach |
| close | ~100ms | hdiutil detach |

Checkpoint time is independent of file count because it clones bands (~100-200) not files (~10k-50k).
