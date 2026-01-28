# Diff Specification

> Phase 2 — Mount-and-compare approach

---

## Overview

Diff compares filesystem state between checkpoints (or between a checkpoint and live CWD) by temporarily mounting checkpoint bands and comparing file trees directly.

**Key insight:** No hashing or content-addressing needed. Just mount both states and compare.

---

## Commands

### `agentfs diff <version>`

Compare live CWD against a checkpoint.

```bash
agentfs diff v3
```

**Output:**
```
Comparing v3 → current

Modified:  src/auth.ts
Modified:  src/utils.ts
Added:     src/newfile.ts
Deleted:   src/oldfile.ts

4 files changed
```

### `agentfs diff <version1> <version2>`

Compare two checkpoints.

```bash
agentfs diff v3 v7
```

**Output:**
```
Comparing v3 → v7

Modified:  src/auth.ts (+47 -12)
Modified:  package.json (+2 -1)
Added:     src/middleware.ts
Added:     tests/auth.test.ts

4 files changed, 2 added
```

### `agentfs diff <version> --stat`

Show change statistics only (fast).

```bash
agentfs diff v3 --stat
```

**Output:**
```
Comparing v3 → current

 src/auth.ts   | 59 ++++++++++++++++++++++++++++++-----------------
 src/utils.ts  | 12 ++++++----
 2 files changed, 42 insertions(+), 17 deletions(-)
```

### `agentfs diff <version> -- <path>`

Diff a specific file.

```bash
agentfs diff v3 -- src/auth.ts
```

**Output:**
```diff
--- v3/src/auth.ts
+++ current/src/auth.ts
@@ -42,6 +42,10 @@
 function validateToken(token) {
+  if (token.expired()) {
+    throw new AuthError('Token expired');
+  }
   return token.isValid();
 }
```

---

## Algorithm

### Step 1: Mount Checkpoints

```go
func (d *Differ) Diff(v1, v2 string) (*DiffResult, error) {
    // v2 can be "current" to mean live CWD

    var mount1, mount2 string

    // Mount v1
    mount1, err = d.mountCheckpoint(v1)
    if err != nil {
        return nil, err
    }
    defer d.unmount(mount1)

    // Mount v2 or use CWD
    if v2 == "current" {
        mount2 = d.store.MountPath  // Live CWD
    } else {
        mount2, err = d.mountCheckpoint(v2)
        if err != nil {
            return nil, err
        }
        defer d.unmount(mount2)
    }

    // Compare
    return d.compareDirectories(mount1, mount2)
}
```

### Step 2: Mount Checkpoint Bands

Create a temporary sparse bundle from checkpoint bands and mount it.

```go
func (d *Differ) mountCheckpoint(version string) (string, error) {
    checkpointPath := filepath.Join(d.store.Path, "checkpoints", version)

    // Create temp sparse bundle structure
    tmpBundle := filepath.Join(os.TempDir(), fmt.Sprintf("agentfs-diff-%s-%d", version, time.Now().UnixNano()))

    // Copy sparse bundle metadata (Info.plist, token, etc.)
    // but symlink/clone the bands from checkpoint
    if err := d.createTempBundle(tmpBundle, checkpointPath); err != nil {
        return "", err
    }

    // Mount
    mountPoint := tmpBundle + "-mount"
    if err := os.MkdirAll(mountPoint, 0755); err != nil {
        return "", err
    }

    cmd := exec.Command("hdiutil", "attach", tmpBundle,
        "-mountpoint", mountPoint,
        "-nobrowse",    // Don't show in Finder
        "-quiet")
    if err := cmd.Run(); err != nil {
        return "", err
    }

    return mountPoint, nil
}
```

### Step 3: Compare Directory Trees

Walk both trees and compare files by mtime and size.

```go
type FileInfo struct {
    Path    string
    Size    int64
    Mtime   time.Time
    IsDir   bool
}

type Change struct {
    Path   string
    Type   ChangeType  // Added, Deleted, Modified
    OldInfo *FileInfo
    NewInfo *FileInfo
}

func (d *Differ) compareDirectories(dir1, dir2 string) (*DiffResult, error) {
    files1 := d.walkDirectory(dir1)
    files2 := d.walkDirectory(dir2)

    var changes []Change

    // Find modified and deleted files
    for path, info1 := range files1 {
        if info2, exists := files2[path]; exists {
            if info1.Size != info2.Size || !info1.Mtime.Equal(info2.Mtime) {
                changes = append(changes, Change{
                    Path:    path,
                    Type:    Modified,
                    OldInfo: info1,
                    NewInfo: info2,
                })
            }
        } else {
            changes = append(changes, Change{
                Path:    path,
                Type:    Deleted,
                OldInfo: info1,
            })
        }
    }

    // Find added files
    for path, info2 := range files2 {
        if _, exists := files1[path]; !exists {
            changes = append(changes, Change{
                Path:    path,
                Type:    Added,
                NewInfo: info2,
            })
        }
    }

    return &DiffResult{Changes: changes}, nil
}
```

### Step 4: Show File Diff

For modified files, use native diff or custom implementation.

