# Manage Specification

> Convert existing directories to/from agentfs management

---

## Overview

Two new commands to seamlessly convert directories:

```bash
agentfs manage <dir>     # Convert existing dir to agentfs-managed
agentfs unmanage [dir]   # Convert back to regular directory
```

**Safety principle:** Never let go of one hold before grabbing the next. Original data is always backed up until explicitly cleaned up or conversion is verified.

---

## Command: `agentfs manage <dir>`

### Usage

```bash
agentfs manage myapp
agentfs manage ./path/to/myapp
agentfs manage --cleanup myapp   # Remove backup after verification
```

### Flow

```
BEFORE:
myapp/                    # Regular directory with files
  ├── src/
  ├── package.json
  └── ...

DURING:
myapp/                    # Original (still intact)
myapp.fs/                 # New store being created
~/.agentfs/backups/xxx/   # Will hold backup

AFTER:
myapp.fs/                 # Store with data
myapp/                    # Mount point (contains files)
  ├── .agentfs            # Context file
  ├── src/
  └── ...
~/.agentfs/backups/xxx/   # Backup of original
```

### Algorithm

```
1. VALIDATE
   - dir exists and is a directory
   - dir.fs/ does not exist (not already managed)
   - dir is not inside an agentfs mount (check for .agentfs in parents)
   - ~/.agentfs/backups/ has no existing backup for this path

2. CREATE STORE
   - Create dir.fs/ with sparse bundle structure
   - Mount at temporary location: /tmp/agentfs-manage-xxx/

3. COPY DATA
   - rsync -a --delete dir/ → temp mount
   - Show progress for large directories

4. VERIFY
   - Count files in source and destination
   - Compare total sizes
   - If mismatch → abort, cleanup temp, leave original

5. BACKUP ORIGINAL
   - Generate backup ID (hash of original path + timestamp)
   - Move dir/ → ~/.agentfs/backups/<id>/
   - Record metadata: original path, timestamp, size

6. MOUNT
   - Unmount temp location
   - Mount dir.fs/ at dir/
   - Write .agentfs context file

7. REGISTER
   - Add to registry for auto-remount

8. REPORT
   - "Converted myapp/ to agentfs"
   - "Original backed up to ~/.agentfs/backups/<id>/"
   - "Backup size: 1.2 GB"
   - "Run 'agentfs manage --cleanup myapp' after verifying"
```

### Cleanup

```bash
agentfs manage --cleanup myapp
```

```
1. Find backup for myapp in ~/.agentfs/backups/
2. Confirm: "Delete backup (1.2 GB)? [y/N]"
3. rm -rf ~/.agentfs/backups/<id>/
4. "Backup deleted"
```

### Error Cases

| Condition | Error |
|-----------|-------|
| dir doesn't exist | "Directory not found: myapp" |
| dir.fs/ exists | "Already managed (myapp.fs exists)" |
| Inside agentfs mount | "Cannot manage directory inside agentfs mount" |
| Backup exists | "Previous backup exists. Run --cleanup first or --force to overwrite" |
| Disk full | "Not enough space. Need X GB free" |
| Copy verification fails | "Verification failed: file count mismatch. Original unchanged." |

---

## Command: `agentfs unmanage [dir]`

### Usage

```bash
agentfs unmanage           # From inside mount, uses context
agentfs unmanage myapp     # Explicit directory
```

### Flow

```
BEFORE:
myapp.fs/                 # Store
myapp/                    # Mount point
  ├── .agentfs
  ├── src/
  └── ...

AFTER:
myapp/                    # Regular directory (no longer mounted)
  ├── src/
  └── ...
(myapp.fs/ deleted)
```

### Algorithm

```
1. VALIDATE
   - Resolve store (from arg or context)
   - Store must be mounted

2. CONFIRM
   - Count checkpoints
   - "This will delete myapp.fs/ and all 5 checkpoints."
   - "Your files will be preserved as a regular directory."
   - "Continue? [y/N]"

3. COPY OUT
   - Create temp dir: /tmp/agentfs-unmanage-xxx/
   - rsync -a --delete mount/ → temp (excluding .agentfs file)

4. VERIFY
   - Compare file counts and sizes (excluding .agentfs)

5. UNMOUNT
   - hdiutil detach mount/
   - Remove mount point directory

6. RESTORE
   - Move temp → original location (dir/)

7. DELETE STORE
   - rm -rf dir.fs/

8. UNREGISTER
   - Remove from registry

9. REPORT
   - "Converted myapp back to regular directory"
   - "Deleted myapp.fs/ and 5 checkpoints"
```

### Error Cases

| Condition | Error |
|-----------|-------|
| Not managed | "Not an agentfs-managed directory" |
| Not mounted | "Store not mounted. Mount first or delete with 'agentfs delete'" |
| Copy fails | "Failed to copy files. Store unchanged." |

