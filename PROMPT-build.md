# Phase 1: AgentFS Core MVP

## Goal

Build a CLI tool that provides instant checkpoint (~20ms) and restore (<500ms) for macOS projects.

**Success looks like:**
- `agentfs checkpoint create` completes in ~20ms for a 36k-file project
- `agentfs restore v3` completes in <500ms
- CLI feels polished (context files, staged output, confirmations)

---

## Prerequisites

- [x] Spec complete: `specs/agentfs-daemon.md`
- [x] Architecture validated: sparse bundles + APFS reflinks
- [x] CLI design inspired by OpenSprite patterns
- [ ] User confirms: ready to build

---

## Tasks

### Project Setup

- [ ] Initialize Go module: `github.com/agentfs/agentfs`
- [ ] Set up CLI with Cobra (subcommand structure)
- [ ] Create SQLite schema for metadata
- [ ] Establish project layout:
  ```
  agentfs/
  ├── cmd/agentfs/
  │   ├── main.go
  │   ├── root.go           # Root command, context resolution
  │   ├── init.go           # agentfs init
  │   ├── open.go           # agentfs open
  │   ├── close.go          # agentfs close
  │   ├── delete.go         # agentfs delete
  │   ├── list.go           # agentfs list
  │   ├── use.go            # agentfs use
  │   ├── status.go         # agentfs status
  │   ├── checkpoint.go     # agentfs checkpoint (subcommand group)
  │   ├── restore.go        # agentfs restore
  │   └── diff.go           # agentfs diff
  ├── internal/
  │   ├── store/            # Sparse bundle management
  │   ├── checkpoint/       # Checkpoint operations
  │   ├── context/          # .agentfs file handling
  │   └── db/               # SQLite metadata
  ├── go.mod
  └── go.sum
  ```

### Context System

- [ ] Implement `.agentfs` context file
  - Create with `agentfs use <name>`
  - Search up directory tree (like .git)
  - Plain text file containing store name
- [ ] Context resolution in all commands:
  1. `--store` flag (explicit override)
  2. `.agentfs` file in current/parent directories
  3. Error with helpful message

### Store Manager

- [ ] `agentfs init <name>` — create sparse bundle
  ```
  Creating store...
  Created store 'myapp'
  Mounted at ~/projects/myapp
  ```
  - Create sparse bundle with hdiutil
  - Mount at specified path (default: ~/projects/<name>)
  - Record in SQLite
  - Auto-run `agentfs use <name>` to set context

- [ ] `agentfs open <name>` — mount existing store
  ```
  Mounted at ~/projects/myapp
  ```

- [ ] `agentfs close [name]` — unmount store
  - Uses context if no name provided
  ```
  Unmounted 'myapp'
  ```

- [ ] `agentfs list` — show all stores
  ```
  NAME      SIZE    MOUNTED    CHECKPOINTS
  myapp     50G     Yes        47
  oldproj   20G     No         12
  ```

- [ ] `agentfs delete <name>` — remove store
  - Require confirmation (bypass with `-f`)
  ```
  Delete store 'myapp' and all 47 checkpoints? [y/N] y
  Deleted 'myapp'
  ```

- [ ] `agentfs use <name>` — set context
  ```
  Created .agentfs
  ```

- [ ] `agentfs status` — show current context
  ```
  Store:       myapp
  Mount:       ~/projects/myapp
  Mounted:     Yes
  Checkpoints: 4
  Latest:      v4 "before risky refactor" (2m ago)
  ```

### Checkpoint Manager

- [ ] `agentfs checkpoint create [message]` — create checkpoint
  - Uses context (or `--store`)
  - Sync filesystem buffers
  - Clone bands/ with APFS reflink
  - Record in SQLite with version number
  - Show timing
  ```
  Creating checkpoint...
  Created v4 "before risky refactor" (19ms)
  ```