```go
func (d *Differ) showFileDiff(path1, path2 string) error {
    // Use native diff for text files
    cmd := exec.Command("diff", "-u", path1, path2)
    cmd.Stdout = os.Stdout
    cmd.Run()  // Ignore exit code (diff returns 1 if files differ)
    return nil
}
```

---

## Performance

| Operation | Time | Notes |
|-----------|------|-------|
| Mount checkpoint | ~200ms | hdiutil attach |
| Walk 10k files | ~50ms | Just stat() calls |
| Unmount | ~100ms | hdiutil detach |
| **Total (vs CWD)** | **~350ms** | Mount + walk + unmount |
| **Total (v1 vs v2)** | **~650ms** | Two mounts |

---

## Temporary Bundle Creation

To mount checkpoint bands, we create a temporary sparse bundle that references them:

```go
func (d *Differ) createTempBundle(tmpBundle, checkpointPath string) error {
    // Create bundle structure
    os.MkdirAll(tmpBundle, 0755)

    // Copy metadata files from original bundle
    origBundle := filepath.Join(d.store.Path, d.store.Name+".sparsebundle")

    copyFile(filepath.Join(origBundle, "Info.plist"),
             filepath.Join(tmpBundle, "Info.plist"))
    copyFile(filepath.Join(origBundle, "token"),
             filepath.Join(tmpBundle, "token"))

    // Clone bands from checkpoint (APFS reflink - instant)
    bandsDir := filepath.Join(tmpBundle, "bands")
    cmd := exec.Command("/bin/cp", "-Rc", checkpointPath, bandsDir)
    return cmd.Run()
}
```

**Key:** We use `cp -Rc` (APFS clone) so creating the temp bundle is instant and uses no extra disk space.

---

## Edge Cases

### Binary Files

```go
func isBinaryFile(path string) bool {
    // Check first 8KB for null bytes
    f, _ := os.Open(path)
    defer f.Close()

    buf := make([]byte, 8192)
    n, _ := f.Read(buf)

    for i := 0; i < n; i++ {
        if buf[i] == 0 {
            return true
        }
    }
    return false
}
```

For binary files, show size change only:
```
Binary file src/image.png changed (1.2 MB → 1.4 MB)
```

### Symlinks

Compare symlink targets, not contents:
```go
if info.Mode()&os.ModeSymlink != 0 {
    target1, _ := os.Readlink(path1)
    target2, _ := os.Readlink(path2)
    if target1 != target2 {
        // Symlink target changed
    }
}
```

### Permissions

Optionally show permission changes:
```bash
agentfs diff v3 --permissions
```

```
Permission: src/deploy.sh (0644 → 0755)
```

### Ignored Patterns

Skip common untracked files:
```go
var defaultIgnore = []string{
    ".DS_Store",
    "*.swp",
    ".git",  // Already in sparse bundle, but skip in diff
}
```

---

## CLI Flags

| Flag | Description |
|------|-------------|
| `--stat` | Show statistics only, no content diff |
| `--name-only` | Just list changed file names |
| `--name-status` | List files with change type (A/M/D) |
| `--no-pager` | Don't pipe through less/more |
| `-- <path>` | Diff specific file or directory |

---

## Output Formats

### Default (Human-Readable)

```
Comparing v3 → current

Modified:  src/auth.ts
  +47 -12 lines

Modified:  src/utils.ts
  +5 -2 lines

Added:     src/newfile.ts
  +120 lines

2 modified, 1 added, 0 deleted
```

### JSON (`--json`)

```json
{
  "base": "v3",
  "target": "current",
  "changes": [
    {
      "path": "src/auth.ts",
      "type": "modified",
      "additions": 47,
      "deletions": 12
    },
    {
      "path": "src/newfile.ts",
      "type": "added",
      "additions": 120
    }
  ],
  "summary": {
    "modified": 1,
    "added": 1,
    "deleted": 0
  }
}
```

---

## Implementation Plan

### Phase 1: Basic Diff

1. `agentfs diff v3` — compare checkpoint vs CWD
2. Mount checkpoint temporarily
3. Walk and compare by mtime/size
4. List changed files

### Phase 2: Content Diff

1. `agentfs diff v3 -- src/auth.ts` — show actual diff
2. Use native `diff -u` for text files
3. Handle binary files gracefully

### Phase 3: Polish

1. `agentfs diff v3 v5` — checkpoint to checkpoint
2. `--stat`, `--name-only` flags
3. JSON output
4. Pager integration

---

## Not In Scope

- **Merge:** Combining changes from divergent checkpoints
- **Patch:** Generating patch files for application
- **Three-way diff:** Comparing with common ancestor
- **Content hashing:** Not needed for mount-and-compare

These can be added later if needed.

---

## Summary

The mount-and-compare approach is simple and efficient:

1. **Zero checkpoint overhead** — no hashing during checkpoint
2. **~350-650ms diff time** — acceptable for interactive use
3. **Uses native tools** — `diff`, `stat`, standard POSIX
4. **APFS-optimized** — temp bundles use reflinks, no extra space

This replaces the current band-level diff with true file-level comparison.
