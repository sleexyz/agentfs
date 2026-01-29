# Socket Activation: Zero-Coordination Service Discovery

**The inetd Model**: The OS/init system owns the listening socket and passes it to applications. Apps don't bind - they inherit an already-listening socket.

## The Core Insight

Traditional service startup:
```
Service starts -> Creates socket -> Binds to port -> Listens -> Ready
```

Socket activation inverts this:
```
Init system creates socket -> Binds -> Listens -> [connection arrives] -> Service starts with socket
```

This seemingly simple inversion enables:
1. **On-demand spawning** - Services start only when needed
2. **Parallel boot** - No dependency ordering required
3. **Zero-downtime restarts** - Socket survives service restart
4. **Privilege separation** - Init binds privileged ports, service runs unprivileged

---

## Part 1: File Descriptor Passing Mechanisms

### The fork+exec Model (Traditional)

When a process forks and execs, file descriptors survive by default:

```c
// Parent process
int listen_fd = socket(AF_INET, SOCK_STREAM, 0);
bind(listen_fd, ...);
listen(listen_fd, 128);

pid_t pid = fork();
if (pid == 0) {
    // Child inherits listen_fd automatically
    // File descriptor number is preserved
    execve("/path/to/service", argv, envp);
    // Service receives listen_fd at same FD number
}
```

**Key insight**: After `fork()`, child has copies of all parent's file descriptors pointing to the same kernel objects. After `exec()`, these persist unless marked `FD_CLOEXEC`.

The **O_CLOEXEC** / **FD_CLOEXEC** flag is critical:
- Set: FD closes automatically on exec (safe default)
- Clear: FD passes through to new program (explicit handoff)

### SCM_RIGHTS: Runtime FD Passing

For processes not in a fork relationship, Unix domain sockets support `SCM_RIGHTS`:

```c
// Sender: pass file descriptor over Unix socket
struct msghdr msg = {0};
struct cmsghdr *cmsg;
char buf[CMSG_SPACE(sizeof(int))];

msg.msg_control = buf;
msg.msg_controllen = sizeof(buf);

cmsg = CMSG_FIRSTHDR(&msg);
cmsg->cmsg_level = SOL_SOCKET;
cmsg->cmsg_type = SCM_RIGHTS;
cmsg->cmsg_len = CMSG_LEN(sizeof(int));

// Copy the file descriptor into the message
*((int *)CMSG_DATA(cmsg)) = fd_to_send;

// Must send at least 1 byte of "real" data
char dummy = 'x';
struct iovec iov = { .iov_base = &dummy, .iov_len = 1 };
msg.msg_iov = &iov;
msg.msg_iovlen = 1;

sendmsg(unix_socket, &msg, 0);
```

**Real-world uses**:
- HAProxy hitless reloads (since 1.8)
- Envoy hot restarts
- Cloudflare TLS 1.3 migration between nginx and Go

---

## Part 2: systemd Socket Activation

### The Protocol

systemd passes sockets via environment variables and file descriptors:

| Variable | Purpose |
|----------|---------|
| `LISTEN_PID` | Process ID that should handle these FDs |
| `LISTEN_FDS` | Count of file descriptors passed |
| `LISTEN_FDNAMES` | Colon-separated names (optional) |

File descriptors start at **FD 3** (`SD_LISTEN_FDS_START`):
- FD 0, 1, 2 = stdin, stdout, stderr (standard)
- FD 3 = first activated socket
- FD 4, 5, ... = additional sockets

### Using sd_listen_fds()

```c
#include <systemd/sd-daemon.h>

int main(void) {
    int n = sd_listen_fds(1);  // 1 = unset env vars after reading

    if (n < 0) {
        fprintf(stderr, "Failed to get fds: %s\n", strerror(-n));
        return 1;
    }

    if (n == 0) {
        // No socket activation - create socket ourselves
        int fd = socket(AF_INET, SOCK_STREAM, 0);
        bind(fd, ...);
        listen(fd, 128);
        // ... use fd
    } else {
        // Socket activated!
        int fd = SD_LISTEN_FDS_START;  // FD 3
        // Already bound and listening - just accept()
        // ... use fd
    }
}
```

