# AgentFS

Instant checkpoint and restore for agentic workflows on macOS.

AgentFS uses a two-layer APFS architecture to checkpoint entire projects in ~20ms and restore in ~500ms — regardless of project size.

```
┌─────────────────────────────────────────────────────────┐
│  Layer 1: Host APFS                                     │
│                                                         │
│  myproject.fs/                                          │
│  ├── data.sparsebundle/bands/   ← ~100 bands (8MB each) │
│  └── checkpoints/                                       │
│      ├── v1/                    ← COW clone of bands    │
│      └── v2/                    ← COW clone of bands    │
└───────────────────────────┬─────────────────────────────┘
                            │ mount
                            ▼
┌─────────────────────────────────────────────────────────┐
│  Layer 2: Inner APFS (where you work)                   │
│                                                         │
│  myproject/                                             │
│  ├── src/                                               │
│  ├── node_modules/          ← 36k files                 │
│  └── .git/                                              │
└─────────────────────────────────────────────────────────┘
```

Checkpoints clone ~100 bands instead of 36k files. APFS handles deduplication automatically — no garbage collection needed. See [how it works](knowledge/two-layer-apfs.md) for details.

## Features

- **Instant checkpoints** — ~20ms for any project size (36k files tested)
- **Fast restore** — ~500ms to roll back to any checkpoint
- **Zero copy** — APFS copy-on-write means checkpoints use no extra disk space until files diverge
- **Works with everything** — node_modules, .git, symlinks, any files

## Installation

Requires macOS (uses APFS reflinks and sparse bundles).

```bash
# Clone and build
git clone https://github.com/sleexyz/agentfs.git
cd agentfs
go build -o agentfs ./cmd/agentfs

# Or with nix
nix develop
go build -o agentfs ./cmd/agentfs
```

## Quick Start

Convert an existing project to agentfs:

```bash
# Convert existing directory to agentfs management
agentfs manage myproject

# Creates myproject.fs/ store, backs up original to ~/.agentfs/backups/
# Mounts store at original location — your paths stay the same

cd myproject

# Create checkpoints as you work
agentfs checkpoint create "before refactor"

# Make changes, then restore if needed
agentfs restore v1

# After verifying everything works, clean up the backup
agentfs manage --cleanup myproject
```

Or start a new empty project:

```bash
agentfs init myproject
cd myproject
```

## Commands

### Store Management

```
agentfs manage <dir>          Convert existing directory to agentfs
agentfs manage --cleanup <dir>  Remove backup after verification
agentfs unmanage [dir]        Convert back to regular directory

agentfs init <name>           Create a new empty store
agentfs mount [name]          Mount a store (or --all for all stores)
agentfs list                  List all stores
agentfs delete <name>         Delete store and all checkpoints
```

### Checkpoints

```
agentfs checkpoint create [msg]   Create a checkpoint (~20ms)
agentfs checkpoint list           List all checkpoints
agentfs checkpoint info <ver>     Show checkpoint details
agentfs checkpoint delete <ver>   Delete a checkpoint
```

### Restore & Diff

```
agentfs restore <version>     Restore to a checkpoint (~500ms)
agentfs diff <v1> [v2]        Show changes between checkpoints
agentfs diff v3               Diff checkpoint v3 against current state
agentfs diff v1 v3            Diff between two checkpoints
agentfs diff v3 -- src/       Diff specific path
```

### Service (Auto-Remount)

```
agentfs service install       Install LaunchAgent for auto-remount on login
agentfs service uninstall     Remove the LaunchAgent
agentfs service status        Show service status
```

## How It Works

Your project lives in a mounted sparse bundle. Checkpoints clone the sparse bundle's bands (not individual files) using APFS copy-on-write. This makes checkpoints O(bands) instead of O(files).

See [knowledge/two-layer-apfs.md](knowledge/two-layer-apfs.md) for the full architecture.

## Context System

AgentFS uses a `.agentfs` file (like `.git`) to know which store you're working with. This file is created automatically by `agentfs manage` or `agentfs init`.

```bash
# Commands auto-detect the store from .agentfs
cd myproject
agentfs checkpoint create "wip"
agentfs restore v1

# Or use --store explicitly
agentfs --store myproject checkpoint list
```

## Claude Code Integration

AgentFS integrates with Claude Code hooks for automatic checkpointing after file edits. See `agentfs checkpoint create --help` for hook-friendly flags.

## Performance

| Operation | Time |
|-----------|------|
| Checkpoint create | ~20ms |
| Restore | ~500ms |
| Mount | ~200ms |
| Unmount | ~100ms |

Tested with a 36k file Next.js project including node_modules.

## FAQ

### Does AgentFS require a daemon?

No. AgentFS is a pure CLI tool — each command runs, does its work, and exits. For auto-remount on login, install the optional LaunchAgent with `agentfs service install`.

### Do checkpoints persist across reboots?

Yes. The sparse bundle, checkpoints, and metadata are just files on disk. However, stores are **unmounted** after reboot. Run `agentfs service install` to auto-remount on login, or manually mount with `agentfs mount <name>`.

### What happens to my original files with `agentfs manage`?

Your original directory is safely backed up to `~/.agentfs/backups/` before any changes. The backup persists until you explicitly run `agentfs manage --cleanup` after verifying the conversion worked.

### Should I exclude stores from Time Machine?

Yes. AgentFS checkpoints are your version history — backing them up with Time Machine is redundant and can cause significant storage bloat (Time Machine would back up every band version). Exclude stores with:

```bash
tmutil addexclusion -p myproject.fs/
```

### Why sparse bundles instead of direct file cloning?

Speed. A 36k file project compresses to ~100-150 bands. Cloning 150 bands with APFS reflinks takes ~20ms. Cloning 36k files directly would take ~1700ms. The sparse bundle acts as an aggregation layer that makes checkpoint operations O(bands) instead of O(files).

## License

MIT
