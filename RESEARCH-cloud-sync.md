# Research: AgentFS + OpenSprite Unification

## Status: ✅ COMPLETE

**Conclusion: Decoupled layered architecture using existing tools.**

See [Conclusion](#conclusion-decoupled-layered-architecture) at the bottom for the final architecture.

---

## Original Goal

**Seamless local/cloud checkpointable volumes.**

A developer can:
1. Work locally with instant checkpoints (agentfs)
2. Push state to cloud for expensive operations or sharing (opensprite)
3. Pull state from cloud to any local machine
4. Collaborate in real-time across local/cloud boundaries

The filesystem is the primitive. Everything is files. Everything is checkpointable.

---

## Original Vision (Rejected)

We initially explored a unified block store approach:

```
┌─────────────────────────────────────────────────────────────────┐
│                    Common Block Store (S3)                       │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │  blocks/     sha256-xxx, sha256-yyy, ...                │    │
│  │  manifests/  checkpoint-v1.json, checkpoint-v2.json     │    │
│  └─────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────┘
            ▲                                   ▲
            │ push/pull                         │ native
            │                                   │
┌───────────┴───────────┐           ┌──────────┴────────────┐
│  AgentFS (macOS)      │           │  OpenSprite (Linux)   │
│                       │           │                       │
│  APFS sparse bundle   │           │  JuiceFS + ext4 img   │
│  for local speed      │           │  for cloud native     │
│                       │           │                       │
│  Local checkpoints:   │           │  Checkpoints:         │
│  APFS COW (20ms)      │           │  juicefs clone (350ms)│
└───────────────────────┘           └───────────────────────┘
```

**Why rejected:** Too complex. JuiceFS blocked on macOS. Custom interchange format unnecessary when git + syncthing already solve coordination.

---

## Biggest Open Questions

### 1. What's the common data representation?

AgentFS uses APFS sparse bundles (bands). OpenSprite uses JuiceFS chunks. These are incompatible.

Options:
- **A) JuiceFS everywhere** — Can JuiceFS run on macOS with acceptable perf?
- **B) Content-addressed blocks** — Define our own format both can speak
- **C) Portable disk image** — ext4 as interchange, native formats for local ops
- **D) File-level sync** — rsync files, rebuild native format on each side

### 2. Can JuiceFS run on macOS?

If yes with good performance, this might be the simplest path:
- AgentFS becomes a UX layer over JuiceFS on macOS
- OpenSprite already uses JuiceFS
- Instant interop

**Status: BLOCKED** — JuiceFS requires FUSE, and the options on macOS are problematic:

| Option | Requires Kernel Ext? | Status |
|--------|---------------------|--------|
| macFUSE (kext) | Yes, SIP reduction | ❌ Not acceptable |
| FUSE-T | No (userspace NFS) | ⚠️ JuiceFS doesn't support it yet |
| FSKit (Apple) | No (native) | 🔮 Future option (macOS 15.4+) |

See **Section: macOS FUSE Options** below for details.

### 3. What chunking algorithm should we use?

JuiceFS uses content-defined chunking (CDC). Options:
- **Use JuiceFS's chunking** — Maximum compat, but tied to JuiceFS
- **Use a standard like casync/restic** — More portable
- **Roll our own** — Maximum control, more work

### 4. How do we handle local-only optimizations?

AgentFS is fast because APFS COW on sparse bundle bands is ~20ms.

If we add a cloud sync layer:
- Local checkpoints stay fast (APFS COW)
- Cloud sync happens async in background?
- Or do we need a different architecture?

### 5. What's the sync model?

- **Push/pull (git-like)** — Explicit, user controls when
- **Continuous (Syncthing-like)** — Always in sync
- **Hybrid** — Local checkpoints instant, cloud sync on-demand

### 6. How do we handle conflicts?

If same volume is modified locally and in cloud:
- Last-write-wins?
- Fork on conflict?
- Manual resolution?

---

## macOS FUSE Options (Research Findings)

### Option A: macFUSE with Kernel Extension
**Verdict: ❌ Not acceptable**

- Requires SIP (System Integrity Protection) reduction on Apple Silicon
- Users must boot into recovery mode to enable
- Security-conscious users won't do this
- Apple actively discouraging kexts

### Option B: FUSE-T (Userspace via NFS Loopback)
**Verdict: ⚠️ Promising but JuiceFS doesn't support it**

