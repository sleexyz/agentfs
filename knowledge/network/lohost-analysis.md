# Lohost Analysis

**Repository:** `/Users/slee2/wbsm/lohost`
**Purpose:** Local virtual host router for development with subdomain routing

---

## 1. What is Lohost?

Lohost is a development tool that provides **virtual host routing for local development servers**. It solves the problem of running multiple dev servers without port conflicts by:

1. Running a **single daemon** on port 8080 that acts as a reverse proxy
2. Routing requests based on **Host header subdomains** (e.g., `frontend.localhost:8080` -> frontend server)
3. **Automatically allocating ports** for dev servers via the `PORT` environment variable
4. **Intercepting DNS** at the syscall level to make `*.localhost` resolve to `127.0.0.1`

### Architecture Overview

```
Browser request: http://api.localhost:8080
        |
        v
+------------------+
|  lohostd daemon  |  (port 8080)
|  (daemon.ts)     |
+------------------+
        |
        | Host header: api.localhost
        | Route to: /tmp/api.sock
        v
+------------------+
|  UDS Proxy       |  (client.ts creates this)
|  /tmp/api.sock   |
+------------------+
        |
        v
+------------------+
|  Your Dev Server |  (PORT=10003)
+------------------+
```

### Key Files

| File | Purpose |
|------|---------|
| `src/index.ts` | CLI entry point, command parsing |
| `src/daemon.ts` | HTTP reverse proxy daemon (`LohostDaemon`) |
| `src/client.ts` | Client orchestrator (`LohostClient`), spawns child processes |
| `native/darwin/lohost_dns.c` | macOS DNS interposition (DYLD) |
| `native/linux/lohost_dns.c` | Linux DNS interposition (LD_PRELOAD) |

---

## 2. Routing Mechanism

### Host-Based Routing

The daemon extracts the subdomain from the `Host` header and looks up the corresponding service:

```typescript
// daemon.ts - extractSubdomain
private extractSubdomain(host: string | undefined): string | null {
  const hostWithoutPort = host.split(":")[0];
  const suffix = `.${this.config.routeDomain}`;  // ".localhost"

  if (!hostWithoutPort.endsWith(suffix)) return null;
  return hostWithoutPort.slice(0, -suffix.length);  // "api" from "api.localhost"
}
```

### Longest-Suffix Match

For nested subdomains like `user1.myapp.localhost`, the router finds the rightmost registered service:

```typescript
// daemon.ts - findService
private findService(subdomain: string): Service | null {
  const parts = subdomain.split(".");  // ["user1", "myapp"]

  for (let i = 0; i < parts.length; i++) {
    const candidate = parts.slice(i).join(".");  // "user1.myapp", then "myapp"
    const service = this.services.get(candidate);
    if (service) return service;
  }
  return null;
}
```

This allows multi-tenant patterns where `user1.myapp.localhost` routes to "myapp" with the full Host header preserved.

### Transport: Unix Domain Sockets

All proxying goes through UDS rather than TCP ports:

```typescript
// daemon.ts - proxyToSocket
private proxyToSocket(req: IncomingMessage, res: ServerResponse, socketPath: string): void {
  const options = {
    socketPath,        // e.g., "/tmp/api.sock"
    path: req.url,
    method: req.method,
    headers: req.headers,  // Host header preserved!
  };
  const proxyReq = httpRequest(options, (proxyRes) => {
    res.writeHead(proxyRes.statusCode, proxyRes.headers);
    proxyRes.pipe(res);
  });
  req.pipe(proxyReq);
}
```

---

## 3. Port Assignment

### Automatic Port Allocation

The client finds a free port using the kernel's ephemeral port allocator:

```typescript
// client.ts - findFreePort
private async findFreePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = createServer();
    srv.listen(0, "127.0.0.1", () => {  // Port 0 = kernel picks
      const addr = srv.address();
      const port = addr.port;
      srv.close(() => resolve(port));
    });
  });
}
```

### UDS Bridge Creation

After getting a port, the client creates a UDS-to-TCP bridge:

```typescript
// client.ts - startProxy
private async startProxy(): Promise<void> {
  this.proxy = createServer((udsConn) => {
    const tcpConn = createConnection({
      port: this.tcpPort,
      host: "localhost",
    });
    udsConn.pipe(tcpConn);
    tcpConn.pipe(udsConn);
  });
  this.proxy.listen(this.socketPath);  // e.g., /tmp/myapp.sock
}
```

