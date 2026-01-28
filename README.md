# AgentFS

Instant checkpoint and restore for macOS projects.

AgentFS uses sparse bundles and APFS reflinks to create near-instant snapshots of your project state. Checkpoint in ~60ms, restore in ~500ms — regardless of project size.

## Features

- **Instant checkpoints** — ~60ms for any project size (36k files tested)
- **Fast restore** — ~500ms to roll back to any checkpoint
- **Zero copy** — APFS copy-on-write means checkpoints use no extra disk space until files diverge
- **Works with everything** — node_modules, .git, any files

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

```bash
# Create a new store (sparse bundle)
agentfs init myproject --mount ~/projects/myproject

# Work in the mounted directory
cd ~/projects/myproject
# ... make changes ...

# Create a checkpoint
agentfs checkpoint create "before refactor"

# Make more changes, then restore if needed
agentfs restore v1
```

## Commands

```
agentfs init <name>           Create and mount a new store
agentfs open <name>           Mount existing store
agentfs close [name]          Unmount store
agentfs delete <name>         Delete store and all checkpoints
agentfs list                  List all stores
agentfs use <name>            Set context for current directory
agentfs status                Show current store status

agentfs checkpoint create [msg]   Create a checkpoint (~60ms)
agentfs checkpoint list           List all checkpoints
agentfs checkpoint info <ver>     Show checkpoint details
agentfs checkpoint delete <ver>   Delete a checkpoint

agentfs restore <version>     Restore to a checkpoint (~500ms)
```

## How It Works

1. **Sparse bundles** — Your project lives inside a macOS sparse bundle (a directory that acts like a disk image). The sparse bundle stores data in 8MB "bands".

2. **APFS reflinks** — Checkpoints use `cp -c` (clone) which creates copy-on-write references. The checkpoint is instant because no data is actually copied.

3. **Band-level snapshots** — A 36k file project compresses to ~100 bands. Cloning 100 bands is much faster than cloning 36k files.

## Context System

AgentFS uses a `.agentfs` file (like `.git`) to know which store you're working with:

```bash
# Set context for current directory
agentfs use myproject

# Now commands work without --store flag
agentfs checkpoint create "wip"
agentfs status
```

Or use `--store` explicitly:

```bash
agentfs --store myproject checkpoint list
```

## Performance

| Operation | Time |
|-----------|------|
| Checkpoint create | ~60ms |
| Restore | ~500ms |
| Mount | ~200ms |
| Unmount | ~100ms |

Tested with a 36k file Next.js project including node_modules.

## License

MIT