**How it works:**
```
┌─────────────────────────────────────────────────────────┐
│  Your App (using libfuse API)                           │
└─────────────────┬───────────────────────────────────────┘
                  │ FUSE protocol
                  ▼
┌─────────────────────────────────────────────────────────┐
│  FUSE-T NFS Server (userspace daemon)                   │
│  - Translates FUSE calls → NFS v4 RPCs                  │
│  - Runs on localhost TCP port                           │
└─────────────────┬───────────────────────────────────────┘
                  │ NFS v4
                  ▼
┌─────────────────────────────────────────────────────────┐
│  macOS Native NFS Client (built into kernel)            │
│  - mount_nfs to localhost                               │
│  - Excellent performance, native caching                │
└─────────────────────────────────────────────────────────┘
```

**Pros:**
- Purely userspace — no kernel extension, no SIP reduction
- Drop-in replacement for macFUSE (same libfuse API)
- Actually faster than macFUSE in some benchmarks (good NFS client impl)
- Already works with: rclone mount, Cryptomator
- Install: `brew install macfuse` (now installs FUSE-T on modern macOS)

**Cons:**
- **JuiceFS doesn't support it yet** ([Issue #4285](https://github.com/juicedata/juicefs/issues/4285), "wishlist" priority)
- Blocker: JuiceFS uses custom go-fuse fork, would need merge with FUSE-T go-fuse
- Some FUSE features unsupported: IOCTL, BMAP, FALLOCATE
- File locks bypass FUSE (handled by macOS NFS client)
- READDIR must return all results in first call
- Attribute caching controlled by NFS client, ignores FUSE hints
- Can't set atime/mtime separately

**Bottom line:** FUSE-T is the right approach architecturally, but JuiceFS support is blocked on go-fuse library work.

### Option C: Apple FSKit (Native Userspace FS)
**Verdict: 🔮 Future option**

Apple's official answer to userspace filesystems, released in macOS 15.4 Sequoia (March 2025).

**What it is:**
- Native Apple framework for userspace filesystems
- No kernel extension, no SIP reduction, Full Security mode
- macFUSE v5.0.0 will use FSKit backend
- "No more rebooting into recovery mode"

**Timeline:**
- macOS 15.4 (March 2025): FSKit officially supported
- macOS 26: macFUSE fully on FSKit backend
- Xcode 16.1+: FSKit development supported

**Current state:**
- API available but documentation sparse
- Some rough edges (e.g., unmount issues during app updates)
- JuiceFS would need to add FSKit support (no issue filed yet)

**Bottom line:** This is where Apple wants everyone to go. Worth monitoring, but not ready for production use today.

### Option D: WebDAV Mount
**Verdict: ⚠️ Simplest but limited**

macOS natively supports mounting WebDAV as a volume.

**Pros:**
- Native, no extra software
- Works today

**Cons:**
- Performance: network filesystem semantics
- No POSIX compliance (permissions, symlinks, etc.)
- Not suitable for development workloads

### Recommendation

Given our constraint (userspace only, no kext):

1. **Short term:** Don't pursue JuiceFS-as-common-layer on macOS
2. **Medium term:** Monitor FUSE-T + JuiceFS progress, or contribute go-fuse work
3. **Long term:** FSKit may be the answer, but not today

**This shifts our focus to Option B/C/D from the "common data representation" question:**
- Content-addressed blocks we define
- Portable disk image interchange
- File-level sync

---

## Critical Ambiguities

### APFS sparse bundle internals

- Are band boundaries deterministic?
- Can we map bands → content-addressed blocks?
- Or do we need to read the mounted filesystem to chunk properly?

### JuiceFS architecture

- How does `juicefs clone` work internally?
- What's stored in S3 vs SQLite metadata?
- Can we replicate this without JuiceFS?

### Performance requirements

- What checkpoint latency is acceptable for cloud sync?
- Is async background sync OK, or do we need sync to complete before continuing?
- What's the bandwidth/latency to S3 we should design for?

---

## Research Agenda

### ~~Spike 1: JuiceFS on macOS~~ (BLOCKED)
**Status:** ❌ Not viable without kernel extension

**Findings:**
- [x] JuiceFS requires FUSE
- [x] macFUSE requires kernel extension + SIP reduction — unacceptable
- [x] FUSE-T is userspace but JuiceFS doesn't support it ([Issue #4285](https://github.com/juicedata/juicefs/issues/4285))
- [x] FSKit is Apple's future answer but JuiceFS hasn't adopted it

**Conclusion:** JuiceFS-as-common-layer is not viable on macOS today. Revisit when:
- JuiceFS adds FUSE-T support, OR
- JuiceFS adds FSKit support (macOS 15.4+)

### Spike 1b: FUSE-T Performance Baseline (NEW)
**Goal:** Understand FUSE-T overhead for potential future use

