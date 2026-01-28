# File Sync Tools Deep Comparison

> Research for AgentFS remote sync architecture

---

## Ecosystem Size (GitHub Stars)

| Tool | Stars | Forks | Status |
|------|-------|-------|--------|
| **Syncthing** | 79,412 ⭐ | 4,901 | Most popular, very active |
| **Unison** | 5,068 ⭐ | 263 | Mature, stable, low activity |
| **Mutagen** | 3,833 ⭐ | 170 | Growing, Docker-owned |

**Winner: Syncthing** by a huge margin (20x more stars than competitors)

---

## Head-to-Head Comparison

### Syncthing

**Architecture:**
- Peer-to-peer, no central server
- Block Exchange Protocol (BEP) over TLS
- Devices discover each other via global discovery servers or LAN broadcast
- NAT traversal via relay servers

**Sync Algorithm:**
- **Block-based**: Files split into 128KB-16MB blocks (dynamic sizing)
- **Content-addressed**: SHA-256 hash per block
- **Vector clocks**: Track concurrent modifications per device
- **Deduplication**: Same content = same block, reused across files

**Conflict Resolution:**
- Creates `.sync-conflict-<date>-<device>` files
- Latest modification time wins (with device ID tiebreaker)
- Both versions preserved for manual resolution

**Cloud Capabilities:**
- ❌ No native S3/cloud storage support
- ✅ Can sync TO a cloud VM running Syncthing
- ✅ Azure Blob support in codebase (for infrastructure, not sync target)

**Strengths:**
- Massive ecosystem, battle-tested
- Works offline, syncs when reconnected
- No account required, fully self-hosted
- Excellent NAT traversal

**Weaknesses:**
- Not designed for cloud storage targets
- Requires Syncthing running on both ends
- Conflict files can accumulate

---

### Mutagen

**Architecture:**
- Client-agent model (agent deployed to remote)
- Transports: SSH, Docker, local
- Three-way merge reconciliation

**Sync Algorithm:**
- **rsync-style deltas**: Only changed bytes transferred
- **Ancestor tracking**: Maintains baseline for conflict detection
- **Block sizes**: 1KB-64KB optimal blocks
- **Fast hashing**: XXH128 (10x faster than SHA-1)

**Conflict Resolution:**
- Four modes:
  - `two-way-safe`: Both sides equal, conflicts require manual resolution
  - `two-way-resolved`: Alpha wins all conflicts
  - `one-way-safe`: Alpha→Beta, Beta changes block propagation
  - `one-way-replica`: Complete mirroring

**Cloud Capabilities:**
- ❌ No native S3 support
- ✅ SSH to any cloud VM (AWS, GCP, Azure)
- ✅ Docker containers (local or remote)
- ✅ Kubernetes pods

**Strengths:**
- Sub-second latency (designed for dev workflows)
- Excellent Docker integration (Docker owns it now)
- Smart conflict modes
- Works with any SSH-accessible server