### Environment Variable Injection

The allocated port is passed to the child process via `PORT`:

```typescript
// client.ts - spawnChild
this.child = spawn(command, args, {
  env: { ...process.env, ...preloadEnv, PORT: String(this.tcpPort) },
  stdio: "inherit",
});
```

---

## 4. Native DNS Interposition (The dylib)

This is the most interesting part. macOS doesn't resolve `*.localhost` by default. Lohost uses **DYLD interposition** to hook DNS syscalls.

### Why It's Needed

RFC 6761 reserves `.localhost` for loopback, but macOS's resolver returns `EAI_NONAME` for subdomains like `foo.localhost`.

### macOS Implementation (DYLD_INSERT_LIBRARIES)

**File:** `native/darwin/lohost_dns.c`

Two hooks using Mach-O `__DATA,__interpose` sections:

#### Hook 1: getaddrinfo (synchronous DNS)

Used by Node.js, Python, C programs:

```c
int hooked_getaddrinfo(const char *node, const char *service,
                       const struct addrinfo *hints,
                       struct addrinfo **res) {
    if (is_localhost_domain(node)) {
        // Return synthetic 127.0.0.1 result
        *res = make_localhost_result(service, hints);
        return 0;
    }
    return getaddrinfo(node, service, hints, res);  // fallthrough
}

// Register via interpose section
__attribute__((section("__DATA,__interpose"))) = {
    (const void *)hooked_getaddrinfo,
    (const void *)getaddrinfo
};
```

#### Hook 2: dlsym (for Bun's async DNS)

Bun uses Apple's private `getaddrinfo_async_start` API, loaded via `dlsym()` at runtime. Direct interposition causes infinite recursion, so lohost hooks `dlsym` itself:

```c
static getaddrinfo_async_start_fn real_async_start = NULL;

void* hooked_dlsym(void *handle, const char *symbol) {
    void *result = dlsym(handle, symbol);

    if (strcmp(symbol, "getaddrinfo_async_start") == 0) {
        if (result && !real_async_start) {
            real_async_start = result;  // Capture real function
        }
        return hooked_getaddrinfo_async_start;  // Return our hook
    }
    return result;
}
```

### Linux Implementation (LD_PRELOAD)

**File:** `native/linux/lohost_dns.c`

Simpler approach using `RTLD_NEXT`:

```c
static int (*real_getaddrinfo)(...) = NULL;

int getaddrinfo(const char *node, ...) {
    if (!real_getaddrinfo) {
        real_getaddrinfo = dlsym(RTLD_NEXT, "getaddrinfo");
    }
    if (is_localhost_domain(node)) {
        *res = make_localhost_result(service, hints);
        return 0;
    }
    return real_getaddrinfo(node, service, hints, res);
}
```

### Limitations

| Runtime | Status | Notes |
|---------|--------|-------|
| Node.js, Python, Ruby, C | Works | Uses `getaddrinfo` |
| Bun | Works | `dlsym` hook captures async API |
| Go | Limited | Static DNS resolver, needs `CGO_ENABLED=1` |
| System binaries | Blocked | macOS SIP prevents DYLD injection |

---

## 5. SDK Brainstorming Insights

The `LOHOST_SDK_BRAINSTORMING.md` document explores making lohost embeddable in other frameworks. Key concepts:

### Flexibility Spectrum

```
Level 1: DNS-only       → User gets just DNS interposition
Level 2: Registry+DNS   → User gets service tracking + DNS
Level 3: Routing Helpers → User gets pure functions for routing
Level 4: Full Middleware → User mounts lohost in their Express/Fastify app
Level 5: CLI (current)  → lohost owns everything
```

### Proposed Handler Factory Pattern

```typescript
import { createLohostHandler, createRegistrationApi } from '@websim/lohost';

const handler = createLohostHandler({
  routeDomain: 'localhost',
  socketDir: '/tmp',
  registry: myCustomRegistry,  // Pluggable storage
  onRequest: (req, service) => { /* logging */ },
});

// Use with any HTTP server
http.createServer((req, res) => {
  if (req.url.startsWith('/_lohost')) return api(req, res);
  return handler(req, res);
}).listen(8080);
```