Tasks:
- [ ] Install FUSE-T: `brew install macfuse`
- [ ] Test with rclone mount (already supports FUSE-T)
- [ ] Benchmark: create 10k files, random reads/writes
- [ ] Compare to native APFS sparse bundle performance
- [ ] Test checkpoint-like operations (if rclone supports snapshots)

Questions to answer:
- What's the NFS loopback overhead?
- Is FUSE-T fast enough if JuiceFS adds support later?
- Could we build our own FUSE-T filesystem for checkpointing?

### Spike 2: Content-addressed chunking formats
**Goal:** Understand existing solutions for content-addressed file sync

Research:
- [ ] casync — Linux, content-defined chunking, designed for OS images
- [ ] restic — Backup tool, content-addressed dedup
- [ ] borgbackup — Similar to restic
- [ ] perkeep (née Camlistore) — Content-addressed storage
- [ ] IPFS — Content-addressed, distributed

Questions:
- What chunking algorithms do they use?
- What's the chunk size?
- How do they handle metadata (permissions, timestamps)?
- Can we adopt one of these as our interchange format?

### Spike 3: JuiceFS internals deep dive
**Goal:** Understand exactly how JuiceFS stores data

Research:
- [ ] How does JuiceFS chunk files?
- [ ] What's the S3 object structure?
- [ ] How does `juicefs clone` work?
- [ ] Can we read/write JuiceFS format without JuiceFS?

### Spike 4: APFS sparse bundle internals
**Goal:** Understand if we can map sparse bundles to content-addressed blocks

Research:
- [ ] How are bands allocated?
- [ ] Are band contents deterministic given file contents?
- [ ] Can we compute block hashes from bands?
- [ ] What happens to band contents on file modification?

### Spike 5: Minimal viable interchange format
**Goal:** Design the simplest format that works

Prototype:
- [ ] Define manifest schema (JSON? protobuf?)
- [ ] Define block naming (sha256? blake3?)
- [ ] Implement `agentfs export` → blocks + manifest
- [ ] Implement `agentfs import` ← blocks + manifest
- [ ] Test roundtrip: agentfs → S3 → opensprite → S3 → agentfs

---

## Prior Art to Research

| Project | What it does | Why relevant |
|---------|--------------|--------------|
| JuiceFS | POSIX FS backed by object storage | Potential common layer (blocked on macOS) |
| **FUSE-T** | Userspace FUSE via NFS loopback | macOS FUSE without kext |
| **FSKit** | Apple's native userspace FS framework | Future macOS FS solution |
| casync | Content-addressed OS image sync | Chunking algorithm |
| restic | Backup with dedup | Content-addressed format |
| Nix store | Content-addressed packages | How they handle dedup |
| Docker registry | Layer-based image storage | Content-addressed layers |
| git LFS | Large file storage | Pointer files + external blocks |
| Syncthing | Continuous file sync | Sync protocol |
| rsync | Delta sync | Block-level diffing |
| LiteFS | SQLite replication | How they handle sync |
| rclone | Cloud storage swiss army knife | Already supports FUSE-T |
| Cryptomator | Encrypted cloud storage | Already supports FUSE-T |

---

## Decision Tree (Historical)

```
Can JuiceFS run well on macOS? (userspace only)
│
├── With macFUSE kext? → NO (requires SIP reduction)
│
├── With FUSE-T? → BLOCKED (JuiceFS doesn't support it yet)
│
├── With FSKit? → FUTURE (not ready)
│
└── Do we need checkpoint interchange at all?
         │
         ├── What problem are we solving?
         │   └── Cross-environment coordination
         │
         ├── Can existing tools solve it?
         │   ├── Git → durable history, milestones ✓
         │   └── Syncthing → real-time file sync ✓
         │
         └── CONCLUSION: Don't build interchange.
             └── Files are the interface.
             └── Checkpoints stay local.
             └── Use git + syncthing.
```

---

## ~~Next Steps~~ (Superseded)

