# Sparse Bundle Spike Findings

**Date:** 2026-01-27
**Environment:** macOS 15.7.1, APFS

## Overview

This document captures findings from investigating macOS sparse bundle internals for use in AgentFS incremental sync.

## Sparse Bundle Structure

A `.sparsebundle` is a directory containing:

```
test.sparsebundle/
├── Info.plist       # Bundle metadata (band size, total size)
├── Info.bckup       # Backup of Info.plist
├── bands/           # Directory containing numbered band files
│   ├── 0
│   ├── 1
│   ├── 7f
│   └── ...
├── lock             # Lock file for mount coordination
└── token            # Token file
```

### Info.plist Contents

```xml
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
    <key>band-size</key>
    <integer>4194304</integer>          <!-- 4MB in bytes -->
    <key>size</key>
    <integer>1073741824</integer>       <!-- 1GB total size -->
    <key>bundle-backingstore-version</key>
    <integer>1</integer>
    <key>diskimage-bundle-type</key>
    <string>com.apple.diskimage.sparsebundle</string>
</dict>
</plist>
```

### Band Naming

- Bands are named in **hexadecimal** (0, 1, 2, ..., 7f, 80, ..., ff, 100, ...)
- Band files are sparse - only allocated portions consume disk space
- Maximum band size is defined in Info.plist (e.g., 4MB = 4,194,304 bytes)

## Creating Sparse Bundles

```bash
# Create 1GB sparse bundle with 4MB bands
hdiutil create -size 1g -type SPARSEBUNDLE -fs APFS \
  -imagekey sparse-band-size=8192 \
  -volname testfs /path/to/test.sparsebundle

# Note: sparse-band-size is in 512-byte SECTORS
# 8192 sectors × 512 bytes = 4,194,304 bytes = 4MB
```

### Band Size Calculations

| Desired Band Size | sparse-band-size Value |
|------------------|------------------------|
| 1 MB             | 2048                   |
| 4 MB             | 8192                   |
| 8 MB             | 16384                  |
| 16 MB            | 32768                  |
| 64 MB            | 131072                 |

## Observed Behavior

### 1. Band Allocation is Lazy

Bands are created **only when data is written** to the corresponding disk region.

**Initial state after creation (no user data):**
```
bands/
├── 0   (3.4 MB) - Filesystem metadata
├── 7f  (4 MB)   - Volume metadata region
├── 80  (32 KB)  - Volume metadata region
└── ff  (4 MB)   - Volume metadata region
```

These initial bands contain APFS volume structures, not user data.

### 2. Data Spreads Contiguously

Writing a 10MB file allocates bands contiguously:

```
Before: 4 bands (0, 7f, 80, ff)
After:  7 bands (0, 1, 2, 3, 7f, 80, ff)

New bands created:
- Band 0: Filled to 4MB (was partial)
- Band 1: 4MB
- Band 2: 4MB
- Band 3: 1.4MB (partial - end of file data)
```

### 3. Write Operations Touch Multiple Bands

**Key Finding:** Even a single-byte modification typically changes 2-3 bands:

| Band | Purpose | Changes on... |
|------|---------|---------------|
| Band 0 | Filesystem metadata (inodes, directory entries) | Every file write (mtime updates) |
| Data band | Contains actual file content | When data in that region changes |
| Journal band (typically last allocated) | APFS transaction journal | Every write operation |

**Example: Modifying 1 byte at offset 5MB in a 10MB file:**
- Band 0: Changed (file mtime updated)
- Band 1: Unchanged
- Band 2: Changed (contains the modified byte)
- Band 3: Changed (journal transaction)
- Bands 7f, 80, ff: Unchanged (volume metadata)

### 4. Hash-Based Change Detection Works

SHA256 hashing reliably detects changed bands:

```bash
# Hash all bands
for f in test.sparsebundle/bands/*; do
  shasum -a 256 "$f"
done
```

**Considerations:**
- Expect 2-3 bands to change per write operation
- Band 0 changes on nearly every operation (metadata)
- Journal band (varies) changes on every operation
- Only sync the bands whose hashes differ

### 5. mtime Has Nanosecond Precision

Band file mtimes have nanosecond granularity:

```bash
stat -f "%Fm" bands/0
# Output: 1769566926.091853016
```

This enables fast change detection without hashing:
1. Compare mtime first (fast)
2. Hash only bands with changed mtime (accurate)

## Gotchas and Edge Cases

### 1. Journal Band Movement

The APFS journal doesn't have a fixed location. As data grows, the journal may relocate. This means:
- Can't hardcode "ignore band 3" as journal
- Must track which bands changed, even if it's journal-only

