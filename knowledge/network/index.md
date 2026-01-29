# Service Discovery & Routing: Synthesis

A comprehensive comparison of service discovery approaches for building **forkable, port-abstracted applications** - specifically targeting **macOS** with **minimal coordination**.

## The Problem Space

You want:
1. **Forkable apps** - run multiple instances without conflicts
2. **Port abstraction** - apps don't hardcode or fight over ports
3. **Minimal coordination** - apps do as little special work as possible
4. **Host-based routing** - `myapp.localhost` routes to the right instance

The fundamental tension: **discovery requires some signal**, but you want apps to remain ignorant.

---

## Approach Comparison Matrix

| Approach | Coordination Level | macOS Support | Port Conflicts | Complexity |
|----------|-------------------|---------------|----------------|------------|
| **Active Registration** | High (SDK required) | Cross-platform | Avoided by registry | High |
| **File/Socket Convention** | Low (path convention) | Excellent (FSEvents) | Eliminated (UDS) | Low |
| **Environment Injection** | Medium (read $PORT) | Cross-platform | Avoided by launcher | Low |
| **Socket Activation** | Low (just accept()) | Good (launchd) | Eliminated | Medium |
| **Passive Discovery** | None (truly magic) | Limited (no eBPF) | Observed after bind | High |
| **Reverse Tunneling** | Low (run daemon) | Excellent | Eliminated | Medium |

---

## Recommended Approach for macOS + Minimal Coordination

### Winner: **Unix Domain Sockets + FSEvents Directory Watching**

```
Router (watches ~/.agentfs/run/)
        │
        │ FSEvents notification: "myapp-abc123.sock created"
        │
        ▼
┌─────────────────────────────────────────────┐
│  ~/.agentfs/run/                            │
│  ├── myapp-abc123.sock   ← App instance 1   │
│  ├── myapp-def456.sock   ← App instance 2   │
│  └── otherapp-xyz789.sock                   │
└─────────────────────────────────────────────┘
```

**Why this wins for your use case:**

1. **No port conflicts** - Unix sockets use paths, not ports
2. **Minimal app change** - app binds to `$AGENTFS_SOCKET` instead of `0.0.0.0:$PORT`
3. **Native macOS support** - FSEvents is fast and efficient
4. **Identity encoded in filename** - `{app}-{instance}.sock`
5. **Simple router** - just watch a directory, proxy to sockets
6. **Performance** - Unix sockets are ~40% faster than TCP loopback

**App contract (minimal):**
```python
import os

socket_path = os.environ.get('AGENTFS_SOCKET', '/tmp/myapp.sock')
server.bind(socket_path)  # Instead of server.bind(('0.0.0.0', 8080))
```

---

## Detailed Analysis by Approach

### 1. Active Registration (Consul, Eureka, etcd)

**How it works:** Apps explicitly register with a central registry via API calls and heartbeats.

**Coordination required:** High - apps need SDK, health checks, graceful deregistration.

**macOS relevance:** Works but overkill for local development. These are designed for distributed systems.

**When to use:** Multi-machine deployments, Kubernetes, service mesh.

**When to avoid:** Single-machine development, local app forking.

**Key insight from research:**
> "Active registration trades operational complexity for dynamic service discovery capabilities. Accept that you're adding a critical dependency."

[Full details: active-registration.md](./active-registration.md)

---

### 2. File/Socket Convention

**How it works:** Apps create sockets/files in a known directory. Router watches with FSEvents (macOS) or inotify (Linux).

**Coordination required:** Low - just a path convention.

**macOS relevance:** Excellent. FSEvents watches entire directory trees efficiently.

**Key considerations:**

| Aspect | Unix Domain Socket | TCP Port File |
|--------|-------------------|---------------|
| Port conflicts | Impossible | Must coordinate |
| Performance | ~40% faster | Standard |
| Cleanup on crash | Socket file remains | Port file remains |
| Cross-network | No | With extra steps |

**Stale socket handling (critical!):**
```c
// Lock file pattern - flock() is released on crash
int lock_fd = open("/run/myapp.lock", O_RDONLY | O_CREAT, 0600);
if (flock(lock_fd, LOCK_EX | LOCK_NB) != 0) {
    exit(1);  // Another instance running
}
unlink("/run/myapp.sock");  // Safe to remove stale
bind(sock_fd, ...);
```