**Compile**: `gcc -o myservice myservice.c $(pkg-config --cflags --libs libsystemd)`

### Socket Unit File Example

**`/etc/systemd/system/myservice.socket`**:
```ini
[Unit]
Description=My Service Socket

[Socket]
# TCP socket on port 8080
ListenStream=0.0.0.0:8080

# Or Unix socket
# ListenStream=/run/myservice.sock

# Name for sd_listen_fds_with_names()
FileDescriptorName=main

# Single service handles all connections (default)
Accept=no

[Install]
WantedBy=sockets.target
```

**`/etc/systemd/system/myservice.service`**:
```ini
[Unit]
Description=My Service
Requires=myservice.socket

[Service]
ExecStart=/usr/bin/myservice
# Service is socket-activated, not started at boot
```

### Accept=yes Mode (inetd-style)

For per-connection service instances:

**`/etc/systemd/system/echo.socket`**:
```ini
[Socket]
ListenStream=7
Accept=yes

[Install]
WantedBy=sockets.target
```

**`/etc/systemd/system/echo@.service`** (template - note the `@`):
```ini
[Service]
ExecStart=/bin/cat
StandardInput=socket
StandardOutput=socket
```

Each connection spawns a new service instance. stdin/stdout are the socket.

### Key Socket Options

| Option | Purpose |
|--------|---------|
| `ListenStream` | TCP / Unix stream socket |
| `ListenDatagram` | UDP socket |
| `ListenSequentialPacket` | SCTP-style sequenced packets |
| `Accept=no` | One service, multiple connections (default) |
| `Accept=yes` | One service instance per connection |
| `FileDescriptorName` | Name for multi-socket services |
| `MaxConnections` | Limit concurrent connections |
| `BindIPv6Only` | Dual-stack control |

---

## Part 3: launchd Socket Activation (macOS)

### The API

macOS uses a different mechanism - the service calls `launch_activate_socket()`:

```c
#include <launch.h>

int main(void) {
    int *fds = NULL;
    size_t cnt = 0;

    int err = launch_activate_socket("Listener", &fds, &cnt);

    if (err == ESRCH) {
        // Not running under launchd - create socket manually
    } else if (err == 0) {
        // Socket activated! fds[0..cnt-1] are listening sockets
        for (size_t i = 0; i < cnt; i++) {
            // Each fd is already bound and listening
            // May have multiple (e.g., IPv4 + IPv6)
        }
        free(fds);  // Caller must free
    }
}
```

**Error codes**:
- `ENOENT`: Socket name not in plist
- `ESRCH`: Process not managed by launchd
- `EALREADY`: Socket already activated (can only call once)

### launchd Plist Configuration

**`~/Library/LaunchAgents/com.example.myservice.plist`**:
```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.example.myservice</string>

    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/myservice</string>
    </array>

    <key>Sockets</key>
    <dict>
        <key>Listener</key>
        <dict>
            <key>SockServiceName</key>
            <string>8080</string>
            <key>SockType</key>
            <string>stream</string>
            <key>SockFamily</key>
            <string>IPv4</string>
        </dict>
    </dict>
</dict>
</plist>
```

### Key Socket Options

| Key | Purpose |
|-----|---------|
| `SockServiceName` | Port number or service name from /etc/services |
| `SockType` | `stream` (TCP), `dgram` (UDP), `seqpacket` |
| `SockFamily` | `IPv4`, `IPv6`, or omit for both |
| `SockNodeName` | Bind address (`localhost`, etc.) |
| `SockPassive` | `true` for listen (default), `false` for connect |

---

## Part 4: The Ancestors - inetd and xinetd

