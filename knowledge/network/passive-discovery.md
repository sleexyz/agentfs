# Passive Discovery: Observing Apps Without Cooperation

Deep dive into techniques for discovering network services by observing system state rather than requiring explicit registration. This represents the most "magical" approach to service discovery - applications simply work without any instrumentation.

## The Magic vs Complexity Tension

**Magic**: Zero application changes. Services are discovered automatically.
**Complexity**: Requires deep system access, kernel capabilities, or privileged containers.

| Approach | Magic Level | Complexity | Permission Required |
|----------|-------------|------------|---------------------|
| /proc polling | Medium | Low | Read access to /proc |
| LD_PRELOAD | High | Medium | Control over process launch |
| ptrace | High | High | CAP_SYS_PTRACE |
| seccomp-BPF | High | High | CAP_SYS_ADMIN or user_notif |
| eBPF | Very High | Very High | CAP_BPF, CAP_PERFMON |

---

## 1. Intercepting bind() Calls

### LD_PRELOAD Hooking

The LD_PRELOAD environment variable forces a shared library to load before libc, enabling function interception.

**How It Works:**
1. Create a shared library that defines `bind()`
2. Use `dlsym(RTLD_NEXT, "bind")` to get the real function
3. Wrap with custom logic before/after the real call

```c
#define _GNU_SOURCE
#include <dlfcn.h>
#include <sys/socket.h>
#include <netinet/in.h>
#include <stdio.h>

// Original bind function pointer
static int (*real_bind)(int, const struct sockaddr*, socklen_t) = NULL;

int bind(int sockfd, const struct sockaddr *addr, socklen_t addrlen) {
    // Lazy initialization
    if (!real_bind) {
        real_bind = dlsym(RTLD_NEXT, "bind");
    }

    // Log the bind attempt
    if (addr->sa_family == AF_INET) {
        struct sockaddr_in *in = (struct sockaddr_in *)addr;
        fprintf(stderr, "[HOOK] bind() port=%d\n", ntohs(in->sin_port));
        // Could notify service registry here
    }

    // Call the real bind
    return real_bind(sockfd, addr, addrlen);
}
```

**Compilation:**
```bash
gcc -fPIC -shared -o bind_hook.so bind_hook.c -ldl
LD_PRELOAD=./bind_hook.so ./my_server
```

**Limitations:**
- Only intercepts libc wrappers, not direct syscalls
- Doesn't work with statically linked binaries
- Programs using `syscall(SYS_bind, ...)` bypass the hook
- Must be set before process starts