**macOS FSEvents watching:**
```c
FSEventStreamRef stream = FSEventStreamCreate(
    NULL, callback, NULL, paths,
    kFSEventStreamEventIdSinceNow,
    0.5,  // 500ms latency (tunable)
    kFSEventStreamCreateFlagFileEvents
);
```

[Full details: file-socket-convention.md](./file-socket-convention.md)

---

### 3. Environment Injection ($PORT)

**How it works:** Launcher assigns port, sets `$PORT`, app reads it.

**Coordination required:** Medium - app must read `$PORT` and bind to it.

**macOS relevance:** Cross-platform, works everywhere.

**The 12-factor contract:**
- Platform says: "I'll set `$PORT`, you listen on it"
- App says: "I'll bind to whatever port you give me"

**Handling non-compliant apps:**
1. **Wrapper script** - transform `$PORT` to `--port` flag
2. **Config templating** - `envsubst` generates config
3. **socat forwarding** - proxy from `$PORT` to hardcoded port

**Key insight:**
> "Environment injection wins when apps are written/controlled by you. It loses when apps can't be modified."

[Full details: environment-injection.md](./environment-injection.md)

---

### 4. Socket Activation (launchd on macOS)

**How it works:** OS owns the socket, passes file descriptor to app. App doesn't bind - it inherits.

**Coordination required:** Low - app just calls `accept()` on the inherited FD.

**macOS relevance:** Native via `launchd` with `launch_activate_socket()`.

**The elegant inversion:**
```
Traditional: App starts → Creates socket → Binds → Listens → Ready
Activated:   launchd binds → [request arrives] → App starts with socket
```

**launchd plist example:**
```xml
<key>Sockets</key>
<dict>
    <key>Listener</key>
    <dict>
        <key>SockServiceName</key>
        <string>8080</string>
        <key>SockType</key>
        <string>stream</string>
    </dict>
</dict>
```

**App code (macOS):**
```c
#include <launch.h>

int *fds = NULL;
size_t cnt = 0;
int err = launch_activate_socket("Listener", &fds, &cnt);
if (err == 0) {
    // fds[0] is already bound and listening!
    // Just call accept()
}
```

**Tradeoff:** Requires launchd plist per app - doesn't fit "just launch" goal unless you generate plists dynamically.

[Full details: socket-activation.md](./socket-activation.md)

---

### 5. Passive Discovery (eBPF, LD_PRELOAD)

**How it works:** Observe `bind()` syscalls without app cooperation.

**Coordination required:** None - truly magic from app's perspective.

**macOS relevance:** Limited.

| Mechanism | Linux | macOS |
|-----------|-------|-------|
| eBPF | Full support | No kprobes, limited DTrace |
| LD_PRELOAD | Yes | Yes (DYLD_INSERT_LIBRARIES) |
| /proc/net/tcp | Yes | No |
| ptrace | Yes | Limited (SIP) |

**DYLD_INSERT_LIBRARIES (macOS LD_PRELOAD equivalent):**
```c
// bind_hook.c
int bind(int sockfd, const struct sockaddr *addr, socklen_t addrlen) {
    // Intercept, notify router, then call real bind
    static int (*real_bind)(...) = NULL;
    if (!real_bind) real_bind = dlsym(RTLD_NEXT, "bind");

    // Log/notify about the bind
    notify_router(sockfd, addr);

    return real_bind(sockfd, addr, addrlen);
}
```

```bash
DYLD_INSERT_LIBRARIES=./bind_hook.dylib ./myapp
```

**macOS limitations:**
- System Integrity Protection (SIP) blocks injection for system binaries
- Must be disabled or apps must be non-system
- Code signing can interfere

**Verdict:** Possible on macOS but fragile. Better for Linux production with eBPF.

[Full details: passive-discovery.md](./passive-discovery.md)

---

### 6. Reverse Tunneling (cloudflared, frp, bore)

**How it works:** App connects outbound to relay; relay routes inbound traffic through tunnel.

**Coordination required:** Low - run tunnel daemon alongside app.

**macOS relevance:** Excellent - pure userspace, no kernel dependencies.

**Architecture:**
```
Internet → Platform Edge → Tunnel → Local App
             (cloudflare)     ↑
                              │
                         Outbound connection
                         (no firewall issues)
```