### inetd (1986)

The original "super-server". Configuration in `/etc/inetd.conf`:

```
# service  socket  proto  wait  user    server          args
echo       stream  tcp    nowait root   internal
daytime    stream  tcp    nowait root   internal
telnet     stream  tcp    nowait root   /usr/sbin/telnetd telnetd
ssh        stream  tcp    nowait root   /usr/sbin/sshd    sshd -i
```

**How it worked**:
1. inetd reads config, creates sockets for all services
2. Uses `select()` to wait on all sockets
3. On connection: `fork()`, `dup2()` socket to stdin/stdout, `exec()` service
4. Service reads from stdin, writes to stdout

**wait vs nowait**:
- `nowait`: inetd continues listening, spawns per connection (multi-threaded)
- `wait`: inetd hands off socket, service handles all connections (single-threaded)

### xinetd (Extended)

More sophisticated configuration in `/etc/xinetd.d/`:

```
service telnet
{
    socket_type = stream
    protocol    = tcp
    wait        = no
    user        = root
    server      = /usr/sbin/telnetd

    # Security features
    only_from   = 192.168.0.0/24
    no_access   = 192.168.0.100
    access_times = 09:00-17:00

    # Rate limiting
    cps         = 25 30
    instances   = 10
}
```

**Advantages over inetd**:
- Per-service configuration files
- Built-in access control (TCP wrappers integration)
- Rate limiting
- Time-based access
- Better logging

---

## Part 5: Alternative Implementations

### ucspi-tcp (DJB)

Dan Bernstein's Unix Client-Server Program Interface:

```bash
# tcpserver: accept connections, run program with socket as stdin/stdout
tcpserver -v 0.0.0.0 8080 /usr/local/bin/myservice

# With privilege dropping and connection limits
tcpserver -v -c 100 -u 1000 -g 1000 0.0.0.0 80 ./httpd
```

**Environment variables set for spawned service**:
- `TCPREMOTEIP`, `TCPREMOTEPORT`: Client address
- `TCPLOCALIP`, `TCPLOCALPORT`: Server address
- `TCPREMOTEHOST`: Reverse DNS (if -h)

**Philosophy**: One tool, one job. `tcpserver` only handles TCP. Compose with:
- `tcprules`: Access control
- `softlimit`: Resource limits
- `setuidgid`: Privilege dropping

### s6 (skarnet)

s6 takes a different philosophical approach:

> "Socket activation is a marketing word used by systemd advocates that mixes a couple useful architecture concepts and several horrible ideas."

**s6's decomposition**:
1. **s6-ipcserver** / **s6-tcpserver**: Super-servers (like tcpserver)
2. **s6-fdholder-daemon**: Stores file descriptors
3. **s6-svscan**: Process supervision

```bash
# Bind socket, store it, service retrieves it
s6-ipcserver-socketbinder /run/myservice.sock \
  s6-fdholder-store /service/fdholder/s unix:/run/myservice.sock

# Later, service retrieves stored socket
s6-fdholder-retrieve /service/fdholder/s unix:/run/myservice.sock myserverd
```

**Key difference**: s6 keeps socket management out of PID 1. The fd-holder is a separate service.

---

## Part 6: Library Support

### Go (coreos/go-systemd)

```go
import "github.com/coreos/go-systemd/v22/activation"

func main() {
    listeners, err := activation.Listeners(true)
    if err != nil {
        log.Fatal(err)
    }

    if len(listeners) == 0 {
        // Not socket activated - bind ourselves
        ln, _ := net.Listen("tcp", ":8080")
        http.Serve(ln, handler)
    } else {
        // Socket activated
        http.Serve(listeners[0], handler)
    }
}
```

For named sockets:
```go
listeners, _ := activation.ListenersWithNames(true)
for name, lns := range listeners {
    // name matches FileDescriptorName from socket unit
}
```

### Python (python-systemd)

