# AgentFS Goals

> A filesystem layer that makes checkpointing instant, invisible, and analyzable.

---

## North Star

**Make version control disappear for agent workflows.**

Agents should be able to checkpoint, restore, and analyze changes without the user configuring anything or waiting for anything.

---

## Success Criteria

1. **Instant checkpoints** — <100ms for typical projects (10k files)
2. **Zero configuration** — Single `brew install` or DMG, no security dialogs
3. **Rich metadata** — Know *why* things changed, not just *what*
4. **Object store sync** — Durable, COW, content-addressed
5. **Seamless UX** — User works in normal directory, magic happens underneath

---

## Current Phase: Ready to Build

Research and design complete. Architecture validated.

**Completed:**
- ✅ Sparse bundle internals (Spike 1)
- ✅ Checkpoint performance ~20ms (Spike 2)
- ✅ APFS reflinks for instant snapshots
- ✅ Sync tool comparison → Syncthing
- ✅ Architecture decision → file-level sync (not band-level)
- ✅ Spec written → `specs/agentfs-daemon.md`

### Architecture Summary

```
Sparse bundles    → Checkpoint optimization (36k files → 100 bands)
APFS reflinks     → Instant snapshots (~20ms for any project)
Syncthing         → File-level sync (handles conflicts gracefully)
```

**Key insight:** Band-level sync would corrupt sparse bundles on conflict. File-level sync is safe.

### Research Completed

- [x] Sparse bundle internals → bands collapse files, enabling fast checkpoints
- [x] Checkpoint speed → ~20ms via APFS reflinks on bands
- [x] macOS constraints → no FUSE needed, sparse bundles are native
- [x] Sync architecture → file-level via Syncthing (not band-level)

---

## Implementation Phases

### Phase 1: Core MVP ← CURRENT
- Instant checkpoint (~20ms) and restore (<500ms)
- CLI: init, open, close, checkpoint, restore, log, diff
- Works with real projects (node_modules, .git)

### Phase 2: Causality & Analysis
- Track agent/action/session per checkpoint
- CLI: blame, timeline, search
- Claude Code hook integration

### Phase 3: Polish
- `brew install agentfs`
- Documentation
- Error recovery

### Phase N+1: Remote Sync (Future)
- Syncthing integration (file-level)
- Multi-machine support

---

## Out of Scope (For Now)

- Remote sync (deferred to Phase N+1)
- Windows/Linux support
- GUI application
- Daemon mode (CLI-first for MVP)