### 2. Band 0 Always Changes

Band 0 contains filesystem metadata and changes on every file operation. For sync purposes:
- Always include band 0 in change sets
- Consider this the "metadata band"

### 3. Unmount Required for Consistency

Reading bands while mounted may show inconsistent state due to:
- Write caching
- Journal not flushed

**Recommendation:** Unmount before syncing bands, or use `sync` + short delay.

### 4. Sparse Files Within Bands

Band files themselves can be sparse (holes). The on-disk size may be smaller than the logical size:

```bash
# Logical size vs actual disk usage
ls -la bands/0      # Shows 4194304 bytes
du -h bands/0       # May show less (actual blocks)
```

### 5. Hex Naming Requires Sorting

Band names are hex strings without leading zeros:
- Sorted alphabetically: 0, 1, 10, 2, 3, ...
- Must convert to int for proper ordering

```python
sorted(bands, key=lambda x: int(x, 16))
```

## Recommendations for AgentFS

### 1. Change Detection Strategy

```
Primary: mtime comparison (fast)
Secondary: SHA256 hash (definitive)

1. List all bands, get mtimes
2. Compare with last sync state
3. Hash only bands with changed mtime
4. Sync bands with different hash
```

### 2. Band Size Selection

| Use Case | Recommended Band Size |
|----------|----------------------|
| Frequent small changes | 1-4 MB (more granular sync) |
| Large file storage | 8-64 MB (fewer bands to track) |
| General purpose | 4 MB (balanced) |

### 3. Sync Protocol

```
1. Unmount sparse bundle (or sync + wait)
2. Snapshot band state (mtime, size, hash)
3. Transfer changed bands to backup
4. On restore: copy bands + Info.plist
```

### 4. Metadata to Track

Per sparse bundle:
- `Info.plist` contents (band size, total size)
- Last sync timestamp

Per band:
- Band name (hex)
- Size (bytes)
- mtime (nanoseconds)
- SHA256 hash

## Code Examples

### List Bands with Metadata

```bash
BUNDLE="test.sparsebundle"
for band in "$BUNDLE/bands"/*; do
  name=$(basename "$band")
  size=$(stat -f%z "$band")
  mtime=$(stat -f%Fm "$band")
  hash=$(shasum -a 256 "$band" | cut -d' ' -f1)
  echo "$name,$size,$mtime,$hash"
done
```

### Python: Band Change Detection

```python
import os
import hashlib
from pathlib import Path

def get_band_state(bundle_path: Path) -> dict:
    """Get current state of all bands."""
    bands_dir = bundle_path / "bands"
    state = {}
    for band in bands_dir.iterdir():
        stat = band.stat()
        with open(band, 'rb') as f:
            hash = hashlib.sha256(f.read()).hexdigest()
        state[band.name] = {
            'size': stat.st_size,
            'mtime': stat.st_mtime_ns,
            'hash': hash
        }
    return state

def find_changed_bands(old_state: dict, new_state: dict) -> set:
    """Find bands that have changed between states."""
    changed = set()

    # New or modified bands
    for name, info in new_state.items():
        if name not in old_state:
            changed.add(name)
        elif old_state[name]['hash'] != info['hash']:
            changed.add(name)

    # Deleted bands
    for name in old_state:
        if name not in new_state:
            changed.add(name)

    return changed
```

### Mount/Unmount Helpers

```bash
mount_bundle() {
  hdiutil attach "$1" -mountpoint "$2"
}

unmount_bundle() {
  hdiutil detach "$1"
}

# With error handling
safe_unmount() {
  local mount_point="$1"
  local retries=3
  while [ $retries -gt 0 ]; do
    if hdiutil detach "$mount_point" 2>/dev/null; then
      return 0
    fi
    sleep 1
    retries=$((retries - 1))
  done
  # Force unmount as last resort
  hdiutil detach "$mount_point" -force
}
```

## Questions Answered

| Question | Answer |
|----------|--------|
| Are bands created lazily? | **Yes** - only when data is written to that region |
| Does modifying 1 byte touch 1 or multiple bands? | **Multiple** - typically 2-3 (data + metadata + journal) |
| What's the mtime granularity? | **Nanoseconds** - sufficient for change detection |
| How does the journal affect band changes? | **Journal band changes on every write** - expect extra band changes |

## Next Steps

1. Implement band state tracking in Python
2. Test with real-world file patterns (git repos, documents)
3. Measure sync efficiency with different band sizes
4. Investigate rsync integration for band transfer