```python
from systemd.daemon import listen_fds
import socket

fds = listen_fds()
if fds:
    # Socket activated
    sock = socket.fromfd(fds[0], socket.AF_INET, socket.SOCK_STREAM)
else:
    # Create our own
    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.bind(('0.0.0.0', 8080))
    sock.listen(128)
```

### Node.js (node-socket-activation)

```javascript
const activation = require('socket-activation');

// Works with both systemd and launchd
const sockets = activation.collect('Listener');
if (sockets.length > 0) {
    server.listen({ fd: sockets[0] });
} else {
    server.listen(8080);
}
```

---

## Part 7: Making Apps Socket-Activation Aware

### Minimal Changes Required

1. **Check for activated sockets** before creating your own
2. **Accept the socket** instead of binding
3. **Fallback** to traditional behavior when not activated

### Compatibility Pattern (C)

```c
int get_listen_socket(const char *addr, int port) {
    int n = sd_listen_fds(0);

    if (n > 0) {
        // Verify it's the right type
        if (sd_is_socket_inet(SD_LISTEN_FDS_START, AF_INET,
                              SOCK_STREAM, 1, port)) {
            return SD_LISTEN_FDS_START;
        }
    }

    // Fallback: create socket
    int fd = socket(AF_INET, SOCK_STREAM, 0);
    // ... bind, listen
    return fd;
}
```

### Verification Functions

libsystemd provides type-checking:
- `sd_is_socket()` - Generic socket check
- `sd_is_socket_inet()` - AF_INET/AF_INET6 with port
- `sd_is_socket_unix()` - AF_UNIX with path
- `sd_is_fifo()` - Named pipe

---

## Part 8: systemd vs launchd Comparison

| Aspect | systemd | launchd |
|--------|---------|---------|
| **Platform** | Linux | macOS |
| **FD Passing** | Environment vars + FD 3+ | API call `launch_activate_socket()` |
| **Configuration** | INI files (`.socket`) | XML plist |
| **Per-connection mode** | `Accept=yes` | Not directly supported |
| **Named sockets** | `FileDescriptorName` | Dictionary key in Sockets |
| **Verification** | `sd_is_socket_*()` | Manual |
| **Library** | libsystemd | System framework |

### systemd Advantages
- Simpler interface (just check environment)
- Rich socket options
- Per-connection spawning built-in
- Extensive documentation

### launchd Advantages
- Integrated with macOS
- Also handles Mach ports, XPC
- Unified with other launch conditions
- No external library needed

---

## Part 9: When to Use Socket Activation

### Good Use Cases

1. **On-demand services**: Services that receive infrequent requests
   - Print services (CUPS)
   - Backup daemons
   - Development servers

2. **Parallel boot**: Eliminate explicit service ordering
   - All services start simultaneously
   - Connections queue until service ready

3. **Zero-downtime upgrades**: Socket survives service restart
   - Upgrade service binary
   - Restart service
   - No connections lost (queued in kernel)

4. **Privilege separation**: Init binds privileged port
   - Web server on port 80
   - Service runs as non-root
   - No capabilities/setuid needed

### Not Ideal For

1. **High-performance servers**: Startup cost matters
   - Long-running services should start at boot
   - Socket activation adds latency on first request

2. **Services with complex initialization**:
   - Database connections
   - Cache warming
   - These should start proactively

3. **Services requiring `SO_REUSEPORT`** load balancing:
   - Multiple workers sharing a port
   - Complex with socket activation

---

## Part 10: The Elegance vs Portability Tension

### The Beautiful Part

Socket activation is **architecturally elegant**:

