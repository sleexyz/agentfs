# Two-Layer APFS Architecture

**Date:** 2026-01-28

AgentFS uses a two-layer APFS architecture that enables instant checkpoints without garbage collection.

---

## The Two Layers

```
┌─────────────────────────────────────────────────────────────────────────────┐
│  LAYER 1: HOST APFS (your Mac's filesystem)                                 │
│                                                                             │
│  ~/.agentfs/stores/myapp/                                                   │
│  ├── myapp.sparsebundle/                                                    │
│  │   └── bands/              ← Raw chunks of the INNER filesystem          │
│  │       ├── 0               ← 8MB opaque binary data                      │
│  │       ├── 1               ← 8MB opaque binary data                      │
│  │       └── ...             ← ~124 bands for 13k file project             │
│  │                                                                          │
│  └── checkpoints/                                                           │
│      ├── v1/                 ← APFS clone of bands/ (HOST-level COW)       │
│      ├── v2/                 ← Another clone                               │
│      └── v3/                 ← ...                                         │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    │ hdiutil attach
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│  LAYER 2: INNER APFS (mounted sparse bundle volume)                         │
│                                                                             │
│  ~/projects/myapp/           ← Mount point, where you work                  │
│  ├── src/app.ts              ← Actual project files                        │
│  ├── node_modules/           ← 13k files                                   │
│  ├── package.json                                                          │
│  └── .git/                                                                  │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Layer 1: Host APFS

- Your Mac's main filesystem
- Stores the sparse bundle and checkpoints
- COW operations happen here (cloning bands/)
- **Unit of storage:** Bands (8MB chunks)

### Layer 2: Inner APFS

- The filesystem INSIDE the mounted sparse bundle
- Where you actually work with files
- Standard APFS volume with normal file operations
- **Unit of storage:** Files and directories

---

## How Instant Checkpoints Work

### The Problem with Direct File Cloning

If we cloned files directly (like claude-snap does):

```
Clone 13,567 files → 13,567 APFS clone operations → ~1,700ms
```

APFS COW is O(inodes), not O(bytes). Many files = slow.

### The Sparse Bundle Solution

Sparse bundles collapse files into bands:

```
13,567 files in INNER APFS
        ↓ stored as
~124 bands in HOST APFS
```

Now checkpoint is:

```
Clone 124 bands → 124 APFS clone operations → ~70ms
```

### Why This Works

1. **Bands are just files** on the HOST APFS
2. **APFS clones bands instantly** via copy-on-write
3. **Each checkpoint** is a COW clone of the bands/ directory
4. **The INNER APFS is frozen** because its underlying storage (bands) is frozen

```bash
# Checkpoint creation
/bin/cp -Rc bands/ checkpoints/v1/

# This creates COW references:
# - v1/0 shares blocks with bands/0
# - v1/1 shares blocks with bands/1
# - etc.
```

---

## How We Avoid Garbage Collection

### Traditional Content-Addressed Systems Need GC

Systems like Git or JuiceFS use content-addressed storage:

```
objects/
├── sha256:abc123 (referenced by v1, v2)
├── sha256:def456 (referenced by v2 only)
└── sha256:xyz789 (orphaned - needs GC!)
```

They need garbage collection to:
1. Track which objects are referenced
2. Delete orphaned objects
3. Handle reference counting

### AgentFS Delegates to APFS

We don't manage object references. APFS does it for us:

```
checkpoints/
├── v1/          ← Full clone of bands at time T1
│   ├── 0
│   ├── 1
│   └── ...
├── v2/          ← Full clone of bands at time T2
│   ├── 0
│   ├── 1
│   └── ...
```

**Key insight:** Each checkpoint is a complete, self-contained copy (via COW). There are no shared references to manage.

### When You Delete a Checkpoint

```bash
rm -rf checkpoints/v1/
```

What happens:
1. APFS decrements refcount on blocks used by v1/
2. If refcount → 0, blocks are freed
3. If other checkpoints share those blocks, they remain

**APFS handles the reference counting automatically.** We just delete directories.

### Comparison

| Aspect | Content-Addressed (Git-style) | AgentFS (APFS COW) |
|--------|------------------------------|-------------------|
| Storage | Deduplicated objects | COW clones |
| References | Explicit (manifests) | Implicit (APFS) |
| Deletion | Mark orphans, GC later | Just rm -rf |
| Complexity | Need GC logic | None |
| Dedup scope | Global (all objects) | Per-clone (blocks) |

---

## The Tradeoff: Reading Old Checkpoints

### What We Gain

- Instant checkpoints (~70ms)
- No garbage collection
- Simple implementation (just cp -Rc and rm -rf)

### What We Lose

Checkpoints store **bands**, not **files**. Bands are opaque:

```bash
# Can't do this - bands are binary chunks of APFS
cat checkpoints/v1/0
# Output: Binary garbage

# Must do this - mount the bands as a sparse bundle
hdiutil attach -mountpoint /tmp/v1 <sparse-bundle-with-v1-bands>
cat /tmp/v1/src/app.ts
# Output: Actual file contents
```

### Implications for Diff

| Diff Type | How It Works | Speed |
|-----------|--------------|-------|
| CWD vs checkpoint | CWD is mounted, checkpoint needs mounting | ~500ms |
| Checkpoint vs checkpoint | Both need mounting | ~1000ms |

To diff without mounting, we'd need to store file metadata separately (Phase 2).

---

## Summary

```
                    HOST APFS                      INNER APFS
                    (bands)                        (files)
                       │                              │
Checkpoint:    cp -Rc bands/ v1/              (frozen via bands)
                       │                              │
Clone speed:   O(bands) = ~70ms               N/A (not cloned directly)
                       │                              │
Delete:        rm -rf v1/                     N/A
                       │                              │
GC needed?     No (APFS refcount)             No
                       │                              │
Read files:    Can't (opaque)                 Must mount checkpoint
```

The two-layer architecture is why agentfs can checkpoint 13k files in 70ms without implementing garbage collection — we leverage APFS COW at the band level and let APFS manage block references automatically.
