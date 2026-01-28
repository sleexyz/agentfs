# Auto-Checkpoint Specification

> Automatic checkpointing via Claude Code hooks

---

## Overview

Enable automatic checkpointing when Claude Code makes changes, using a Stop hook that fires after each turn.

**Key principle:** agentfs detects changes internally — no external state or flags needed.

---

## Hook Configuration

```json
// ~/.claude/settings.json
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "agentfs checkpoint --auto 2>/dev/null || true",
            "async": true,
            "timeout": 30
          }
        ]
      }
    ]
  }
}
```

---

## Command: `agentfs checkpoint --auto`

### Behavior

1. **Detect context** — Find agentfs store from cwd (walk up looking for `.agentfs` file)
2. **Check if mounted** — If store not mounted, exit silently (code 0)
3. **Detect changes** — Compare current bands to last checkpoint
4. **Skip if unchanged** — Exit silently if no changes
5. **Create checkpoint** — If changed, create checkpoint with auto-generated message

### Flags

| Flag | Description |
|------|-------------|
| `--auto` | Enable auto-checkpoint mode (quiet, skip-if-unchanged) |

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success (checkpoint created, or skipped because unchanged/not in agentfs) |
| 1 | Error (store exists but checkpoint failed) |

### Output

**Normal mode:** Print checkpoint info
```
Created v5 "auto" (72ms)
```

**Auto mode (--auto):** Silent on success/skip, stderr on error

---

## Change Detection

### Approach: Compare bands directory

The bands directory (`foo.fs/data.sparsebundle/bands/`) reflects current filesystem state. Compare to last checkpoint's bands.

```go
func hasChanges(store *Store) (bool, error) {
    currentBands := filepath.Join(store.BundlePath, "bands")

    // Get last checkpoint
    lastCheckpoint := getLastCheckpoint(store)
    if lastCheckpoint == nil {
        // No previous checkpoint = always has changes
        return true, nil
    }

    lastBands := filepath.Join(store.StorePath, "checkpoints", lastCheckpoint.Name)

    // Compare directory contents
    return !dirsEqual(currentBands, lastBands), nil
}
```

### Comparison Strategy

**Option A: File count + total size**
- Fast O(1) comparison
- May miss some edge cases (file replaced with same-size file)

**Option B: List files and compare names + sizes**
- More accurate
- O(n) where n = number of bands (~100-150)

**Option C: Hash comparison**
- Most accurate
- Expensive for large bands

**Recommendation:** Option B — list band files, compare names and sizes. Fast enough for ~150 files, accurate enough for practical use.

```go
func dirsEqual(dir1, dir2 string) bool {
    entries1 := listDirWithSizes(dir1)
    entries2 := listDirWithSizes(dir2)

    if len(entries1) != len(entries2) {
        return false
    }

    for name, size1 := range entries1 {
        if size2, ok := entries2[name]; !ok || size1 != size2 {
            return false
        }
    }
    return true
}
```

---

## Auto-Generated Message

Format: `"auto"`

Simple, consistent. The timestamp is already in the checkpoint metadata.

Future enhancement: Include session ID or trigger context if available via environment variables.

---

## Context Detection

The `--auto` flag implies context detection from cwd:

```go
func findStoreFromCwd() (*Store, error) {
    // Walk up from cwd looking for .agentfs file
    cwd, _ := os.Getwd()

    for dir := cwd; dir != "/"; dir = filepath.Dir(dir) {
        agentfsFile := filepath.Join(dir, ".agentfs")
        if _, err := os.Stat(agentfsFile); err == nil {
            // Found it - read store path
            storePath, _ := os.ReadFile(agentfsFile)
            return storeManager.GetFromPath(strings.TrimSpace(string(storePath)))
        }
    }

    return nil, nil // Not in agentfs directory
}
```

---

## Edge Cases

### Not in agentfs directory
- `--auto` mode: Exit 0 silently
- Normal mode: Error "not in agentfs directory"

### Store not mounted
- `--auto` mode: Exit 0 silently (can't checkpoint unmounted store)
- Normal mode: Error "store not mounted"

### No previous checkpoint
- Always create checkpoint (first one)

### Rapid successive calls
- Each call compares to last checkpoint
- If multiple calls before bands change, only first creates checkpoint
- APFS COW handles any duplicates efficiently

### Concurrent access
- SQLite handles concurrent checkpoint creation
- Worst case: two identical checkpoints (harmless)

---

## Testing

### Manual Testing

```bash
# Setup
cd /tmp && mkdir test-auto && cd test-auto
agentfs init myapp
cd myapp

# Test 1: First checkpoint (no previous)
agentfs checkpoint --auto
agentfs checkpoint list  # Should show v1 "auto"

# Test 2: No changes - should skip
agentfs checkpoint --auto
agentfs checkpoint list  # Should still show only v1

# Test 3: Make changes
echo "hello" > test.txt
agentfs checkpoint --auto
agentfs checkpoint list  # Should show v1 and v2

# Test 4: Not in agentfs dir
cd /tmp
agentfs checkpoint --auto  # Should exit 0 silently
echo $?  # Should be 0
```

### Hook Testing

```bash
# Install hook
cat > ~/.claude/settings.json << 'EOF'
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "agentfs checkpoint --auto 2>/dev/null || true",
            "async": true,
            "timeout": 30
          }
        ]
      }
    ]
  }
}
EOF

# Start Claude Code in agentfs directory
cd ~/projects/myapp  # (agentfs managed)
claude

# Make some edits via Claude
# Check checkpoints after
agentfs checkpoint list
```

---

## Summary

| Aspect | Decision |
|--------|----------|
| Hook event | Stop (once per turn) |
| Change detection | Compare band files (names + sizes) |
| Message format | "auto" |
| Unchanged behavior | Skip silently |
| Not in agentfs | Exit 0 silently |
| Execution | Async (non-blocking) |