```
                    +---------+
                    |  Init   |
                    | System  |
                    +----+----+
                         |
         +---------------+---------------+
         |               |               |
    +----v----+    +-----v-----+   +-----v-----+
    | Socket  |    |  Socket   |   |  Socket   |
    | (DB)    |    |  (HTTP)   |   |  (Cache)  |
    +---------+    +-----------+   +-----------+
         |               |               |
         v               v               v
    [queued]        [queued]        [queued]
         |               |               |
    +----v----+    +-----v-----+   +-----v-----+
    | Service |    |  Service  |   |  Service  |
    | (MySQL) |    |  (nginx)  |   |  (Redis)  |
    +---------+    +-----------+   +-----------+
```

- No startup ordering needed
- Dependencies resolved by socket readiness
- Services can crash and restart without connection loss

### The Portability Problem

**Platform fragmentation**:
- Linux: systemd (most distros), or init.d/upstart (legacy)
- macOS: launchd
- BSD: rc.d, OpenRC, s6
- Windows: Nothing comparable

**Library dependencies**:
- systemd: libsystemd (Linux only)
- launchd: macOS system framework
- Portable apps need conditional compilation

### Practical Approach

```c
#if defined(__linux__)
    #include <systemd/sd-daemon.h>
    #define SOCKET_ACTIVATION_SYSTEMD
#elif defined(__APPLE__)
    #include <launch.h>
    #define SOCKET_ACTIVATION_LAUNCHD
#endif

int get_activated_socket(const char *name) {
#ifdef SOCKET_ACTIVATION_SYSTEMD
    int n = sd_listen_fds(0);
    return (n > 0) ? SD_LISTEN_FDS_START : -1;
#elif SOCKET_ACTIVATION_LAUNCHD
    int *fds;
    size_t cnt;
    if (launch_activate_socket(name, &fds, &cnt) == 0) {
        int fd = fds[0];
        free(fds);
        return fd;
    }
    return -1;
#else
    return -1;  // No socket activation support
#endif
}
```

---

## Testing Socket Activation

### systemd-socket-activate

Test without full systemd integration:

```bash
# Single socket
systemd-socket-activate -l 8080 ./myservice

# Multiple sockets with names
systemd-socket-activate \
    -l 8080 --fdname=http \
    -l 8443 --fdname=https \
    ./myservice

# inetd mode (socket as stdin/stdout)
systemd-socket-activate --inetd -l 8080 /bin/cat
```

### Manual Testing

```bash
# Check socket unit status
systemctl status myservice.socket

# View socket file descriptors
systemctl show myservice.socket -p Listen

# Force service start
systemctl start myservice.service

# Check if service received sockets
cat /proc/$(pgrep myservice)/fd/ | head
```

---

## Summary

Socket activation is a powerful pattern that:

1. **Decouples** socket lifecycle from service lifecycle
2. **Enables** on-demand service spawning
3. **Eliminates** startup ordering complexity
4. **Provides** seamless service restarts

The tradeoff is **platform specificity** - each OS has its own mechanism:
- systemd: Environment variables + FD 3+
- launchd: `launch_activate_socket()` API
- inetd/xinetd: stdin/stdout as socket

For portable applications, abstract the mechanism behind a common interface, with fallback to traditional socket creation when not activated.

---

## References

- [sd_listen_fds(3) man page](https://www.freedesktop.org/software/systemd/man/latest/sd_listen_fds.html)
- [systemd.socket(5) man page](https://www.freedesktop.org/software/systemd/man/latest/systemd.socket.html)
- [launch_activate_socket(3) man page](https://keith.github.io/xcode-man-pages/launch_activate_socket.3.html)
- [Socket Activation - systemd for Developers](http://0pointer.de/blog/projects/socket-activation.html)
- [s6 socket activation documentation](https://skarnet.org/software/s6/socket-activation.html)
- [CoreOS go-systemd library](https://github.com/coreos/go-systemd)
- [Know your SCM_RIGHTS - Cloudflare](https://blog.cloudflare.com/know-your-scm_rights/)
- [Integration of a Go service with systemd](https://vincent.bernat.ch/en/blog/2018-systemd-golang-socket-activation)
