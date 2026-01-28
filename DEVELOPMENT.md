# AgentFS Development Guide

## Architecture

AgentFS uses a **frozen global binary** for stability while allowing local development:

```
Global (frozen, stable):
  /etc/profiles/per-user/slee2/bin/agentfs  ← Nix flake build
  Used by: hooks, service, CLI

Development:
  ~/projects/agentfs/                        ← source code
  ./agentfs-dev                              ← local test binary
```

This separation ensures that:
- Hooks and auto-checkpoint always use a stable version
- Breaking changes during development don't affect running systems
- You explicitly choose when to promote dev → global

---

## Quick Reference

```bash
just build        # Build local test binary
just run <args>   # Run via go run
just test         # Run E2E tests
just which        # Show global version info
just upgrade-now  # Promote to global (requires sudo)
```

---

## Development Workflow

### 1. Make Changes

Edit source code as usual. Test with the local binary:

```bash
just build
./agentfs-dev checkpoint list
./agentfs-dev manage ~/some/test/dir
```

Or use `go run` for quick iteration:

```bash
just run checkpoint list
just run manage ~/some/test/dir
```

### 2. Run Tests

```bash
just test
```

This runs the E2E test suite in `test/e2e/manage_test.sh`.

### 3. Commit & Push

```bash
git add -A
git commit -m "feat: add new feature"
git push
```

### 4. Promote to Global

When ready to update the global installation:

```bash
just upgrade-now
```

This:
1. Updates `~/config/flake.lock` to point to latest agentfs commit
2. Runs `darwin-rebuild switch` to install the new version

**Note:** Requires sudo for darwin-rebuild.

---

## How Global Installation Works

AgentFS is installed via Nix flakes:

```
~/config/flake.nix
  └── inputs.agentfs.url = "path:/Users/slee2/projects/agentfs"

~/config/nixpkgs/home.nix
  └── home.packages includes agentfs from flake input
```

The `flake.lock` pins a specific commit. Running `just upgrade-now` updates this pin.

---

## Hooks Configuration

Claude Code hooks use the global `agentfs` binary:

```json
// ~/.claude/settings.json
{
  "hooks": {
    "PostToolUse": [{
      "matcher": "Edit|Write|Bash",
      "hooks": [{
        "command": "agentfs checkpoint create --auto --from-hook 2>/dev/null || true"
      }]
    }]
  }
}
```

Since hooks use `agentfs` (not a hardcoded path), they automatically use whatever version is in PATH — the frozen Nix build.

---

## Dogfooding: AgentFS on AgentFS

The agentfs source directory can be managed by agentfs itself:

```bash
agentfs manage .
```

This is safe because:
1. The global binary lives in `/nix/store/...` (immutable)
2. Restoring checkpoints only affects source code, not the binary
3. Development uses `./agentfs-dev`, not the global binary

---

## Updating Dependencies

If you add/remove Go dependencies:

```bash
go mod tidy
```

Then update the Nix vendorHash:

```bash
# Set a fake hash to get the real one
# Edit flake.nix: vendorHash = "sha256-AAAA...";
nix build .#agentfs 2>&1 | grep "got:"
# Copy the correct hash back to flake.nix
```

---

## File Structure

```
cmd/agentfs/           # CLI commands
  ├── main.go
  ├── root.go
  ├── checkpoint.go
  ├── manage.go
  ├── unmanage.go
  ├── service.go
  └── registry.go

internal/              # Core packages
  ├── store/           # Sparse bundle management
  ├── checkpoint/      # Checkpoint operations
  ├── context/         # .agentfs context detection
  ├── registry/        # Global store registry
  ├── backup/          # Backup management for manage/unmanage
  └── db/              # SQLite operations

test/e2e/              # End-to-end tests
  └── manage_test.sh

specs/                 # Feature specifications
flake.nix              # Nix build definition
justfile               # Development commands
```

---

## Troubleshooting

### "agentfs: command not found" after upgrade

The shell might have cached the old path. Try:
```bash
hash -r  # Clear command hash table
which agentfs
```

### Nix build fails with vendorHash mismatch

Dependencies changed. Update the hash:
```bash
nix build .#agentfs 2>&1 | grep "got:"
# Update vendorHash in flake.nix
```

### Hooks not creating checkpoints

1. Check you're in an agentfs-managed directory (has `.agentfs` file)
2. Verify the global binary works: `agentfs checkpoint create --auto`
3. Check hook config: `cat ~/.claude/settings.json`