**Best for local dev:** Cloudflare quick tunnels
```bash
cloudflared tunnel --url http://localhost:3000
# Instant HTTPS URL: https://random-words.trycloudflare.com
```

**Self-hosted option:** frp
```ini
# frps.ini (server)
[common]
bind_port = 7000
vhost_http_port = 80

# frpc.ini (client)
[common]
server_addr = your.server.com
[web]
type = http
local_port = 3000
subdomain = myapp  # → myapp.your.server.com
```

**Minimal option:** bore (~500 lines of Rust)
```bash
bore local 3000 --to bore.pub
# Exposes localhost:3000 on bore.pub:XXXXX
```

**Tradeoff:** Adds latency (extra hop), requires external infrastructure.

[Full details: reverse-tunneling.md](./reverse-tunneling.md)

---

## Decision Framework

### For Your Goal: Forkable Apps on macOS

**Recommended stack:**

```
┌─────────────────────────────────────────────────────────────┐
│                     agentfs router                          │
│  - Listens on *.localhost:80                                │
│  - Watches ~/.agentfs/run/ with FSEvents                    │
│  - Routes Host header to matching .sock file                │
└─────────────────────────────────────────────────────────────┘
        │                           │
        ▼                           ▼
┌───────────────────┐    ┌───────────────────┐
│ myapp-1.sock      │    │ myapp-2.sock      │
│ (fork 1)          │    │ (fork 2)          │
└───────────────────┘    └───────────────────┘
```

**Launcher responsibilities:**
1. Generate unique socket path: `~/.agentfs/run/{app}-{uuid}.sock`
2. Set `AGENTFS_SOCKET` environment variable
3. Optionally set `AGENTFS_HOSTNAME` for app awareness
4. Launch app

**App requirements (minimal):**
- Read `$AGENTFS_SOCKET` (or `$PORT` for TCP fallback)
- Bind to that path/port

**Router responsibilities:**
1. Watch `~/.agentfs/run/` with FSEvents
2. Parse socket filenames for app identity
3. Route `myapp.localhost` → `~/.agentfs/run/myapp-*.sock`
4. Handle load balancing if multiple instances

---

## Coordination Spectrum Summary

```
ZERO ←────────────────────────────────────────────────→ HIGH
COORDINATION                                       COORDINATION

 Passive        File/Socket     Environment    Socket        Active
 Discovery      Convention      Injection      Activation    Registration
 (eBPF)         (UDS+watch)     ($PORT)        (launchd)     (Consul)
    │               │               │              │             │
    │               │               │              │             │
    ▼               ▼               ▼              ▼             ▼
 App does        App binds       App reads      App calls    App uses
 nothing         to path         env var        accept()     SDK
```

**For macOS with minimal coordination:** File/Socket Convention is the sweet spot.

---

## Implementation Checklist

For your agentfs routing layer:

- [ ] **Router daemon**
  - [ ] HTTP server on `*.localhost:80` (may need /etc/hosts or dnsmasq)
  - [ ] FSEvents watcher on `~/.agentfs/run/`
  - [ ] HTTP proxy to Unix domain sockets
  - [ ] Handle socket cleanup on app crash (connect-test or lock file)

- [ ] **Launcher integration**
  - [ ] Generate unique socket paths
  - [ ] Set `AGENTFS_SOCKET` env var
  - [ ] Write sidecar metadata (optional): `{app}.json`

- [ ] **App compatibility layer** (for non-compliant apps)
  - [ ] Wrapper script to translate `$AGENTFS_SOCKET` → `--port`
  - [ ] socat bridge for truly stubborn apps

- [ ] **Development tools**
  - [ ] CLI to list active services: `agentfs ps`
  - [ ] CLI to route manually: `agentfs route myapp.localhost ./app.sock`

---

## Further Reading

- [file-socket-convention.md](./file-socket-convention.md) - Unix sockets, FSEvents, cleanup strategies
- [environment-injection.md](./environment-injection.md) - $PORT contract, handling non-compliant apps
- [socket-activation.md](./socket-activation.md) - launchd deep dive
- [passive-discovery.md](./passive-discovery.md) - eBPF, DYLD_INSERT_LIBRARIES
- [reverse-tunneling.md](./reverse-tunneling.md) - cloudflared, frp, bore analysis
- [active-registration.md](./active-registration.md) - Consul, Eureka, etcd patterns
