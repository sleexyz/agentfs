# Knowledge Index

Index of research documents and spike findings.

---

## sparse-bundle-spike.md
**Tags:** sparse-bundle, hdiutil, macos, incremental-sync, bands, sha256
**Summary:** Comprehensive findings from spike validating sparse bundle internals for AgentFS incremental sync - band allocation, change detection, and recommended strategies.

---

## checkpoint-benchmark.md
**Tags:** performance, benchmark, checkpoint, mtime, hashing, apfs-reflink, sparse-bundle
**Summary:** Performance benchmarks validating <100ms checkpoint target - mtime detection, incremental hashing, and APFS reflink clone performance with 36k file project.

---

## sync-tools-comparison.md
**Tags:** syncthing, mutagen, unison, sync, distributed, conflict-resolution
**Summary:** Deep comparison of file sync tools (Syncthing, Mutagen, Unison) including architecture, conflict handling, and ecosystem size. Syncthing chosen for AgentFS.

---

## agentfs-vs-claude-snap.md
**Tags:** benchmark, comparison, claude-snap, sparse-bundle, apfs-clone, performance
**Summary:** Head-to-head benchmark comparing agentfs (sparse bundle + band clones) vs claude-snap (direct file clones). agentfs is 23x faster for checkpoints and 7x faster for restores on a 13k file project due to band compression (13,567 files → 124 bands).

---

## content-addressing-spike.md
**Tags:** content-addressing, sha256, file-hashing, fsevents, incremental, benchmark, sqlite
**Summary:** Spike validating content-addressed file tracking for Phase 2. Confirms <200ms target achievable for 10k files with 4-worker parallel hashing and mtime-based incremental detection. FSEvents works with sparse bundle mounts.

---

## two-layer-apfs.md
**Tags:** architecture, apfs, cow, sparse-bundle, bands, garbage-collection, checkpoint
**Summary:** Explains the two-layer APFS architecture (host APFS + inner APFS inside sparse bundle). Documents how this enables instant checkpoints via band-level COW cloning and eliminates the need for garbage collection by delegating reference counting to APFS.

---

## network/reverse-tunneling.md
**Tags:** networking, reverse-tunnel, cloudflare, frp, bore, ngrok, quic, multiplexing
**Summary:** Deep technical analysis of reverse tunneling implementations (Cloudflare Tunnel, frp, bore, localtunnel). Covers protocol negotiation, multiplexing, heartbeats, and reconnection strategies.

---

## network/active-registration.md
**Tags:** service-discovery, consul, eureka, etcd, kubernetes, heartbeat, lease
**Summary:** Analysis of active registration patterns for service discovery. Examines Consul, Eureka, and etcd implementations including heartbeat mechanisms, failure modes, and self-preservation.

---

## network/environment-injection.md
**Tags:** 12-factor, port-binding, heroku, cloud-run, fly-io, render, foreman, procfile
**Summary:** Research on environment variable injection for service discovery. Covers how platforms (Heroku, Cloud Run, Fly.io, Render) assign ports via $PORT, health check patterns, and strategies for apps that don't support environment-based configuration.

