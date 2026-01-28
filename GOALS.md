# AgentFS Goals

> Instant checkpoint and restore for macOS projects.

---

## North Star

**Make version control invisible for agent workflows.**

Agents checkpoint before risky operations, restore when things go wrong — without the user waiting or configuring anything.

---

## Status: Phase 1 Complete

### What's Working

- **Checkpoint:** ~60-80ms for any project size (13k+ files tested)
- **Restore:** ~500-1000ms (limited by hdiutil mount/unmount)
- **CLI:** init, open, close, delete, list, use, status, checkpoint, restore, diff
- **Real projects:** Works with node_modules, .git, etc.

### Key Insight

Sparse bundles collapse thousands of files into ~100 bands. APFS reflinks on bands = O(bands) instead of O(files).

```
13,567 files → 124 bands → checkpoint in 70ms
```

See `knowledge/agentfs-vs-claude-snap.md` for benchmarks against alternative approaches.

---

## Implementation Phases

### Phase 1: Core MVP ✅ COMPLETE
- [x] Sparse bundle store management
- [x] Instant checkpoint via band cloning
- [x] Fast restore via band swap
- [x] Context system (.agentfs file)
- [x] SQLite metadata
- [x] CLI with all core commands

### Phase 2: Causality & Hooks (Next)
- [ ] Track agent/action/session per checkpoint
- [ ] Claude Code hook integration
- [ ] CLI: blame, timeline commands

### Phase 3: Polish
- [x] Global registry (`~/.agentfs/registry.db`)
- [x] Auto-remount on login (LaunchAgent service)
- [x] `mount --all` / `unmount --all`
- [ ] `brew install agentfs`
- [ ] Shell completions
- [ ] Better error messages

### Phase N+1: Remote Sync (Future)
- [ ] Syncthing integration (file-level)
- [ ] Multi-machine support

---

## Out of Scope

- Windows/Linux (macOS-only: APFS + sparse bundles)
- GUI application
- Daemon mode (CLI-first)
- Remote sync (deferred)