**Weaknesses:**
- Requires SSH or Docker access (can't sync to pure storage)
- Smaller community
- Pre-1.0 (API may change)

---

### Unison

**Architecture:**
- Archive-based bidirectional sync
- Transports: SSH, TCP sockets, local
- Pure OCaml, no external dependencies

**Sync Algorithm:**
- **Three-way merge**: Archive + Replica1 + Replica2
- **MD5 fingerprinting**: Full content verification
- **rsync-style compression**: Block-based delta transfer
- **Inode tracking**: Detects renames without rescanning

**Conflict Resolution:**
- Interactive: User chooses direction per conflict
- `-force root`: One side always wins
- `-prefer root`: One side wins conflicts only
- External merge tool support

**Cloud Capabilities:**
- ❌ No native cloud storage support
- ✅ SSH to any server
- ✅ Works over slow/high-latency links

**Strengths:**
- 20+ years of stability
- Excellent conflict detection
- Low bandwidth (delta transfer)
- Cross-platform (even Windows↔Unix)

**Weaknesses:**
- Version compatibility issues (same version required on both ends until 2.52)
- No real-time watching built-in (needs external fswatch)
- Small development team

---

## Feature Comparison Matrix

| Feature | Syncthing | Mutagen | Unison |
|---------|-----------|---------|--------|
| **Ecosystem** | ⭐⭐⭐⭐⭐ (79K stars) | ⭐⭐ (3.8K) | ⭐⭐⭐ (5K) |
| **Real-time sync** | ✅ Continuous | ✅ Continuous | ⚠️ Repeat mode |
| **Block-level delta** | ✅ | ✅ | ✅ |
| **Bidirectional** | ✅ | ✅ | ✅ |
| **Conflict handling** | Keep both | Configurable modes | Interactive |
| **NAT traversal** | ✅ Excellent | ❌ Needs SSH | ❌ Needs SSH |
| **Docker integration** | ❌ | ✅ Native | ❌ |
| **S3/Cloud storage** | ❌ | ❌ | ❌ |
| **Offline support** | ✅ | ⚠️ Limited | ✅ |
| **No central server** | ✅ | ✅ | ✅ |
| **Latency** | ~1-5s | <1s | Manual trigger |
| **Setup complexity** | Low | Medium | Medium |

---

## Best For Local↔Cloud Bridging

### The Problem

None of these tools natively support S3/GCS as a sync target. They all require an **agent or peer running on the remote end**.

### Options for Local↔Cloud

**Option 1: Syncthing on Cloud VM**
```
Mac (Syncthing) ←→ Cloud VM (Syncthing) ←→ [optional: VM writes to S3]
```
- Run Syncthing on a small cloud VM (t3.micro, ~$5/month)
- VM acts as always-on peer
- VM can optionally backup to S3

**Pros:** Most reliable, great NAT traversal
**Cons:** Need to run a VM

**Option 2: Mutagen to Cloud VM**
```
Mac (Mutagen) ←SSH→ Cloud VM (Mutagen agent)
```
- Mutagen auto-deploys agent via SSH
- Faster than Syncthing for dev workflows
- No persistent daemon needed on VM

**Pros:** Lower latency, simpler setup
**Cons:** Requires SSH access, VM must be running

**Option 3: rclone bisync (different approach)**
```
Mac (rclone) ←→ S3 bucket directly
```
- Not real-time (cron-based)
- Native S3 support
- No VM needed

**Pros:** Direct S3 access, no VM
**Cons:** Not real-time, conflict handling is basic

---

## Other Notable Tools

### Resilio Sync (formerly BitTorrent Sync)
- Commercial, peer-to-peer
- Based on BitTorrent protocol
- Enterprise features (central management)
- **Stars:** Closed source, but widely used

### FreeFileSync
- Open source, GUI-based
- Real-time sync via RealTimeSync companion
- Local and SFTP only
- Good for manual sync workflows

### Lsyncd
- Linux-only (inotify + rsync)
- One-way sync only
- Very simple, very fast
- Good for replication, not bidirectional

### rclone
- "rsync for cloud storage"
- Supports 70+ cloud providers
- `rclone mount`: FUSE mount of cloud storage
- `rclone bisync`: Bidirectional sync (beta)
- **Not real-time** without external watcher

---

## Recommendation for AgentFS

### Best Overall: Syncthing
- Largest ecosystem = most likely to survive
- Excellent documentation
- Works offline, syncs automatically
- Community relay servers for NAT traversal

### Best for Dev Workflow: Mutagen
- Sub-second latency
- Docker integration (future-proof, Docker owns it)
- Smart conflict modes
- But: smaller ecosystem, pre-1.0

### For Direct Cloud Storage: rclone
- Only option that directly supports S3
- But: not real-time, basic conflict handling

---

## Hybrid Architecture for AgentFS

```
┌─────────────────────────────────────────────────────────────────┐
│  LOCAL (Mac)                                                    │
│                                                                 │
│  ~/projects/myapp/         ← User works here                   │
│  (sparse bundle mounted)                                        │
│       ↓                                                         │
│  AgentFS daemon            ← Checkpointing, causality tracking │
│       ↓                                                         │
│  Syncthing/Mutagen         ← Sync layer (pick one)             │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
                              ↕ (automatic sync)
┌─────────────────────────────────────────────────────────────────┐
│  CLOUD (VM or Container)                                        │
│                                                                 │
│  Syncthing/Mutagen agent   ← Receives synced files             │
│       ↓                                                         │
│  /home/user/myapp/         ← Mirror of local project           │
│       ↓ (optional)                                              │
│  Backup to S3              ← Durable storage                   │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

**Key insight:** Use existing sync tools for transport, AgentFS adds value via:
- Instant local snapshots (APFS reflinks)
- Causality tracking (agent metadata)
- Analysis tools (blame, timeline, diff)

---

## Sources

- [Syncthing GitHub](https://github.com/syncthing/syncthing)
- [Mutagen GitHub](https://github.com/mutagen-io/mutagen)
- [Unison GitHub](https://github.com/bcpierce00/unison)
- [Syncthing Block Exchange Protocol](https://docs.syncthing.net/specs/bep-v1.html)
- [Mutagen Documentation](https://mutagen.io/documentation/introduction)
- [rclone bisync](https://rclone.org/bisync/)
- [Resilio Sync](https://www.resilio.com/)