---

## Backup Storage

### Location

```
~/.agentfs/
├── registry.db           # Store registry
└── backups/              # Manage backups
    ├── index.json        # Backup metadata
    └── <id>/             # Backup contents
        └── ...           # Original files
```

### Backup Metadata (index.json)

```json
{
  "backups": [
    {
      "id": "a1b2c3d4",
      "original_path": "/Users/me/projects/myapp",
      "store_path": "/Users/me/projects/myapp.fs",
      "created_at": "2026-01-28T02:30:00Z",
      "size_bytes": 1234567890
    }
  ]
}
```

### Backup ID Generation

```go
func generateBackupID(originalPath string) string {
    h := sha256.New()
    h.Write([]byte(originalPath))
    h.Write([]byte(time.Now().Format(time.RFC3339Nano)))
    return hex.EncodeToString(h.Sum(nil))[:8]
}
```

### Cross-Device Handling

If ~/.agentfs/ is on a different device than the source:
- Copy instead of move (slower but works)
- Warn user about potential slowness for large directories

---

## Progress Reporting

For large directories, show progress:

```
Copying files...
  12,456 / 36,789 files (33%)
  1.2 GB / 3.8 GB

Verifying...
  Files: 36,789 ✓
  Size: 3.8 GB ✓

Moving original to backup...
  Done

Mounting store...
  Mounted at ./myapp/

✓ Converted myapp/ to agentfs
  Backup: ~/.agentfs/backups/a1b2c3d4/ (3.8 GB)
  Run 'agentfs manage --cleanup myapp' after verifying
```

---

## E2E Test Scenarios

### Basic Manage

```bash
# Setup
mkdir -p /tmp/test-manage/myapp
echo "hello" > /tmp/test-manage/myapp/file.txt
mkdir /tmp/test-manage/myapp/subdir
echo "world" > /tmp/test-manage/myapp/subdir/nested.txt

# Test
cd /tmp/test-manage
agentfs manage myapp

# Verify
[ -d myapp.fs ] || echo "FAIL: store not created"
[ -f myapp/.agentfs ] || echo "FAIL: not mounted"
[ -f myapp/file.txt ] || echo "FAIL: file missing"
[ -f myapp/subdir/nested.txt ] || echo "FAIL: nested file missing"
cat myapp/file.txt | grep -q "hello" || echo "FAIL: content wrong"

# Verify backup exists
ls ~/.agentfs/backups/ | grep -q . || echo "FAIL: no backup"

# Cleanup
agentfs manage --cleanup myapp
ls ~/.agentfs/backups/ | grep -q . && echo "FAIL: backup not cleaned"
```

### Manage with Symlinks

```bash
# Setup
mkdir -p /tmp/test-symlink/myapp
echo "target" > /tmp/test-symlink/myapp/real.txt
ln -s real.txt /tmp/test-symlink/myapp/link.txt

# Test
cd /tmp/test-symlink
agentfs manage myapp

# Verify symlink preserved
[ -L myapp/link.txt ] || echo "FAIL: symlink not preserved"
[ "$(readlink myapp/link.txt)" = "real.txt" ] || echo "FAIL: symlink target wrong"
```

### Manage with Permissions

```bash
# Setup
mkdir -p /tmp/test-perms/myapp
echo "secret" > /tmp/test-perms/myapp/secret.txt
chmod 600 /tmp/test-perms/myapp/secret.txt

# Test
cd /tmp/test-perms
agentfs manage myapp

# Verify permissions preserved
[ "$(stat -f %Lp myapp/secret.txt)" = "600" ] || echo "FAIL: permissions not preserved"
```

### Unmanage

```bash
# Setup (from previous manage test)
cd /tmp/test-manage
agentfs checkpoint "before unmanage"

# Test
agentfs unmanage myapp <<< "y"

# Verify
[ ! -d myapp.fs ] || echo "FAIL: store not deleted"
[ ! -f myapp/.agentfs ] || echo "FAIL: still has .agentfs"
[ -f myapp/file.txt ] || echo "FAIL: file missing"
cat myapp/file.txt | grep -q "hello" || echo "FAIL: content wrong"
```

### Already Managed

```bash
cd /tmp/test-manage
agentfs manage myapp 2>&1 | grep -q "Already managed" || echo "FAIL: should reject"
```

### Interrupted Manage (Simulated)

```bash
# This would require injecting failures, but conceptually:
# 1. Start manage
# 2. Kill during copy
# 3. Verify original is intact
# 4. Verify no partial state left behind
```

---

## Summary

| Command | Action | Safety |
|---------|--------|--------|
| `manage <dir>` | Convert to agentfs | Backup in ~/.agentfs/backups/ |
| `manage --cleanup <dir>` | Delete backup | Requires confirmation |
| `unmanage [dir]` | Convert back | y/N confirmation, copies out first |
