# File/Socket Convention for Service Discovery

A deep dive into using filesystem artifacts (Unix domain sockets, PID files, port files) for service discovery. This is a "low-tech" approach that trades sophistication for simplicity.

## Table of Contents
1. [Unix Domain Sockets Deep Dive](#unix-domain-sockets-deep-dive)
2. [Directory Watching Mechanisms](#directory-watching-mechanisms)
3. [Lifecycle and Cleanup Strategies](#lifecycle-and-cleanup-strategies)
4. [Path Conventions](#path-conventions)
5. [Encoding Metadata](#encoding-metadata)
6. [Real-World Implementations](#real-world-implementations)
7. [Simplicity vs Robustness Analysis](#simplicity-vs-robustness-analysis)
8. [When to Use This Pattern](#when-to-use-this-pattern)

---

## Unix Domain Sockets Deep Dive

### What Are Unix Domain Sockets?

Unix domain sockets (UDS), also called AF_UNIX or IPC sockets, enable inter-process communication on the same host machine. Unlike TCP/IP sockets that use IP addresses and ports, Unix sockets use filesystem paths.

```c
// Creating a Unix domain socket
#include <sys/socket.h>
#include <sys/un.h>

int server_fd = socket(AF_UNIX, SOCK_STREAM, 0);

struct sockaddr_un addr;
memset(&addr, 0, sizeof(addr));
addr.sun_family = AF_UNIX;
strncpy(addr.sun_path, "/var/run/myapp.sock", sizeof(addr.sun_path) - 1);

bind(server_fd, (struct sockaddr*)&addr, sizeof(addr));
listen(server_fd, 5);
```

### Performance: Unix Sockets vs TCP Loopback

Unix sockets are **significantly faster** than TCP for local communication:

| Metric | Unix Socket | TCP Loopback | Improvement |
|--------|-------------|--------------|-------------|
| Latency | Lower | Higher | ~40% reduction |
| Throughput | Higher | Lower | ~67% improvement |
| Small transactions/sec | 79,000 | 47,300 | 67% more |

**Why the difference?**
- Unix sockets bypass the entire TCP/IP network stack
- No packet headers, checksums, or routing
- No TCP congestion control or sliding window overhead
- Direct kernel-level memory copying between processes

PostgreSQL benchmarks show **30% improvement** using Unix sockets over TCP loopback.

### Socket Types

Unix sockets support two primary modes:

1. **SOCK_STREAM** - Connection-oriented, reliable byte streams (like TCP)
2. **SOCK_DGRAM** - Connectionless datagrams (like UDP, but reliable on local system)

### The SO_REUSEADDR Problem

**Critical difference from TCP:** Unix domain sockets do **not** support `SO_REUSEADDR`.

With TCP:
```c
int opt = 1;
setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &opt, sizeof(opt));
// Can immediately rebind to same port after close
```

With Unix sockets:
```c
// This has NO EFFECT for AF_UNIX!
setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &opt, sizeof(opt));
// bind() will fail with EADDRINUSE if socket file exists
```

**The only solution:** You must `unlink()` the socket file before binding.

### Permission-Based Security

Unix sockets use standard filesystem permissions for access control:

```c
// Create socket with restricted permissions
int server_fd = socket(AF_UNIX, SOCK_STREAM, 0);
umask(0077);  // Owner-only access
bind(server_fd, ...);
// Result: srw------- (0600) - only owner can connect

// Or set permissions after creation
chmod("/var/run/myapp.sock", 0660);  // Owner + group access
chown("/var/run/myapp.sock", uid, gid);
```

**Permission recommendations:**
- `0600` - Only socket owner can connect
- `0660` - Owner and group members can connect
- `0666` - **Avoid** - World-writable is a security risk

**Important:** On some BSD systems, socket permissions are ignored. Don't rely solely on this for security.

### Linux Abstract Namespace Sockets

Linux offers an alternative: **abstract namespace sockets** that don't create filesystem entries.

```c
struct sockaddr_un addr;
addr.sun_family = AF_UNIX;
addr.sun_path[0] = '\0';  // Null byte indicates abstract namespace
strcpy(&addr.sun_path[1], "myapp");  // Name follows null byte
```

**Advantages:**
- No filesystem cleanup needed
- Automatically disappears when socket closes
- No stale socket file problem

**Disadvantages:**
- Linux-only (not portable to macOS/BSD)
- No filesystem permissions - must implement app-level auth
- Cannot use standard tools to inspect (`ls`, `rm`)
- Less secure by default

**Display convention:** Tools like `ss` and `netstat` show abstract sockets with `@` prefix:
```bash
$ ss -x | grep myapp
u_str ESTAB 0 0 @myapp 12345 * 12346
```

---

## Directory Watching Mechanisms

Directory watching enables "reactive" service discovery - get notified when services appear/disappear.

### Platform-Specific APIs

| Platform | API | Granularity | Resource Usage |
|----------|-----|-------------|----------------|
| Linux | inotify | Per-directory | One FD per directory |
| macOS | FSEvents | Per-tree | One FD per tree |
| BSD/macOS | kqueue | Per-file | One FD **per file** |
| Windows | ReadDirectoryChangesW | Per-directory | Handle per directory |
| Fallback | stat() polling | Any | CPU-intensive |

### Linux: inotify

```c
#include <sys/inotify.h>

// Initialize
int inotify_fd = inotify_init1(IN_NONBLOCK);

// Watch a directory for new sockets
int watch_fd = inotify_add_watch(
    inotify_fd,
    "/run/myapp/",
    IN_CREATE | IN_DELETE | IN_MOVED_TO | IN_MOVED_FROM
);

// Event loop
char buf[4096] __attribute__((aligned(__alignof__(struct inotify_event))));
while (1) {
    ssize_t len = read(inotify_fd, buf, sizeof(buf));
    if (len <= 0) continue;

    char *ptr = buf;
    while (ptr < buf + len) {
        struct inotify_event *event = (struct inotify_event *)ptr;

        if (event->mask & IN_CREATE) {
            printf("Created: %s\n", event->name);
            // Check if it's a socket, attempt connection
        }
        if (event->mask & IN_DELETE) {
            printf("Deleted: %s\n", event->name);
            // Remove from service registry
        }

        ptr += sizeof(struct inotify_event) + event->len;
    }
}
```

**Limitations:**
- Not recursive - must add watch for each subdirectory
- Queue can overflow if events generated faster than consumed
- Network filesystems (NFS) don't support inotify
- System limit: `/proc/sys/fs/inotify/max_user_watches` (default ~8192)

**Handling queue overflow:**
```c
if (event->mask & IN_Q_OVERFLOW) {
    // Events were lost! Must rescan directory
    rescan_directory("/run/myapp/");
}
```

### macOS: FSEvents

```c
#include <CoreServices/CoreServices.h>

void callback(
    ConstFSEventStreamRef stream,
    void *context,
    size_t numEvents,
    void *eventPaths,
    const FSEventStreamEventFlags eventFlags[],
    const FSEventStreamEventId eventIds[])
{
    char **paths = (char **)eventPaths;
    for (size_t i = 0; i < numEvents; i++) {
        printf("Change in: %s\n", paths[i]);
        // Note: FSEvents gives directory-level granularity
        // You need to scan the directory to find specific changes
    }
}

// Create and start stream
CFStringRef path = CFSTR("/Users/myuser/.local/run/myapp");
CFArrayRef paths = CFArrayCreate(NULL, (const void **)&path, 1, NULL);

FSEventStreamRef stream = FSEventStreamCreate(
    NULL, callback, NULL, paths,
    kFSEventStreamEventIdSinceNow,
    0.5,  // Latency in seconds
    kFSEventStreamCreateFlagFileEvents
);

FSEventStreamScheduleWithRunLoop(stream, CFRunLoopGetCurrent(), kCFRunLoopDefaultMode);
FSEventStreamStart(stream);
CFRunLoopRun();
```

**Advantages over kqueue:**
- Watches entire directory trees with single FD
- No file descriptor exhaustion
- Coalesces rapid changes

**Limitations:**
- macOS only
- Directory-level granularity by default
- Slight latency (configurable)

### BSD/macOS: kqueue

```c
#include <sys/event.h>

int kq = kqueue();
int fd = open("/var/run/myapp.sock", O_RDONLY);

struct kevent change;
EV_SET(&change, fd, EVFILT_VNODE,
       EV_ADD | EV_ENABLE | EV_CLEAR,
       NOTE_DELETE | NOTE_WRITE | NOTE_RENAME,
       0, NULL);

kevent(kq, &change, 1, NULL, 0, NULL);

// Event loop
struct kevent event;
while (1) {
    int n = kevent(kq, NULL, 0, &event, 1, NULL);
    if (n > 0) {
        if (event.fflags & NOTE_DELETE) {
            printf("File deleted\n");
        }
    }
}
```

**Critical limitation:** kqueue requires an open file descriptor per watched file. This scales **poorly** - systems often limit FDs to 256-1024 by default.

### Cross-Platform: fsnotify (Go)

```go
package main

import (
    "log"
    "github.com/fsnotify/fsnotify"
)

func main() {
    watcher, _ := fsnotify.NewWatcher()
    defer watcher.Close()

    go func() {
        for {
            select {
            case event := <-watcher.Events:
                if event.Op&fsnotify.Create == fsnotify.Create {
                    log.Printf("New service: %s", event.Name)
                }
                if event.Op&fsnotify.Remove == fsnotify.Remove {
                    log.Printf("Service gone: %s", event.Name)
                }
            case err := <-watcher.Errors:
                log.Printf("Error: %v", err)
            }
        }
    }()

    watcher.Add("/run/myapp/")
    select {}  // Block forever
}
```

### Polling Fallback

When native APIs aren't available (network filesystems, old systems):

```python
import os
import time

def poll_directory(path, interval=1.0):
    known_files = set(os.listdir(path))

    while True:
        time.sleep(interval)
        current_files = set(os.listdir(path))

        added = current_files - known_files
        removed = known_files - current_files

        for f in added:
            print(f"New: {f}")
        for f in removed:
            print(f"Gone: {f}")

        known_files = current_files
```

**Tradeoffs:**
- Works everywhere
- CPU-intensive at high frequencies
- Misses rapid create-delete sequences
- Adds latency equal to poll interval

---

## Lifecycle and Cleanup Strategies

The hardest problem with file-based service discovery: **stale artifacts**.

### The Stale Socket Problem

```
1. Process A creates /var/run/myapp.sock and binds
2. Process A crashes (SIGKILL, OOM, power failure)
3. Socket file still exists but nothing is listening
4. Process B tries to start, sees socket, thinks A is running
   OR
   Process B tries to bind, gets EADDRINUSE
```

### Strategy 1: Unlink Before Bind

```c
unlink("/var/run/myapp.sock");  // Remove if exists
bind(fd, &addr, sizeof(addr));
```

**Problem:** If two processes start simultaneously:
```
Process A: unlink()
Process B: unlink()  <- Removes A's socket!
Process A: bind()
Process B: bind()    <- EADDRINUSE
```

### Strategy 2: Lock File Pattern (Recommended)

Use a separate lock file that survives crashes:

```c
#define LOCK_PATH "/var/run/myapp.lock"
#define SOCK_PATH "/var/run/myapp.sock"

int acquire_lock() {
    int lock_fd = open(LOCK_PATH, O_RDONLY | O_CREAT, 0600);
    if (lock_fd < 0) return -1;

    // Non-blocking exclusive lock
    if (flock(lock_fd, LOCK_EX | LOCK_NB) != 0) {
        close(lock_fd);
        return -1;  // Another process holds the lock
    }

    return lock_fd;  // Keep open to hold lock
}

int main() {
    int lock_fd = acquire_lock();
    if (lock_fd < 0) {
        fprintf(stderr, "Another instance is running\n");
        exit(1);
    }

    // Safe to unlink - we hold the lock
    unlink(SOCK_PATH);

    int sock_fd = socket(AF_UNIX, SOCK_STREAM, 0);
    bind(sock_fd, ...);
    listen(sock_fd, 5);

    // Lock released automatically when process exits
    // (even on crash, kernel releases flock)
}
```

**Why this works:**
- `flock()` is released by kernel when process exits (even on crash)
- Lock file persists but that's fine - we never unlink it
- No race condition between processes

### Strategy 3: Connect-to-Test

Before creating, try connecting to existing socket:

```c
int test_socket_live(const char *path) {
    int fd = socket(AF_UNIX, SOCK_STREAM, 0);
    struct sockaddr_un addr = {.sun_family = AF_UNIX};
    strncpy(addr.sun_path, path, sizeof(addr.sun_path) - 1);

    int result = connect(fd, (struct sockaddr*)&addr, sizeof(addr));
    close(fd);

    return (result == 0);  // true if something is listening
}

// Usage
if (test_socket_live(SOCK_PATH)) {
    fprintf(stderr, "Service already running\n");
    exit(1);
}
unlink(SOCK_PATH);  // Stale, safe to remove
bind(...);
```

**Limitation:** Race condition still exists between test and bind.

### Strategy 4: PID File Validation

```c
#define PID_PATH "/var/run/myapp.pid"

int is_process_alive(pid_t pid) {
    return kill(pid, 0) == 0;  // Signal 0 tests existence
}

int check_pid_file() {
    FILE *f = fopen(PID_PATH, "r");
    if (!f) return 0;  // No PID file

    pid_t pid;
    fscanf(f, "%d", &pid);
    fclose(f);

    return is_process_alive(pid);
}

void write_pid_file() {
    FILE *f = fopen(PID_PATH, "w");
    fprintf(f, "%d\n", getpid());
    fclose(f);
}

// On startup
if (check_pid_file()) {
    fprintf(stderr, "Already running\n");
    exit(1);
}
write_pid_file();
// ... create socket ...
```

**Limitations:**
- PID reuse: PIDs wrap around and get reused
- Only validates process existence, not that it's the right process
- PID file can be stale if process crashes

### Strategy 5: Systemd RuntimeDirectory

Let systemd manage the directory:

```ini
[Service]
RuntimeDirectory=myapp
RuntimeDirectoryMode=0755
ExecStart=/usr/bin/myapp --socket=%t/myapp/app.sock
```

- `%t` expands to `/run` (system) or `/run/user/$UID` (user)
- Directory created on service start
- **Directory deleted on service stop** - automatic cleanup!

### Cleanup on Exit

Always try to clean up, but don't rely on it:

```c
volatile sig_atomic_t running = 1;

void signal_handler(int sig) {
    running = 0;
}

int main() {
    signal(SIGINT, signal_handler);
    signal(SIGTERM, signal_handler);

    // Create socket...

    while (running) {
        // Main loop
    }

    // Cleanup
    close(sock_fd);
    unlink(SOCK_PATH);
    unlink(PID_PATH);

    return 0;
}
```

---

## Path Conventions

### Standard Locations

| Path | Purpose | Scope | Lifetime |
|------|---------|-------|----------|
| `/run` | System runtime data | System-wide | Until reboot |
| `/var/run` | Symlink to `/run` | System-wide | Until reboot |
| `/run/user/$UID` | Per-user runtime | User | Until logout |
| `~/.local/run` | User apps (non-standard) | User | Persistent |
| `/tmp` | Temporary files | Shared | Variable |

### XDG Runtime Directory

The `XDG_RUNTIME_DIR` specification (typically `/run/user/$UID`):

**Requirements:**
- Must be on local filesystem (not NFS)
- Owned by user with mode `0700`
- Created on first login, removed on last logout
- Subject to periodic cleanup (modified every 6 hours or set sticky bit)

**Accessing in code:**
```c
const char *runtime_dir = getenv("XDG_RUNTIME_DIR");
if (!runtime_dir) {
    // Fallback for systems without systemd
    runtime_dir = "/tmp";
}

char sock_path[PATH_MAX];
snprintf(sock_path, sizeof(sock_path), "%s/myapp.sock", runtime_dir);
```

**Common contents of `/run/user/1000`:**
```
/run/user/1000/
├── bus                    # D-Bus session socket
├── pulse/                 # PulseAudio
│   └── native
├── gnupg/
├── systemd/
│   └── private
├── dconf/
└── myapp.sock            # Your application
```

### Recommended Structure for Discovery

```
/run/myapp/                    # System service directory
├── main.sock                  # Primary control socket
├── main.pid                   # PID file
├── workers/                   # Worker discovery directory
│   ├── worker-1.sock
│   ├── worker-2.sock
│   └── worker-3.sock
└── metadata.json              # Service metadata

/run/user/1000/myapp/          # Per-user instance
├── instance.sock
└── instance.pid
```

### Docker's Convention

Docker uses a single well-known path:
```
/var/run/docker.sock
```

This is mounted into containers that need Docker access:
```bash
docker run -v /var/run/docker.sock:/var/run/docker.sock myimage
```

---

## Encoding Metadata

How do you communicate more than just "service exists"?

### Option 1: Filename Encoding

Embed metadata in the socket/file name:

```
/run/myapp/workers/
├── worker-pid12345-port8080.sock
├── worker-pid12346-port8081.sock
└── worker-pid12347-port8082.sock
```

**Parsing:**
```python
import re
import os

def parse_worker_socket(filename):
    match = re.match(r'worker-pid(\d+)-port(\d+)\.sock', filename)
    if match:
        return {'pid': int(match.group(1)), 'port': int(match.group(2))}
    return None

workers = []
for f in os.listdir('/run/myapp/workers/'):
    info = parse_worker_socket(f)
    if info:
        info['socket'] = f'/run/myapp/workers/{f}'
        workers.append(info)
```

**Pros:** Simple, atomic, no extra files
**Cons:** Limited metadata, parsing complexity, filename length limits

### Option 2: Sidecar JSON/YAML Files

Pair each socket with a metadata file:

```
/run/myapp/
├── service.sock
├── service.json          # Metadata for service.sock
```

**service.json:**
```json
{
  "version": "1.2.3",
  "pid": 12345,
  "started_at": "2024-01-15T10:30:00Z",
  "endpoints": {
    "http": 8080,
    "grpc": 9090
  },
  "health_check": "/health",
  "tags": ["primary", "us-west-2"]
}
```

**Discovery code:**
```python
import json
import os

def discover_services(base_path):
    services = []
    for f in os.listdir(base_path):
        if f.endswith('.sock'):
            json_path = os.path.join(base_path, f.replace('.sock', '.json'))
            if os.path.exists(json_path):
                with open(json_path) as jf:
                    meta = json.load(jf)
                    meta['socket'] = os.path.join(base_path, f)
                    services.append(meta)
    return services
```

**Pros:** Rich metadata, human-readable, easy to update
**Cons:** Two files to manage, potential inconsistency

### Option 3: Prometheus-Style File-Based Discovery

Single JSON file listing all targets:

```json
[
  {
    "targets": ["localhost:9100", "localhost:9101"],
    "labels": {
      "job": "node",
      "env": "production"
    }
  },
  {
    "targets": ["unix:///run/myapp/worker1.sock"],
    "labels": {
      "job": "myapp-worker",
      "instance": "worker1"
    }
  }
]
```

Prometheus watches this file and automatically updates scrape targets.

### Option 4: Extended Attributes (xattr)

Store metadata directly on the socket file:

```bash
# Set attributes
setfattr -n user.version -v "1.2.3" /run/myapp/service.sock
setfattr -n user.port -v "8080" /run/myapp/service.sock

# Get attributes
getfattr -n user.version /run/myapp/service.sock
```

**In code (Linux):**
```c
#include <sys/xattr.h>

// Set
setxattr("/run/myapp/service.sock", "user.version", "1.2.3", 5, 0);

// Get
char value[256];
ssize_t len = getxattr("/run/myapp/service.sock", "user.version", value, sizeof(value));
```

**Pros:** Atomic with file, no extra files
**Cons:**
- Limited size (~4KB typical)
- Not all filesystems support xattr
- tmpfs (used for `/run`) may not preserve xattr
- NFS support limited
- Not portable across OS

### Option 5: Socket Protocol Handshake

First message on socket connection contains metadata:

```python
# Server
conn, addr = sock.accept()
metadata = json.dumps({
    'version': '1.2.3',
    'capabilities': ['streaming', 'batch']
})
conn.send(f'{len(metadata):08d}'.encode() + metadata.encode())

# Client
sock.connect('/run/myapp/service.sock')
length = int(sock.recv(8).decode())
metadata = json.loads(sock.recv(length).decode())
```

**Pros:** Guaranteed fresh, no filesystem metadata
**Cons:** Must connect to discover, overhead per connection

---

## Real-World Implementations

### Docker Daemon (`/var/run/docker.sock`)

**Architecture:**
```
docker CLI  ──HTTP/JSON──>  /var/run/docker.sock  ──>  dockerd
```

**Key design choices:**
- Single well-known socket path
- RESTful HTTP API over Unix socket
- Permissions control via file ownership (root:docker)
- Metadata via API calls, not filesystem

**Accessing the API:**
```bash
# List containers
curl -s --unix-socket /var/run/docker.sock http://localhost/containers/json

# Create container
curl -s --unix-socket /var/run/docker.sock \
  -X POST \
  -H "Content-Type: application/json" \
  -d '{"Image": "nginx"}' \
  http://localhost/containers/create
```

### Supervisord

**Configuration:**
```ini
[unix_http_server]
file=/var/run/supervisor.sock
chmod=0700
chown=nobody:nogroup
username=user
password={SHA}hash

[supervisorctl]
serverurl=unix:///var/run/supervisor.sock
```

**Key features:**
- XML-RPC over Unix socket
- Optional authentication
- chmod/chown for access control
- Socket manager for FastCGI workers

### systemd User Services

**How /run/user/$UID works:**
1. `pam_systemd` creates directory on first login
2. Sets `XDG_RUNTIME_DIR` environment variable
3. Directory owned by user with mode 0700
4. Removed on last session logout

**User service example:**
```ini
# ~/.config/systemd/user/myapp.service
[Service]
ExecStart=/usr/bin/myapp --socket=%t/myapp.sock
RuntimeDirectory=myapp

[Install]
WantedBy=default.target
```

`%t` expands to `$XDG_RUNTIME_DIR` for user services.

### Nginx Upstream with Unix Sockets

**nginx.conf:**
```nginx
upstream backend {
    server unix:/var/run/gunicorn.sock weight=5;
    server unix:/var/run/gunicorn2.sock weight=3;
    server 127.0.0.1:8000 backup;  # TCP fallback
}

server {
    listen 80;

    location / {
        proxy_pass http://backend;
        proxy_set_header Host $host;
    }
}
```

**Gunicorn configuration:**
```bash
gunicorn --bind unix:/var/run/gunicorn.sock myapp:app
```

**Benefits:**
- Nginx doesn't need to know about ports
- Easy to add/remove workers
- Can mix Unix sockets and TCP

---

## Simplicity vs Robustness Analysis

### What File-Based Discovery Sacrifices

| Feature | Consul/etcd | File-Based |
|---------|-------------|------------|
| Cross-machine discovery | Native | Not possible |
| Health checking | Built-in | Manual |
| Consistency guarantees | Strong (Raft) | Eventual/none |
| Atomic updates | Yes | No |
| Watch/subscribe | Global | Local only |
| Metadata | Rich key-value | Limited |
| Failure detection | Automatic | Manual |
| Load balancing info | Integrated | Separate |

### The Fundamental Limitation

**File-based discovery is inherently local.** You cannot watch `/run/myapp/` on another machine without additional infrastructure (NFS, network filesystem, sync daemon).

### Failure Modes

**With Consul/etcd:**
```
Service crashes → Health check fails → Registry updated → Clients notified
(Automatic, milliseconds to seconds)
```

**With file-based:**
```
Service crashes → Socket file remains → Clients try to connect → Connection refused
(Manual detection required)
```

### When Simplicity Wins

1. **Single-machine deployments** - No network coordination needed
2. **Container orchestration** - Kubernetes handles cross-node, file-based for intra-pod
3. **Development environments** - Easy to inspect with `ls`, `cat`
4. **Bootstrapping** - Discover local services before network is up
5. **Security-sensitive** - No network attack surface
6. **Embedded systems** - No external dependencies

### When Robustness Wins

1. **Distributed systems** - Multiple machines must coordinate
2. **Dynamic scaling** - Frequent service additions/removals
3. **High availability** - Need automatic failover
4. **Service mesh** - Complex routing requirements
5. **Multi-datacenter** - Geographic distribution

### Hybrid Approaches

Many systems use **both**:

```
┌─────────────────────────────────────────────────┐
│  Machine A                                      │
│  ┌──────────┐    ┌──────────┐                  │
│  │ Service1 │────│ Local    │                  │
│  └──────────┘    │ Socket   │                  │
│  ┌──────────┐    │ Dir      │──── Consul ──────│
│  │ Service2 │────│/run/myapp│     Agent        │
│  └──────────┘    └──────────┘       │          │
└─────────────────────────────────────│──────────┘
                                      │
┌─────────────────────────────────────│──────────┐
│  Machine B                          │          │
│  ┌──────────┐    ┌──────────┐      │          │
│  │ Service3 │────│/run/myapp│──────│          │
│  └──────────┘    └──────────┘                  │
└─────────────────────────────────────────────────┘
```

- **Intra-machine:** Unix sockets (fast, secure)
- **Inter-machine:** Consul/etcd (reliable, global)

---

## When to Use This Pattern

### Use File/Socket Discovery When:

1. **All services run on one machine**
2. **You control the deployment environment** (can set up directories, permissions)
3. **Simplicity is paramount** (no external dependencies)
4. **Performance matters** (Unix sockets are faster)
5. **Security requires minimal attack surface** (no network ports)
6. **Bootstrapping** (services need to find each other before network ready)

### Avoid File/Socket Discovery When:

1. **Services span multiple machines**
2. **Dynamic scaling** is frequent
3. **Strong consistency** required
4. **Automatic failure recovery** needed
5. **Rich service metadata** must be queryable
6. **Service mesh features** needed (circuit breaking, retries, etc.)

### Implementation Checklist

- [ ] Choose socket location (`/run`, `XDG_RUNTIME_DIR`, etc.)
- [ ] Implement lock file pattern for stale socket handling
- [ ] Set appropriate permissions (0600/0660)
- [ ] Decide metadata format (filename, sidecar, xattr)
- [ ] Implement directory watcher with platform-appropriate API
- [ ] Handle watcher failures (queue overflow, FD limits)
- [ ] Add connect-timeout for health checking
- [ ] Document cleanup procedure for ops
- [ ] Consider systemd RuntimeDirectory for automatic cleanup

---

## References

- [Unix Domain Socket Wikipedia](https://en.wikipedia.org/wiki/Unix_domain_socket)
- [Reusing UNIX domain socket (SO_REUSEADDR for AF_UNIX)](https://gavv.net/articles/unix-socket-reuse/)
- [Docker Tips: /var/run/docker.sock](https://betterprogramming.pub/about-var-run-docker-sock-3bfd276e12fd)
- [Supervisor Configuration](https://supervisord.org/configuration.html)
- [Linux inotify(7) man page](https://man7.org/linux/man-pages/man7/inotify.7.html)
- [fsnotify - Cross-platform filesystem notifications](https://github.com/fsnotify/fsnotify)
- [Prometheus file-based service discovery](https://prometheus.io/docs/guides/file-sd/)
- [XDG Base Directory Specification](https://wiki.archlinux.org/title/XDG_Base_Directory)
- [systemd RuntimeDirectory](https://www.freedesktop.org/software/systemd/man/systemd.exec.html)
- [unix(7) - Linux manual page (abstract sockets)](https://www.man7.org/linux/man-pages/man7/unix.7.html)
