# Simplified UX Specification

> Self-contained stores with adjacent mount points

---

## Overview

A simpler, more visible approach to agentfs stores:

```
~/projects/
├── foo.fs/          # The store (sparse bundle + checkpoints + metadata)
└── foo/             # Mount point (adjacent, auto-created)
```

**Key principles:**
1. **Self-contained** — everything lives inside foo.fs/
2. **Visible** — no hidden ~/.agentfs directory
3. **Portable** — move foo.fs anywhere, mount it there
4. **Adjacent mounting** — foo.fs → foo/ in same directory

---

## Store Structure

```
foo.fs/                           # Directory (sparse bundle wrapper)
├── data.sparsebundle/            # The actual sparse bundle
│   ├── Info.plist
│   ├── bands/
│   └── token
├── checkpoints/                  # Checkpoint band clones
│   ├── v1/
│   ├── v2/
│   └── latest -> v2
└── metadata.db                   # SQLite: checkpoints, settings
```

**Why wrap the sparse bundle?**
- Allows checkpoints and metadata to live alongside
- Single foo.fs directory contains everything
- Can still use hdiutil on data.sparsebundle inside

---

## Commands

### `agentfs init [name]`

Interactive wizard to create a new store.

```bash
$ cd ~/projects
$ agentfs init

  Name: foo
  Size (default 50G):

  Created foo.fs/
  Mounted at ./foo/

$ agentfs init bar    # Non-interactive with name
  Created bar.fs/
  Mounted at ./bar/
```

**Behavior:**
1. Prompt for name if not provided
2. Create `<name>.fs/` directory structure
3. Create sparse bundle inside
4. Initialize metadata.db
5. Mount at `./<name>/`

### `agentfs mount [name]`

Mount a store. Auto-detects from context.

```bash
$ agentfs mount foo       # Mount foo.fs as foo/
Mounted at ./foo/

$ cd foo
$ agentfs mount           # Auto-detect from .agentfs context
Already mounted

$ cd ~/other-place
$ agentfs mount           # No context, no argument
Error: No store specified. Use 'agentfs mount <name>' or run from a store directory.
```

**Resolution order:**
1. Explicit argument: `agentfs mount foo` → look for `foo.fs/`
2. Context file: `.agentfs` contains store path
3. Error if neither

### `agentfs unmount`

Unmount current store.

```bash
$ cd foo
$ agentfs unmount
Unmounted foo

$ ls ..
foo.fs/               # Store remains
                      # foo/ directory removed
```

### `agentfs checkpoint [message]`

Create checkpoint (unchanged from current).

```bash
$ agentfs checkpoint "before refactor"
Created v3 "before refactor" (54ms)
```

### `agentfs restore <version>`

Restore to checkpoint (unchanged from current).

```bash
$ agentfs restore v2
Restored to v2 (612ms)
```

### `agentfs diff <version> [version2]`

Diff (unchanged from current).

```bash
$ agentfs diff v2
Comparing v2 → current
Modified: src/app.ts
```

### `agentfs list`

List stores in current directory.

```bash
$ agentfs list
STORE     SIZE      MOUNTED  CHECKPOINTS
foo.fs    50 GiB    Yes      12
bar.fs    20 GiB    No       3
```

**Scans for `*.fs/` directories in cwd.**

### `agentfs delete <name>`

Delete a store.

```bash
$ agentfs delete foo
Delete foo.fs and all 12 checkpoints? [y/N] y
Unmounting...
Deleted foo.fs/
```

---

## Context System

The `.agentfs` file now contains the full path to the store:

```bash
$ cat foo/.agentfs
/Users/me/projects/foo.fs
```

This allows:
- Commands to work from inside the mount
- Store to be identified even if renamed
- Portable if paths are relative (future enhancement)

---

## Mount Point Behavior

### Creating Mount Point

```bash
# agentfs mount foo
1. Check foo.fs/ exists
2. Create foo/ directory if not exists
3. Mount foo.fs/data.sparsebundle at foo/
4. Write foo/.agentfs with store path
```

### Unmounting

```bash
# agentfs unmount (from inside foo/)
1. Read .agentfs to find store
2. hdiutil detach foo/
3. Remove foo/ directory (it was just a mount point)
```

### Already Mounted

```bash
$ agentfs mount foo
Already mounted at ./foo/
```

---

## Metadata Schema