- [ ] `agentfs checkpoint list` — list checkpoints
  ```
  VERSION   MESSAGE                  CREATED
  v4        before risky refactor    2m ago
  v3        added authentication     1h ago
  v2        initial setup            2h ago
  ```
  - Support `--limit N`
  - Support `--json` for scripting

- [ ] `agentfs checkpoint info <version>` — show details
  ```
  Checkpoint:  v4
  Store:       myapp
  Message:     before risky refactor
  Created:     2026-01-27 10:32:15
  ```

- [ ] `agentfs checkpoint delete <version>` — delete checkpoint
  - Require confirmation (bypass with `-f`)
  ```
  Delete checkpoint v2? [y/N] y
  Deleted v2
  ```

### Restore

- [ ] `agentfs restore <version>` — restore to checkpoint
  - Require confirmation (bypass with `-f`)
  - Auto-create checkpoint of current state before restore
  - Show staged progress output
  ```
  Restore to v3? Current state will be saved as v5. [y/N] y
  Creating checkpoint v5 "pre-restore"...
  Unmounting...
  Restoring from v3...
  Mounting...
  Restored to v3 "added authentication" (412ms)
  ```

### Diff

- [ ] `agentfs diff [version]` — diff vs current
- [ ] `agentfs diff <v1> <v2>` — diff between checkpoints
  ```
  Modified: src/app.ts (+50 -10)
  Added:    src/utils.ts
  Deleted:  src/old.ts
  ```

### Polish

- [ ] Global flags: `--store`, `--json`, `-f/--force`, `-h/--help`
- [ ] Exit codes (0=success, 1=error, 2=usage, 3=not found, etc.)
- [ ] Human-friendly error messages
- [ ] Staged output for long operations

### Testing

- [ ] Test with real Next.js project (node_modules)
- [ ] Test with git repository inside store
- [ ] Benchmark: checkpoint create time for 36k files → ~20ms
- [ ] Benchmark: restore time → <500ms
- [ ] Test context resolution (`.agentfs` file)
- [ ] Test confirmation prompts and `-f` bypass

---

## Reference

### Command Structure

```
agentfs
├── init <name>           Create and mount a new store
├── open <name>           Mount existing store
├── close [name]          Unmount store (uses context)
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
└── diff [v1] [v2]        Show changes
```

### Sparse Bundle Commands

```bash
# Create
hdiutil create -size 50G -type SPARSEBUNDLE -fs APFS \
  -volname myapp ~/.agentfs/stores/myapp/myapp.sparsebundle

# Mount
hdiutil attach ~/.agentfs/stores/myapp/myapp.sparsebundle \
  -mountpoint ~/projects/myapp

# Unmount
hdiutil detach ~/projects/myapp
```

### APFS Reflink Clone

```bash
# Instant clone (shares blocks, zero disk cost)
cp -Rc source_dir/ dest_dir/
```

### Directory Layout

```
~/.agentfs/
├── agentfs.db                    # SQLite metadata
└── stores/
    └── myapp/
        ├── myapp.sparsebundle/
        │   └── bands/
        └── checkpoints/
            ├── v1/               # Reflink clone of bands/
            ├── v2/
            ├── v3/
            └── latest -> v3

~/projects/myapp/                 # Mount point
├── .agentfs                      # Context file (contains "myapp")
├── src/
├── node_modules/
└── .git/
```

### SQLite Schema

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

### Performance Targets

| Operation | Target |
|-----------|--------|
| Checkpoint create | ~20ms |
| Restore | <500ms |
| Mount | ~200ms |
| Unmount | ~100ms |

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | General error |
| 2 | Usage error |
| 3 | Store not found |
| 4 | Checkpoint not found |
| 5 | Mount/unmount failed |

---

## Out of Scope (Phase 1)

- Causality tracking (agent/action metadata) — Phase 2
- Claude Code hooks — Phase 2
- Remote sync — Phase N+1
- Daemon mode — Future
- brew formula — Phase 3

---

## Output

- Working `agentfs` binary
- All commands listed above functional
- README with usage instructions
- Benchmark results documented