### Key Design Decisions

1. **Zero external dependencies** - Uses only Node.js built-ins
2. **UDS for transport** - Avoids port coordination between services
3. **In-memory service registry** - Simple Map, could be swapped for Redis/Consul
4. **Daemon auto-spawn** - Client starts daemon if not running

---

## 6. Key Code Patterns

### Service Discovery via HTTP API

```typescript
// daemon.ts - Registration endpoint
// POST /_lohost/register
const { name, socketPath, port } = JSON.parse(body);
this.services.set(name, { name, socketPath, port, registeredAt: new Date() });

// GET /_lohost/services
return Array.from(this.services.values());
```

### Daemon Lifecycle Management

```typescript
// client.ts - Auto-start daemon
private async ensureDaemon(): Promise<void> {
  if (await this.checkDaemonHealth()) return;  // Already running

  this.spawnDaemon();  // Fire-and-forget, detached

  for (let i = 0; i < 30; i++) {
    await this.sleep(100);
    if (await this.checkDaemonHealth()) return;
  }
  throw new Error('Daemon failed to start');
}
```

### Minimal Coordination

The only shared state is:
1. The daemon port (default 8080, configurable via `LOHOST_PORT`)
2. The socket directory (default `/tmp`)

Services self-register; no central configuration file needed.

---

## 7. Relevance to AgentFS

### Service Discovery Patterns

Lohost's HTTP-based registration could inspire how agentfs services discover each other:
- Simple `/_lohost/register` POST to announce presence
- Health check at `/_lohost/health`
- No configuration files, just environment variables

### Port Abstraction

Instead of hardcoded ports, lohost demonstrates:
- **Dynamic port allocation** via `PORT=0` binding
- **UDS as the coordination primitive** - no port conflicts
- **Host header routing** - single entry port (8080) for all services

### macOS-Specific Techniques

The DYLD interposition pattern could be useful for:
- Intercepting filesystem calls (like FUSE but lighter)
- Transparent proxying without app modification
- Runtime behavior modification for testing

### Minimal Coordination Approach

Lohost avoids complex service meshes:
- No service discovery service
- No configuration management
- Just a daemon + per-service registration
- Failures are local (one service crash doesn't cascade)

---

## 8. Architecture Diagram

```
                    +-----------------+
                    |    Browser      |
                    +-----------------+
                           |
                           | http://api.localhost:8080
                           v
+----------------------------------------------------------------+
|                     lohost daemon                               |
|                     (port 8080)                                 |
|                                                                 |
|   +---------------------------------------------------------+  |
|   |  Service Registry (Map<name, Service>)                  |  |
|   |                                                         |  |
|   |  "api"      -> { socketPath: "/tmp/api.sock" }         |  |
|   |  "frontend" -> { socketPath: "/tmp/frontend.sock" }    |  |
|   +---------------------------------------------------------+  |
|                                                                 |
|   Routing: Host header -> subdomain -> lookup -> UDS proxy     |
+----------------------------------------------------------------+
        |                           |
        v                           v
+----------------+          +----------------+
| /tmp/api.sock  |          | /tmp/frontend.sock |
+----------------+          +----------------+
        |                           |
        v                           v
+----------------+          +----------------+
| API Server     |          | Frontend       |
| (PORT=10003)   |          | (PORT=10007)   |
+----------------+          +----------------+

Each LohostClient:
1. Finds free port (kernel assigns)
2. Creates UDS -> TCP bridge
3. Registers with daemon
4. Spawns child with PORT env var
5. Injects DYLD/LD_PRELOAD for DNS
```

---

## Summary

Lohost is an elegantly minimal local service router that:

1. **Abstracts ports** - Services get random ports, routing is by name
2. **Uses UDS** - Avoids port coordination, enables file-based permissions
3. **Hooks DNS** - Makes `*.localhost` work without `/etc/hosts` editing
4. **Zero deps** - Pure Node.js stdlib, native C for DNS interception
5. **Auto-manages lifecycle** - Daemon spawns on demand, services self-register

The DNS interposition technique (DYLD/LD_PRELOAD) is particularly interesting for systems that need transparent interception of system calls without modifying application code.