**Reference:** [Tudor Brindus - Correct LD_PRELOAD Hooking](https://tbrindus.ca/correct-ld-preload-hooking-libc/)

### ptrace Interception

ptrace allows a tracer process to observe and control another process, including intercepting all system calls.

**How It Works:**
1. Fork and call `PTRACE_TRACEME` in child, or attach with `PTRACE_ATTACH`
2. Use `PTRACE_SYSCALL` to stop on syscall entry/exit
3. Read/modify arguments with `PTRACE_GETREGS`/`PTRACE_SETREGS`

```c
// Simplified ptrace syscall tracing loop
pid_t child = fork();
if (child == 0) {
    ptrace(PTRACE_TRACEME, 0, NULL, NULL);
    execvp(argv[1], &argv[1]);
}

waitpid(child, &status, 0);
ptrace(PTRACE_SETOPTIONS, child, 0, PTRACE_O_TRACESYSGOOD);

while (1) {
    // Continue until next syscall
    ptrace(PTRACE_SYSCALL, child, 0, 0);
    waitpid(child, &status, 0);

    // Read registers to check syscall number
    struct user_regs_struct regs;
    ptrace(PTRACE_GETREGS, child, 0, &regs);

    if (regs.orig_rax == SYS_bind) {
        // regs.rdi = sockfd, regs.rsi = addr pointer, regs.rdx = addrlen
        // Read sockaddr from tracee's memory
        struct sockaddr_in addr;
        // Use PTRACE_PEEKDATA to read addr from regs.rsi
    }
}
```

**Performance:** Very slow - stops twice per syscall (entry and exit). Facebook's [Reverie](https://github.com/facebookexperimental/reverie) framework provides a safer abstraction.

**Reference:** [Phil Eaton - Intercepting Linux Syscalls with ptrace](https://notes.eatonphil.com/2023-10-01-intercepting-and-modifying-linux-system-calls-with-ptrace.html)

### seccomp-BPF with User Notification

Modern seccomp (SECCOMP_RET_USER_NOTIF) can intercept and handle syscalls in userspace.

```c
// Install seccomp filter that notifies on bind()
struct sock_filter filter[] = {
    BPF_STMT(BPF_LD | BPF_W | BPF_ABS,
             offsetof(struct seccomp_data, nr)),
    BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, __NR_bind, 0, 1),
    BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_USER_NOTIF),
    BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ALLOW),
};

// Handler receives notification and can inspect/modify
```

The [force-bind-seccomp](https://github.com/mildred/force-bind-seccomp) project demonstrates using `SECCOMP_RET_USER_NOTIF` to intercept and modify `bind()` calls.

**Reference:** [Kernel seccomp_filter documentation](https://www.kernel.org/doc/html/latest/userspace-api/seccomp_filter.html)

---

## 2. eBPF Socket Observation

eBPF provides the most powerful and lowest-overhead mechanism for observing socket operations.

### Attachment Points

| Program Type | Attachment Point | Use Case |
|--------------|------------------|----------|
| `tracepoint/syscalls/sys_enter_bind` | Syscall entry | Log bind attempts |
| `kprobe/__sys_bind` | Kernel function | Flexible, but unstable API |
| `fentry/__sys_bind` | Kernel function | Lower overhead (5.5+) |
| `BPF_PROG_TYPE_SOCK_OPS` | Socket operations | Connection establishment |
| `BPF_PROG_TYPE_CGROUP_SOCK` | Cgroup sockets | Per-cgroup policy |

### Tracepoint Example (Stable API)

```c
SEC("tracepoint/syscalls/sys_enter_bind")
int trace_bind_entry(struct trace_event_raw_sys_enter *ctx) {
    u64 pid_tgid = bpf_get_current_pid_tgid();
    int sockfd = (int)ctx->args[0];
    struct sockaddr *addr = (struct sockaddr *)ctx->args[1];

    struct sockaddr_in addr_in = {};
    bpf_probe_read_user(&addr_in, sizeof(addr_in), addr);

    if (addr_in.sin_family == AF_INET) {
        struct bind_event_t event = {
            .pid = pid_tgid >> 32,
            .port = bpf_ntohs(addr_in.sin_port),
            .addr = addr_in.sin_addr.s_addr,
        };
        bpf_get_current_comm(&event.comm, sizeof(event.comm));
        bpf_perf_event_output(ctx, &events, BPF_F_CURRENT_CPU,
                             &event, sizeof(event));
    }
    return 0;
}
```

### kprobe with kretprobe (Return Value Correlation)

```c
// Store info on entry
BPF_HASH(infotmp, u32, struct bind_info_t);

SEC("kprobe/__sys_bind")
int kprobe_bind(struct pt_regs *ctx) {
    u32 tgid = bpf_get_current_pid_tgid();
    struct bind_info_t info = {};

    info.sockfd = PT_REGS_PARM1(ctx);
    bpf_probe_read_user(&info.addr, sizeof(info.addr),
                        (void *)PT_REGS_PARM2(ctx));
    bpf_get_current_comm(&info.comm, sizeof(info.comm));

    infotmp.update(&tgid, &info);
    return 0;
}

SEC("kretprobe/__sys_bind")
int kretprobe_bind(struct pt_regs *ctx) {
    u32 tgid = bpf_get_current_pid_tgid();
    struct bind_info_t *infop = infotmp.lookup(&tgid);
    if (!infop) return 0;

    int ret = PT_REGS_RC(ctx);  // Return value
    if (ret == 0) {
        // bind() succeeded - notify service registry
    }
    infotmp.delete(&tgid);
    return 0;
}
```

### fentry/fexit (Modern, Lower Overhead)

Available in kernel 5.5+, fentry/fexit use BPF trampolines instead of breakpoints:

```c
SEC("fentry/__sys_bind")
int BPF_PROG(fentry_bind, int sockfd, struct sockaddr *addr, int addrlen) {
    // Direct parameter access - no PT_REGS macros needed
    struct sockaddr_in addr_in = {};
    bpf_probe_read_user(&addr_in, sizeof(addr_in), addr);
    // ...
    return 0;
}

SEC("fexit/__sys_bind")
int BPF_PROG(fexit_bind, int sockfd, struct sockaddr *addr,
             int addrlen, int ret) {
    // fexit gets BOTH input params AND return value
    if (ret == 0) {
        // Bind succeeded
    }
    return 0;
}
```

**Performance comparison:**
- kprobes: ~100ns overhead per call
- fentry/fexit: ~10ns overhead (10x faster)

### sock_ops for Connection Tracking

```c
struct sock_key {
    __u32 sip;
    __u32 dip;
    __u32 sport;
    __u32 dport;
    __u32 family;
};

struct {
    __uint(type, BPF_MAP_TYPE_SOCKHASH);
    __uint(max_entries, 65535);
    __type(key, struct sock_key);
    __type(value, int);
} sock_ops_map SEC(".maps");

SEC("sockops")
int bpf_sockops(struct bpf_sock_ops *skops) {
    u32 op = skops->op;

    // Track connection establishment (both active and passive)
    if (op != BPF_SOCK_OPS_PASSIVE_ESTABLISHED_CB &&
        op != BPF_SOCK_OPS_ACTIVE_ESTABLISHED_CB)
        return BPF_OK;

    struct sock_key key = {
        .sip = skops->local_ip4,
        .dip = skops->remote_ip4,
        .sport = skops->local_port,
        .dport = bpf_ntohl(skops->remote_port),
        .family = skops->family,
    };

    bpf_sock_hash_update(skops, &sock_ops_map, &key, BPF_NOEXIST);
    return BPF_OK;
}
```

**Key sock_ops callbacks:**
- `BPF_SOCK_OPS_TCP_CONNECT_CB` - Active connection initiation
- `BPF_SOCK_OPS_ACTIVE_ESTABLISHED_CB` - Outbound connection established
- `BPF_SOCK_OPS_PASSIVE_ESTABLISHED_CB` - Inbound connection accepted
- `BPF_SOCK_OPS_STATE_CB` - TCP state changes
- `BPF_SOCK_OPS_TCP_LISTEN_CB` - Socket starts listening

**Reference:** [eBPF sockops tutorial](https://eunomia.dev/tutorials/29-sockops/)

---

## 3. Scanning for Listening Sockets

### /proc/net/tcp Format

```
  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:0277 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345
```

**Field breakdown:**
- `local_address`: `IP:PORT` in hex (little-endian IP, big-endian port)
- `st`: State (0A = LISTEN)
- `uid`: Socket owner
- `inode`: Socket inode (correlate with /proc/*/fd)

**Parsing hex address:**
```bash
# Convert 0100007F:0277 to 127.0.0.1:631
# IP: read bytes in reverse: 7F.00.00.01 = 127.0.0.1
# Port: 0x0277 = 631
```

**AWK parser (when lsof unavailable):**
```awk
function hextodec(str, ret, n, i, k, c) {
    ret = 0; n = length(str)
    for (i = 1; i <= n; i++) {
        c = tolower(substr(str, i, 1))
        k = index("123456789abcdef", c)
        ret = ret * 16 + k
    }
    return ret
}

function getIP(str, ret) {
    ret = hextodec(substr(str, index(str,":")-2, 2))
    for (i = 5; i > 0; i -= 2) {
        ret = ret "." hextodec(substr(str, i, 2))
    }
    ret = ret ":" hextodec(substr(str, index(str,":")+1, 4))
    return ret
}

NR > 1 { print getIP($2) " -> " getIP($3) " state=" $4 }
```

**Reference:** [Kernel /proc/net/tcp documentation](https://www.kernel.org/doc/Documentation/networking/proc_net_tcp.txt)

### ss (Socket Statistics) - NETLINK_SOCK_DIAG

`ss` is much faster than netstat because it uses the kernel's netlink interface instead of parsing /proc files.

**How ss works:**
1. Opens `socket(AF_NETLINK, SOCK_RAW, NETLINK_SOCK_DIAG)`
2. Sends `SOCK_DIAG_BY_FAMILY` requests
3. Receives `inet_diag_msg` responses with socket details

```c
// Simplified netlink request structure
struct {
    struct nlmsghdr nlh;
    struct inet_diag_req_v2 r;
} req = {
    .nlh.nlmsg_len = sizeof(req),
    .nlh.nlmsg_type = SOCK_DIAG_BY_FAMILY,
    .nlh.nlmsg_flags = NLM_F_REQUEST | NLM_F_DUMP,
    .r.sdiag_family = AF_INET,
    .r.sdiag_protocol = IPPROTO_TCP,
    .r.idiag_states = TCPF_LISTEN,  // Only listening sockets
};

send(nl_sock, &req, sizeof(req), 0);
// Receive and parse inet_diag_msg responses
```

**Key advantages:**
- Direct kernel query (no file parsing)
- Filtering done in kernel (reduced data transfer)
- Real-time information

**Reference:** [iproute2 ss.c source](https://github.com/iproute2/iproute2/blob/main/misc/ss.c)

---

## 4. Correlating Sockets to Processes

### The /proc/PID/fd Method

```bash
# 1. Find socket inode from /proc/net/tcp
# (inode field in the output)

# 2. Search all process fd directories
for pid in /proc/[0-9]*; do
    for fd in $pid/fd/*; do
        link=$(readlink $fd 2>/dev/null)
        if [[ $link == "socket:[$INODE]" ]]; then
            echo "PID $(basename $pid) has socket $INODE"
        fi
    done
done
```

### How lsof Works Internally

lsof performs the correlation by:
1. Reading `/proc/net/tcp`, `/proc/net/tcp6`, `/proc/net/udp`, etc.
2. Building a hash table of inode -> socket info
3. Walking `/proc/*/fd/` and reading symlinks
4. Matching `socket:[inode]` links to the hash table

**Performance note:** On systems with many connections, lsof can be slow. Use `ss -p` for faster process correlation via NETLINK_SOCK_DIAG.

### Programmatic Correlation

```c
#include <dirent.h>
#include <unistd.h>

void find_socket_process(ino_t target_inode) {
    DIR *proc = opendir("/proc");
    struct dirent *entry;

    while ((entry = readdir(proc))) {
        if (!isdigit(entry->d_name[0])) continue;

        char fd_path[256];
        snprintf(fd_path, sizeof(fd_path), "/proc/%s/fd", entry->d_name);

        DIR *fd_dir = opendir(fd_path);
        if (!fd_dir) continue;

        struct dirent *fd_entry;
        while ((fd_entry = readdir(fd_dir))) {
            char link_path[512], target[256];
            snprintf(link_path, sizeof(link_path), "%s/%s",
                     fd_path, fd_entry->d_name);

            ssize_t len = readlink(link_path, target, sizeof(target)-1);
            if (len > 0) {
                target[len] = '\0';
                ino_t inode;
                if (sscanf(target, "socket:[%lu]", &inode) == 1) {
                    if (inode == target_inode) {
                        printf("Found: PID %s\n", entry->d_name);
                    }
                }
            }
        }
        closedir(fd_dir);
    }
    closedir(proc);
}
```

---

## 5. Container Runtime Networking

### Network Namespaces

Containers use Linux network namespaces to isolate their network stack:

```bash
# Create a network namespace
ip netns add myns

# Run command in namespace
ip netns exec myns ip addr

# Container runtimes do this automatically
```

Each container has its own:
- Network interfaces
- Routing tables
- iptables rules
- /proc/net/* files

### CNI (Container Network Interface)

CNI provides a standard interface between container runtimes and network plugins.

**Plugin Discovery:**
- Binaries in `/opt/cni/bin/` (bridge, flannel, calico, etc.)
- Configuration in `/etc/cni/net.d/*.conf`

**CNI Operations:**
| Operation | Description |
|-----------|-------------|
| ADD | Create network for container |
| DEL | Delete network for container |
| CHECK | Verify network is correct |
| GC | Garbage collect unused resources |
| VERSION | Report plugin version |

**How container networking works:**
1. Container runtime creates container with new network namespace
2. Runtime calls CNI plugin with namespace path
3. CNI plugin creates veth pair, bridges, assigns IPs
4. Container gets network connectivity

### Cgroups for Network Tracking

Cgroups v2 integrates with eBPF for network policy:

```c
// Attach eBPF program to cgroup for network control
SEC("cgroup/connect4")
int cgroup_connect4(struct bpf_sock_addr *ctx) {
    // Can block or redirect connections per-cgroup
    return 1;  // Allow
}
```

**Key cgroup network controls:**
- `BPF_CGROUP_INET4_CONNECT` - Intercept connect()
- `BPF_CGROUP_INET4_BIND` - Intercept bind()
- `BPF_CGROUP_SOCK_OPS` - Socket operations

### Cilium's Approach

Cilium uses eBPF at multiple kernel attachment points:

1. **Socket-level load balancing**: Rewrites at `connect()` time, avoiding per-packet NAT
2. **XDP**: Processes packets before they enter the network stack
3. **TC (Traffic Control)**: For egress policy
4. **sock_ops**: Tracks connection establishment

**Service discovery without DNS:**
```
Pod A calls ClusterIP -> Cilium eBPF intercepts connect()
-> Rewrites destination to actual pod IP -> Direct connection
```

### Istio's Sidecar Approach

Istio uses iptables to redirect traffic through Envoy proxies:

```bash
# Typical iptables rules injected by istio-init
iptables -t nat -A PREROUTING -p tcp -j REDIRECT --to-port 15006
iptables -t nat -A OUTPUT -p tcp -j REDIRECT --to-port 15001
```

**istio-init container:**
- Runs as init container with `NET_ADMIN` capability
- Configures iptables for traffic interception
- All pod traffic flows through Envoy sidecar

**Comparison:**
| Aspect | Cilium (eBPF) | Istio (Sidecar) |
|--------|---------------|-----------------|
| Overhead | Very low | Medium (extra hop) |
| Visibility | Kernel-level | L7 proxy |
| Complexity | High (eBPF) | Medium (iptables) |
| App Changes | None | None |

**Reference:** [Istio sidecar injection explained](https://jimmysong.io/en/blog/sidecar-injection-iptables-and-traffic-routing/)

### Weave Net's DNS-based Discovery

Weave Net provides automatic service discovery via DNS:

```
Container A: curl http://myservice
-> weaveDNS resolves "myservice" to container IP
-> Direct connection over Weave network
```

**No external cluster store required** - unlike Docker's overlay driver.

---

## When to Use Each Approach

### Development/Debugging

**Use /proc polling or ss:**
- Simple to implement
- No special permissions needed for basic info
- Good for one-off discovery

### Testing/CI

**Use LD_PRELOAD:**
- Intercept without code changes
- Easy to inject test behaviors
- Control process launch environment

### Production Monitoring

**Use eBPF (tracepoints or sock_ops):**
- Lowest overhead
- Doesn't modify application behavior
- Real-time visibility

**Requirements:**
- Kernel 4.15+ (basic eBPF)
- Kernel 5.5+ (fentry/fexit for best performance)
- CAP_BPF, CAP_PERFMON capabilities

### Container Orchestration

**Use CNI plugins + eBPF:**
- Cilium for high-performance networking
- Calico for network policy
- Integrate with Kubernetes service discovery

### Security Sandboxing

**Use seccomp-BPF:**
- Restrict syscalls per-process
- Combine with eBPF for monitoring
- Good for untrusted workloads

---

## Security Considerations

### Permissions Required

| Mechanism | Required Capabilities |
|-----------|----------------------|
| /proc/net/* | None (world-readable) |
| /proc/PID/fd | PTRACE_MODE_READ_FSCREDS |
| LD_PRELOAD | Control process environment |
| ptrace | CAP_SYS_PTRACE |
| eBPF (tracing) | CAP_BPF, CAP_PERFMON |
| eBPF (networking) | CAP_NET_ADMIN |
| seccomp | CAP_SYS_ADMIN or no_new_privs |

### Container Escapes

Be cautious with:
- Mounting /proc from host
- Sharing network namespaces
- Privileged containers with CAP_SYS_PTRACE
- eBPF programs that can read kernel memory

### Information Disclosure

/proc/net/tcp reveals:
- All listening ports
- Connection endpoints
- Socket owners (UIDs)

Mitigate with:
- Network namespaces (containers only see their connections)
- Cgroup namespaces (restrict /proc visibility)

---

## Implementation Recommendations

### For a Service Router

1. **Start with /proc polling** for prototype
   - Poll `/proc/net/tcp` every few seconds
   - Correlate to processes via `/proc/*/fd`
   - Build service registry

2. **Graduate to eBPF** for production
   - Use tracepoints for stability across kernel versions
   - Hook `sys_enter_bind` and `sys_exit_bind`
   - Notify userspace via perf buffer or ring buffer

3. **Consider sock_ops** for connection-level tracking
   - Track both bind and established connections
   - Integrate with sockmap for zero-copy forwarding

### Sample Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    User Space                           │
│  ┌─────────────┐    ┌─────────────┐    ┌────────────┐  │
│  │   Service   │    │    Ring     │    │   Router   │  │
│  │  Registry   │◄───│   Buffer    │◄───│   Agent    │  │
│  └─────────────┘    └─────────────┘    └────────────┘  │
└───────────────────────────┬─────────────────────────────┘
                            │
┌───────────────────────────┼─────────────────────────────┐
│                    Kernel │                             │
│  ┌────────────────────────┴───────────────────────────┐ │
│  │                   eBPF Programs                     │ │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────┐  │ │
│  │  │ tracepoint/  │  │   sockops    │  │  cgroup  │  │ │
│  │  │ sys_*_bind   │  │  established │  │  inet4   │  │ │
│  │  └──────────────┘  └──────────────┘  └──────────┘  │ │
│  └────────────────────────────────────────────────────┘ │
│                           │                             │
│                    ┌──────┴──────┐                      │
│                    │   Sockets   │                      │
│                    └─────────────┘                      │
└─────────────────────────────────────────────────────────┘
```

---

## Further Reading

- [eBPF.io - What is eBPF](https://ebpf.io/what-is-ebpf/)
- [Cilium Documentation](https://docs.cilium.io/en/stable/)
- [Brendan Gregg's eBPF Tools](https://www.brendangregg.com/ebpf.html)
- [iximiuz - eBPF Tracing Comparison](https://labs.iximiuz.com/tutorials/ebpf-tracing-46a570d1)
- [CNI Specification](https://www.cni.dev/docs/spec/)
- [Kernel sock_diag Documentation](https://man7.org/linux/man-pages/man7/sock_diag.7.html)