Research complete. See [Conclusion](#conclusion-decoupled-layered-architecture).

**Actual next steps for AgentFS:**
1. Continue Phase 2 (causality & hooks) from GOALS.md
2. Document Syncthing integration for multi-machine workflows
3. Keep checkpoints local, let git/syncthing handle coordination

---

## ~~Open Questions for Discussion~~

Resolved by conclusion below.

---

## Conclusion: Decoupled Layered Architecture

### Key Insight

**Checkpoints are a local optimization. Files are the universal interface.**

We don't need checkpoint interchange between environments. Each system optimizes for local speed using native primitives. Coordination happens at the file level using existing, battle-tested tools.

### The Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                        LAYER 3: DURABLE HISTORY                      │
│                                                                      │
│                              Git                                     │
│                                                                      │
│   - Commits = durable milestones                                    │
│   - Push/pull for cross-environment transfer                        │
│   - Handles: history, branching, collaboration, code review         │
│   - Latency: seconds to minutes (manual)                            │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
                                 ▲
                                 │ git commit (when work is complete)
                                 │
┌─────────────────────────────────────────────────────────────────────┐
│                     LAYER 2: REAL-TIME SYNC                          │
│                                                                      │
│                           Syncthing                                  │
│                                                                      │
│   - Continuous file sync between environments                       │
│   - Agents see each other's work in real-time                       │
│   - Filesystem as coordination primitive                            │
│   - Latency: seconds (automatic)                                    │
│                                                                      │
│        ┌──────────────┐                    ┌──────────────┐         │
│        │   Agent A    │◄──── files ───────►│   Agent B    │         │
│        │   (macOS)    │    (real-time)     │   (cloud)    │         │
│        └──────────────┘                    └──────────────┘         │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
                                 │
                                 │ files (universal interface)
                                 │
┌─────────────────────────────────────────────────────────────────────┐
│                    LAYER 1: LOCAL CHECKPOINTS                        │
│                                                                      │
│     AgentFS (macOS)                    OpenSprite (Linux/Cloud)     │
│                                                                      │
│     ┌─────────────────────┐            ┌─────────────────────┐      │
│     │ APFS sparse bundle  │            │ JuiceFS + ext4      │      │
│     │ Band-level COW      │            │ juicefs clone       │      │
│     │ ~60ms checkpoint    │            │ ~350ms checkpoint   │      │
│     └─────────────────────┘            └─────────────────────┘      │
│                                                                      │
│   - Optimized for LOCAL speed                                       │
│   - Ephemeral working state                                         │
│   - Agent "undo/redo" during exploration                            │
│   - NOT synced across environments                                  │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

### Layer Responsibilities

| Layer | Tool | Latency | Scope | Purpose |
|-------|------|---------|-------|---------|
| 1. Checkpoints | agentfs/opensprite | 60-350ms | Local only | Agent undo/redo during work |
| 2. Real-time sync | Syncthing | ~seconds | Multi-environment | Agents see each other's files |
| 3. Durable history | Git | Manual | Permanent | Milestones, collaboration, review |

### Design Principles

1. **Local systems stay local**
   - Optimize for speed using native OS primitives
   - No network concerns in checkpoint code
   - Each environment uses what's fastest (APFS on macOS, JuiceFS on Linux)

2. **Coordination via existing tools**
   - Git for durable history (everyone knows it)
   - Syncthing for real-time sync (battle-tested)
   - Don't reinvent distributed systems

3. **Files as the universal interface**
   - Agents coordinate by reading/writing files
   - Lock files, status files, output files
   - Debuggable, composable, no custom protocols

4. **Layers are decoupled**
   - Can use agentfs without Syncthing
   - Can use Syncthing without Git
   - Each layer evolves independently

### What We Don't Build

- ~~Custom sync protocol~~
- ~~Checkpoint interchange format~~
- ~~Distributed checkpoint system~~
- ~~Content-addressed block store~~
- ~~Cloud-native checkpoint storage~~

### What We Focus On

**AgentFS:**
- Fast local checkpoints (keep improving from 60ms)
- Seamless agent integration (hooks, CLI)
- Great UX for checkpoint/restore/diff

**Integration:**
- Documentation for Syncthing setup
- Git workflow recommendations
- Example agent coordination patterns

### Answered Questions

| Original Question | Answer |
|-------------------|--------|
| What's the common data representation? | **Files.** No checkpoint interchange needed. |
| Can JuiceFS run on macOS? | **Blocked.** No userspace FUSE option today. |
| What chunking algorithm? | **N/A.** Not building content-addressed sync. |
| How do we handle local-only optimizations? | **Checkpoints stay local.** That's the whole point. |
| What's the sync model? | **Layered.** Syncthing for real-time, git for durable. |
| How do we handle conflicts? | **Git handles it.** Or Syncthing's conflict files. |

### Future Considerations

If requirements change, revisit when:
- JuiceFS adds FUSE-T or FSKit support (enables unified layer)
- We need checkpoint history across environments (would need interchange)
- Real-time sync latency becomes critical (might need custom protocol)

For now, this architecture is:
- **Simple:** Uses existing tools
- **Decoupled:** Each layer independent
- **Fast:** Local checkpoints stay fast
- **Proven:** Git and Syncthing are battle-tested

---

*Research spike complete. 2025-01-28*
