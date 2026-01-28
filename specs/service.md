# Service Specification

> Centralized registry and auto-remount on login

---

## Overview

Add a global registry to track stores and a launchd service to auto-remount them after reboot.

**Key components:**
1. **Global registry** — `~/.agentfs/registry.db` tracks all stores
2. **`agentfs mount --all`** — Mount all registered stores
3. **`agentfs service`** — Install/uninstall LaunchAgent for auto-remount

---

## Registry

### Location

```
~/.agentfs/
└── registry.db              # SQLite database
```

### Schema

```sql
CREATE TABLE stores (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    store_path TEXT NOT NULL UNIQUE,    -- /Users/me/projects/foo.fs
    mount_point TEXT NOT NULL,          -- /Users/me/projects/foo
    auto_mount INTEGER NOT NULL DEFAULT 1,  -- 1 = remount on login
    created_at INTEGER NOT NULL,
    last_mounted_at INTEGER
);
```

### Registration Lifecycle

**On `agentfs init`:**
```go
// After creating store, register it
registry.Register(storePath, mountPoint)
```

**On `agentfs delete`:**
```go
// Before deleting store, unregister it
registry.Unregister(storePath)
```

**On `agentfs mount`:**
```go
// Update last_mounted_at
registry.UpdateLastMounted(storePath)
```

---

## Commands

### `agentfs mount --all`

Mount all registered stores with `auto_mount = true`.

```bash
$ agentfs mount --all
Mounting foo.fs... done
Mounting bar.fs... done
Skipping baz.fs (not found)    # Store was deleted manually
Mounted 2 stores (1 skipped)
```

**Behavior:**
1. Query registry for all stores where `auto_mount = 1`
2. For each store:
   - If store path doesn't exist → skip silently, log warning
   - If already mounted → skip
   - Otherwise → mount
3. Print summary

### `agentfs unmount --all`

Unmount all currently mounted stores.

```bash
$ agentfs unmount --all
Unmounting foo... done
Unmounting bar... done
Unmounted 2 stores
```

### `agentfs service install`

Create and load a LaunchAgent for auto-remount at login.

```bash
$ agentfs service install
Creating LaunchAgent...
Loading service...
Service installed. Stores will auto-mount on login.
```

**Creates:** `~/Library/LaunchAgents/com.agentfs.mount.plist`

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.agentfs.mount</string>
    <key>ProgramArguments</key>
    <array>
        <string>/path/to/agentfs</string>
        <string>mount</string>
        <string>--all</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/tmp/agentfs-mount.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/agentfs-mount.log</string>
</dict>
</plist>
```

**Then runs:**
```bash
launchctl load ~/Library/LaunchAgents/com.agentfs.mount.plist
```

### `agentfs service uninstall`

Unload and remove the LaunchAgent.

```bash
$ agentfs service uninstall
Unloading service...
Removing LaunchAgent...
Service uninstalled.
```

**Runs:**
```bash
launchctl unload ~/Library/LaunchAgents/com.agentfs.mount.plist
rm ~/Library/LaunchAgents/com.agentfs.mount.plist
```

### `agentfs service status`

Show service status.

```bash
$ agentfs service status
Service: installed
LaunchAgent: ~/Library/LaunchAgents/com.agentfs.mount.plist
Registered stores: 3
  - /Users/me/projects/foo.fs (auto-mount: yes)
  - /Users/me/projects/bar.fs (auto-mount: yes)
  - /Users/me/projects/old.fs (auto-mount: no)
```

---

## Registry Management

### `agentfs registry list`

List all registered stores.

```bash
$ agentfs registry list
STORE                              MOUNT                           AUTO-MOUNT
/Users/me/projects/foo.fs          /Users/me/projects/foo          yes
/Users/me/projects/bar.fs          /Users/me/projects/bar          yes
```

### `agentfs registry remove <store>`

Remove a store from registry (doesn't delete the store itself).

```bash
$ agentfs registry remove foo.fs
Removed foo.fs from registry
```

### `agentfs registry clean`

Remove stale entries (stores that no longer exist on disk).

```bash
$ agentfs registry clean
Removed 2 stale entries:
  - /Users/me/old/deleted.fs
  - /Users/me/temp/gone.fs
```

---

## Implementation Notes

### Finding agentfs Binary Path

For the LaunchAgent, we need the absolute path to agentfs:

```go
func getAgentfsBinaryPath() (string, error) {
    // Get path of current executable
    exe, err := os.Executable()
    if err != nil {
        return "", err
    }
    return filepath.EvalSymlinks(exe)
}
```

### Registry Directory Creation

```go
func ensureRegistryDir() error {
    dir := filepath.Join(os.Getenv("HOME"), ".agentfs")
    return os.MkdirAll(dir, 0755)
}
```

### Stale Entry Handling

When mounting with `--all`, skip missing stores silently:

```go
for _, store := range stores {
    if !pathExists(store.StorePath) {
        log.Printf("Skipping %s (not found)", store.StorePath)
        continue
    }
    // ... mount
}
```

---

## Error Cases

### Service Already Installed

```bash
$ agentfs service install
Service already installed. Use --force to reinstall.

$ agentfs service install --force
Unloading existing service...
Creating LaunchAgent...
Loading service...
Service installed.
```

### No Stores Registered

```bash
$ agentfs mount --all
No stores registered. Use 'agentfs init' to create a store.
```

### Store Path Changed

If a store is moved, the registry entry becomes stale:

```bash
$ mv foo.fs /new/location/
$ agentfs mount --all
Skipping foo.fs (not found)
```

User should run `agentfs registry clean` or re-init at new location.

---

## Flags

| Command | Flag | Description |
|---------|------|-------------|
| `mount` | `--all` | Mount all registered stores |
| `unmount` | `--all` | Unmount all mounted stores |
| `service install` | `--force` | Reinstall even if exists |

---

## Summary

| Component | Location |
|-----------|----------|
| Registry database | `~/.agentfs/registry.db` |
| LaunchAgent plist | `~/Library/LaunchAgents/com.agentfs.mount.plist` |
| Mount log | `/tmp/agentfs-mount.log` |