```sql
-- foo.fs/metadata.db

CREATE TABLE store (
    id INTEGER PRIMARY KEY CHECK (id = 1),  -- Singleton
    name TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    size_bytes INTEGER NOT NULL
);

CREATE TABLE checkpoints (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    version INTEGER NOT NULL UNIQUE,
    message TEXT,
    created_at INTEGER NOT NULL
);

CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT
);
```

**No stores table** — each store is self-contained, no global registry.

---

## File Discovery

### Finding Store from Mount Point

```go
func findStoreFromCwd() (string, error) {
    // Look for .agentfs file
    agentfsFile := findUpwards(".agentfs")
    if agentfsFile != "" {
        storePath := readFile(agentfsFile)
        return storePath, nil
    }

    // Look for *.fs in current directory
    stores := glob("*.fs")
    if len(stores) == 1 {
        return stores[0], nil
    }

    return "", errors.New("no store found")
}
```

### Finding Store from Name

```go
func findStoreByName(name string) (string, error) {
    // Try exact match
    if exists(name + ".fs") {
        return name + ".fs", nil
    }

    // Try with .fs suffix
    if exists(name) && isDir(name) && exists(name + "/data.sparsebundle") {
        return name, nil
    }

    return "", errors.New("store not found: " + name)
}
```

---

## Migration from Old Format

For existing stores in `~/.agentfs/stores/`:

```bash
$ agentfs migrate myproject
Migrating myproject from ~/.agentfs/stores/myproject...
Created ./myproject.fs/
Moved sparse bundle
Moved checkpoints
Updated metadata
Done. You can now delete ~/.agentfs/stores/myproject/
```

**Not in MVP scope** — can be added later.

---

## Directory Layout Examples

### Single Project

```
~/projects/
├── myapp.fs/                 # Store
│   ├── data.sparsebundle/
│   ├── checkpoints/
│   └── metadata.db
└── myapp/                    # Mount (when mounted)
    ├── .agentfs              # Points to ../myapp.fs
    ├── src/
    ├── package.json
    └── ...
```

### Multiple Projects

```
~/projects/
├── frontend.fs/
├── frontend/                 # Mounted
├── backend.fs/
├── backend/                  # Mounted
├── shared.fs/
└── shared/                   # Mounted
```

### Unmounted

```
~/projects/
├── myapp.fs/                 # Store exists
                              # No myapp/ - not mounted
```

---

## Error Cases

### Name Collision

```bash
$ agentfs init foo
Error: foo.fs already exists

$ agentfs init foo --force
Warning: Deleting existing foo.fs
Created foo.fs/
```

### Mount Point Exists

```bash
$ mkdir foo
$ agentfs mount foo
Error: ./foo/ already exists and is not empty
Use --force to mount anyway (will hide existing contents)
```

### Store Not Found

```bash
$ agentfs mount baz
Error: baz.fs not found in current directory
```

---

## Flags

| Command | Flag | Description |
|---------|------|-------------|
| `init` | `--size <size>` | Sparse bundle size (default: 50G) |
| `init` | `--force` | Overwrite existing store |
| `mount` | `--force` | Mount even if directory exists |
| `delete` | `-f, --force` | Skip confirmation |

---

## Implementation Notes

### Sparse Bundle Location

The sparse bundle is now at `foo.fs/data.sparsebundle/` not `foo.fs/` directly. This is because:
1. We need space for checkpoints/ and metadata.db
2. foo.fs/ is a regular directory, not the bundle itself
3. hdiutil operates on data.sparsebundle inside

### Checkpoint Location

Checkpoints are now at `foo.fs/checkpoints/` not `~/.agentfs/stores/foo/checkpoints/`. The clone command becomes:

```bash
/bin/cp -Rc foo.fs/data.sparsebundle/bands/ foo.fs/checkpoints/v3/
```

### Database Location

SQLite database is at `foo.fs/metadata.db`. Open with:

```go
db, err := sql.Open("sqlite3", filepath.Join(storePath, "metadata.db"))
```

---

## Summary

| Aspect | Old | New |
|--------|-----|-----|
| Store location | `~/.agentfs/stores/foo/` | `./foo.fs/` |
| Mount point | Configurable | Adjacent `./foo/` |
| Checkpoints | `~/.agentfs/stores/foo/checkpoints/` | `./foo.fs/checkpoints/` |
| Metadata | `~/.agentfs/agentfs.db` (global) | `./foo.fs/metadata.db` (per-store) |
| Discovery | Global database | Scan for `*.fs/` |
| Portability | Tied to machine | Fully portable |
