# Active Registration Patterns for Service Discovery

## Overview

Active registration is a service discovery pattern where applications explicitly register themselves with a central registry. The registry maintains a real-time view of available services, which routers and load balancers consume to direct traffic.

This document provides a deep technical analysis based on four major implementations:
- **Consul** (HashiCorp) - Service mesh and discovery
- **Eureka** (Netflix) - Service registry for microservices
- **etcd** (CoreOS/CNCF) - Key-value store with lease-based registration
- **Kubernetes Endpoints** - Built on etcd with controller patterns

---

## How Registration Works Technically

### The Core Registration Flow

```
┌─────────────┐     Register      ┌──────────────┐
│   Service   │ ──────────────▶  │   Registry   │
│  Instance   │                   │              │
│             │ ◀────────────── │   (Consul/   │
│             │    Heartbeat      │    Eureka)   │
└─────────────┘                   └──────────────┘
                                         │
                                         │ Watch/Query
                                         ▼
                                  ┌──────────────┐
                                  │    Router/   │
                                  │ Load Balancer│
                                  └──────────────┘
```

### Consul: Anti-Entropy with Local State

Consul uses a **local agent model** where services register with a local agent that syncs to the cluster.

**Registration via Local State (from `consul/agent/local/state.go`):**

```go
// ServiceState describes the state of a service record.
type ServiceState struct {
    Service *structs.NodeService  // The service definition
    Token   string                 // ACL token for updates
    InSync  bool                   // Is local state synced with server?
    Deleted bool                   // Marked for deletion but not yet removed
}

// AddServiceWithChecks adds a service and its health checks atomically
func (l *State) AddServiceWithChecks(service *structs.NodeService,
    checks []*structs.HealthCheck, token string, isLocallyDefined bool) error {
    l.Lock()
    defer l.Unlock()

    if err := l.addServiceLocked(service, token, isLocallyDefined); err != nil {
        return err
    }
    for _, check := range checks {
        if err := l.addCheckLocked(check, token, isLocallyDefined); err != nil {
            return err
        }
    }
    return nil
}
```

**Anti-Entropy Syncing (from `consul/agent/ae/ae.go`):**

Consul's anti-entropy mechanism ensures local and remote state eventually converge:

```go
// StateSyncer manages background synchronization of the given state.
// This is also called anti-entropy.
type StateSyncer struct {
    State      SyncState      // Data to synchronize
    Interval   time.Duration  // Time between full sync runs
    SyncFull   *Trigger       // Immediate full sync trigger
    SyncChanges *Trigger      // Partial sync trigger
}

// Scale factor to prevent thundering herd in large clusters
func scaleFactor(nodes int) int {
    if nodes <= 128 {
        return 1.0
    }
    // Doubles delay for each doubling of cluster size
    return int(math.Ceil(math.Log2(float64(nodes))-math.Log2(128)) + 1.0)
}
```

### Eureka: Heartbeat-Based Leases

Eureka uses a **direct registration model** where clients register and send heartbeats directly to the server.

**Client-Side Registration (from `eureka/DiscoveryClient.java`):**

```java
// Register with Eureka server
boolean register() throws Throwable {
    logger.info("Registering service...");
    EurekaHttpResponse<Void> httpResponse =
        eurekaTransport.registrationClient.register(instanceInfo);
    return httpResponse.getStatusCode() == Status.NO_CONTENT.getStatusCode();
}

// Heartbeat renewal - called every 30 seconds by default
boolean renew() {
    EurekaHttpResponse<InstanceInfo> httpResponse =
        eurekaTransport.registrationClient.sendHeartBeat(
            instanceInfo.getAppName(),
            instanceInfo.getId(),
            instanceInfo,
            null
        );

    // If server lost our registration, re-register
    if (httpResponse.getStatusCode() == Status.NOT_FOUND.getStatusCode()) {
        return register();
    }
    return httpResponse.getStatusCode() == Status.OK.getStatusCode();
}
```

**Scheduled Tasks for Registration:**

```java
private void initScheduledTasks() {
    // Heartbeat timer - renews lease
    int renewalIntervalInSecs = instanceInfo.getLeaseInfo().getRenewalIntervalInSecs();
    heartbeatTask = new TimedSupervisorTask(
        "heartbeat",
        scheduler,
        heartbeatExecutor,
        renewalIntervalInSecs,  // Default: 30 seconds
        TimeUnit.SECONDS,
        expBackOffBound,
        new HeartbeatThread()
    );

    // InstanceInfo replicator - pushes metadata changes
    instanceInfoReplicator = new InstanceInfoReplicator(
        this,
        instanceInfo,
        clientConfig.getInstanceInfoReplicationIntervalSeconds(),
        2  // burstSize
    );
}
```

### etcd: Lease-Based TTL

etcd provides a lower-level primitive - **leases with automatic TTL expiration**.

**Lease Grant and KeepAlive (from `etcd/client/v3/lease.go`):**

```go
// Lease interface for service registration
type Lease interface {
    // Grant creates a new lease with TTL
    Grant(ctx context.Context, ttl int64) (*LeaseGrantResponse, error)

    // KeepAlive keeps the lease alive forever (until context cancels)
    KeepAlive(ctx context.Context, id LeaseID) (<-chan *LeaseKeepAliveResponse, error)

    // Revoke explicitly ends a lease
    Revoke(ctx context.Context, id LeaseID) (*LeaseRevokeResponse, error)
}

// Internal keep-alive loop
func (l *lessor) sendKeepAliveLoop(stream pb.Lease_LeaseKeepAliveClient) {
    for {
        var tosend []LeaseID
        now := time.Now()

        l.mu.Lock()
        for id, ka := range l.keepAlives {
            if ka.nextKeepAlive.Before(now) {
                tosend = append(tosend, id)
            }
        }
        l.mu.Unlock()

        for _, id := range tosend {
            r := &pb.LeaseKeepAliveRequest{ID: int64(id)}
            if err := stream.Send(r); err != nil {
                return  // Stream broken, will reconnect
            }
        }

        select {
        case <-time.After(500 * time.Millisecond):
        case <-stream.Context().Done():
            return
        }
    }
}
```

**Service Registration with Lease:**

```go
// From etcd naming/endpoints package
func (m *endpointManager) AddEndpoint(ctx context.Context, key string,
    endpoint Endpoint, opts ...clientv3.OpOption) error {

    internalUpdate := &internal.Update{
        Op:       internal.Add,
        Addr:     endpoint.Addr,
        Metadata: endpoint.Metadata,
    }
    v, _ := json.Marshal(internalUpdate)

    // Put with lease option for automatic expiry
    _, err = m.client.KV.Txn(ctx).Then(
        clientv3.OpPut(key, string(v), opts...),
    ).Commit()
    return err
}
```

---

## How Routers Consume Registration Data

### Consul: Blocking Queries and DNS

**Blocking Queries (Long Polling):**
```go
// Clients specify an index and block until state changes
req := structs.NodeSpecificRequest{
    Datacenter: l.config.Datacenter,
    Node:       l.config.NodeName,
    QueryOptions: structs.QueryOptions{
        AllowStale:       true,
        MaxStaleDuration: 2 * time.Second,
    },
}
```

**DNS Interface:**
Consul provides DNS-based discovery - services are queried as `myservice.service.consul`.

### Eureka: Delta Fetching with Hash Codes

Eureka clients maintain a local registry cache and fetch deltas:

```java
// Delta update to minimize bandwidth
private void getAndUpdateDelta(Applications applications) throws Throwable {
    Applications delta = eurekaTransport.queryClient.getDelta();

    for (Application app : delta.getRegisteredApplications()) {
        for (InstanceInfo instance : app.getInstances()) {
            switch (instance.getActionType()) {
                case ADDED:
                    applications.getRegisteredApplications(instance.getAppName())
                        .addInstance(instance);
                    break;
                case MODIFIED:
                    applications.getRegisteredApplications(instance.getAppName())
                        .addInstance(instance);
                    break;
                case DELETED:
                    existingApp.removeInstance(instance);
                    break;
            }
        }
    }

    // Reconcile with hash codes
    if (!reconcileHashCode.equals(delta.getAppsHashCode())) {
        reconcileAndLogDifference(delta, reconcileHashCode);
    }
}
```

### etcd: Watch Streams

etcd provides real-time updates via gRPC watch streams:

```go
func (m *endpointManager) NewWatchChannel(ctx context.Context) (WatchChannel, error) {
    // Initial list
    resp, err := m.client.Get(ctx, key, clientv3.WithPrefix())

    upch := make(chan []*Update, 1)
    upch <- initUpdates  // Send current state

    go m.watch(ctx, resp.Header.Revision+1, upch)  // Watch for changes
    return upch, nil
}

func (m *endpointManager) watch(ctx context.Context, rev int64, upch chan []*Update) {
    wch := m.client.Watch(ctx, key, clientv3.WithRev(rev), clientv3.WithPrefix())

    for wresp := range wch {
        deltaUps := make([]*Update, 0)
        for _, e := range wresp.Events {
            switch e.Type {
            case clientv3.EventTypePut:
                deltaUps = append(deltaUps, &Update{Op: Add, ...})
            case clientv3.EventTypeDelete:
                deltaUps = append(deltaUps, &Update{Op: Delete, ...})
            }
        }
        upch <- deltaUps
    }
}
```

---

## What Happens on App Crash/Restart

### Stale Entry Detection

**Eureka's Lease Expiration:**

```java
public class Lease<T> {
    public static final int DEFAULT_DURATION_IN_SECS = 90;

    private volatile long lastUpdateTimestamp;
    private long duration;

    public void renew() {
        lastUpdateTimestamp = System.currentTimeMillis() + duration;
    }

    public boolean isExpired(long additionalLeaseMs) {
        return evictionTimestamp > 0 ||
            System.currentTimeMillis() > (lastUpdateTimestamp + duration + additionalLeaseMs);
    }
}

// Eviction runs periodically
public void evict(long additionalLeaseMs) {
    List<Lease<InstanceInfo>> expiredLeases = new ArrayList<>();

    for (Entry<String, Lease<InstanceInfo>> leaseEntry : leaseMap.entrySet()) {
        Lease<InstanceInfo> lease = leaseEntry.getValue();
        if (lease.isExpired(additionalLeaseMs)) {
            expiredLeases.add(lease);
        }
    }

    // Evict in random order to spread impact across apps
    Random random = new Random();
    for (int i = 0; i < toEvict; i++) {
        int next = i + random.nextInt(expiredLeases.size() - i);
        Collections.swap(expiredLeases, i, next);
        internalCancel(lease.getHolder().getAppName(), lease.getHolder().getId(), false);
    }
}
```

**etcd's Automatic Lease Expiry:**

```go
// Deadline loop checks for expired keep-alives
func (l *lessor) deadlineLoop() {
    timer := time.NewTimer(time.Second)
    for {
        select {
        case <-timer.C:
            now := time.Now()
            l.mu.Lock()
            for id, ka := range l.keepAlives {
                if ka.deadline.Before(now) {
                    // Waited too long - lease may be expired
                    ka.close()
                    delete(l.keepAlives, id)
                }
            }
            l.mu.Unlock()
        }
    }
}
```

### Graceful Deregistration

**Eureka Client Shutdown:**

```java
@PreDestroy
public synchronized void shutdown() {
    if (isShutdown.compareAndSet(false, true)) {
        // Cancel scheduled tasks
        cancelScheduledTasks();

        // Explicitly deregister if configured
        if (clientConfig.shouldUnregisterOnShutdown()) {
            applicationInfoManager.setInstanceStatus(InstanceStatus.DOWN);
            unregister();
        }

        eurekaTransport.shutdown();
    }
}

void unregister() {
    EurekaHttpResponse<Void> httpResponse =
        eurekaTransport.registrationClient.cancel(
            instanceInfo.getAppName(),
            instanceInfo.getId()
        );
}
```

**Consul's Deregistration:**

```go
// Consul marks service as deleted locally, then syncs
func (l *State) removeServiceLocked(id structs.ServiceID) error {
    s := l.services[id]
    if s == nil || s.Deleted {
        return fmt.Errorf("Unknown service ID %q", id)
    }

    // Mark as deleted, keep entry for sync
    s.InSync = false
    s.Deleted = true

    l.TriggerSyncChanges()  // Trigger background sync
    return nil
}

// Background sync deletes from server
func (l *State) deleteService(key structs.ServiceID) error {
    req := structs.DeregisterRequest{
        Datacenter: l.config.Datacenter,
        Node:       l.config.NodeName,
        ServiceID:  key.ID,
    }
    err := l.Delegate.RPC(context.Background(), "Catalog.Deregister", &req, &out)
    if err == nil || strings.Contains(err.Error(), "Unknown service") {
        delete(l.services, key)
    }
    return err
}
```

---

## Failure Modes and Mitigations

### Registry Down

**Eureka's Backup Registry:**

```java
private boolean fetchRegistryFromBackup() {
    BackupRegistry backupRegistryInstance = backupRegistryProvider.get();

    if (backupRegistryInstance != null) {
        Applications apps = backupRegistryInstance.fetchRegistry();
        if (apps != null) {
            localRegionApps.set(filterAndShuffle(apps));
            return true;
        }
    }
    return false;
}
```

**Local Caching:**
All systems maintain local caches that remain valid during registry outages:

```java
// Eureka caches locally
private final AtomicReference<Applications> localRegionApps = new AtomicReference<>();

// Consul caches in local agent
services map[structs.ServiceID]*ServiceState
```

### Self-Preservation Mode (Split-Brain Protection)

Eureka's self-preservation prevents mass eviction during network partitions:

```java
@Override
public boolean isLeaseExpirationEnabled() {
    if (!isSelfPreservationModeEnabled()) {
        return true;  // Always allow expiration if disabled
    }

    // Only expire if we're seeing enough renewals
    return numberOfRenewsPerMinThreshold > 0 &&
           getNumOfRenewsInLastMin() > numberOfRenewsPerMinThreshold;
}

// Self-preservation activates when:
// - Expected renewals/min: (registeredInstances * 2) * 0.85
// - Actual renewals fall below threshold
// Result: Stop evicting instances to prevent cascading failure
```

### Network Partition Handling

**Consul's Staggered Sync:**

```go
// staggerFn spreads sync operations based on cluster size
func (s *StateSyncer) staggerFn(d time.Duration) time.Duration {
    f := scaleFactor(s.ClusterSize())
    return libRandomStagger(time.Duration(f) * d)
}

// Prevents thundering herd when server comes back online
case <-s.SyncFull.Notif():
    select {
    case <-time.After(s.stagger(s.serverUpInterval)):
        return syncFullNotifEvent
    case <-s.ShutdownCh:
        return shutdownEvent
    }
```

**etcd's Leader Requirement:**

```go
// Detect no-leader scenarios
if errors.Is(ContextError(l.stopCtx, err), rpctypes.ErrNoLeader) {
    l.closeRequireLeader()  // Close channels that require leader
}
```

---

## Performance Characteristics

### Heartbeat Overhead

| System | Default Interval | Network Impact |
|--------|-----------------|----------------|
| Eureka | 30 seconds | Low - simple PUT request |
| Consul | Anti-entropy 60s + changes | Low - batched updates |
| etcd | TTL/3 (e.g., 5s for 15s TTL) | Moderate - gRPC stream |

### Scalability Limits

**Eureka:**
- Designed for Netflix scale (thousands of instances)
- Delta fetching reduces bandwidth
- Response cache minimizes server load

**Consul:**
- Uses log2 scaling for sync intervals
- At 8192 nodes, sync delay is 8x normal
- Gossip protocol handles membership

**etcd:**
- Watch streams are efficient
- Lease keep-alive is single stream per client
- Bounded by Raft consensus (typically ~10k writes/sec)

### Registration Latency

```
Registration -> Available in routing table:

Consul:  ~100ms (local agent) + sync interval (0-60s)
Eureka:  ~30s (next cache refresh for consumers)
etcd:    ~10-50ms (immediate with watch)
```

---

## When to Use Active Registration

### Ideal Use Cases

1. **Dynamic Container Environments**
   - Pods starting/stopping frequently
   - Short-lived instances
   - Auto-scaling scenarios

2. **Multi-Datacenter Deployments**
   - Need awareness of instance location
   - Geographic routing requirements

3. **Health-Aware Routing**
   - Want traffic shifted from unhealthy instances
   - Need circuit-breaker integration

4. **Service Mesh Prerequisites**
   - Building toward sidecar proxy model
   - Need service identity for mTLS

### When It's Overkill

1. **Static Infrastructure**
   - Fixed set of servers
   - Rare deployments
   - DNS-based discovery sufficient

2. **Small Scale**
   - < 10 services
   - < 50 instances total
   - Config file management works

3. **Stateless Load Balancing**
   - Round-robin is sufficient
   - No health checks needed
   - No routing intelligence required

---

## Tradeoff Analysis

### What You Gain

| Benefit | Description |
|---------|-------------|
| **Real-time Visibility** | Know exactly what's running, where |
| **Health Integration** | Unhealthy instances removed from rotation |
| **Self-Healing** | Failed instances eventually evicted |
| **Metadata Routing** | Route based on version, region, etc. |
| **Decoupled Configuration** | Services find each other dynamically |

### What Complexity You Take On

| Cost | Description |
|------|-------------|
| **Operational Burden** | Registry is a critical path dependency |
| **Network Sensitivity** | Heartbeat failures can cascade |
| **Clock Synchronization** | TTL expiration requires reasonable clock sync |
| **Client Libraries** | Every service needs registration code |
| **Debugging Difficulty** | "Why isn't my service routing?" investigations |

### The Fundamental Tension

**Availability vs. Consistency:**
- Fast eviction catches failures quickly but risks false positives
- Slow eviction is safer but routes to dead instances longer

**Eureka's Default: 30s heartbeat, 90s expiration**
```
Best case: Dead instance evicted in ~90 seconds
Worst case: Network blip causes false eviction at 90 seconds
```

**etcd's Approach: Configurable TTL**
```go
// Application chooses its own risk tolerance
lease, _ := client.Grant(ctx, 15)  // 15 second TTL - aggressive
lease, _ := client.Grant(ctx, 300) // 5 minute TTL - conservative
```

---

## Implementation Recommendations

### 1. Start with Heartbeat Defaults

Most systems have sensible defaults. Only tune after observing:
- False positive eviction rate
- Time-to-recovery after real failures

### 2. Implement Graceful Shutdown

```go
// Always deregister explicitly on shutdown
sigCh := make(chan os.Signal, 1)
signal.Notify(sigCh, syscall.SIGTERM)

go func() {
    <-sigCh
    registry.Deregister(serviceID)
    os.Exit(0)
}()
```

### 3. Use Health Checks Appropriately

**Consul Check Types:**
- HTTP: Full application health
- TCP: Port is accepting connections
- TTL: Application pushes status
- gRPC: For gRPC services

```go
// Consul check definition
type CheckType struct {
    HTTP     string         // URL to check
    Interval time.Duration  // Check frequency
    Timeout  time.Duration  // Request timeout
    DeregisterCriticalServiceAfter time.Duration  // Auto-cleanup
}
```

### 4. Plan for Registry Unavailability

- Cache service locations locally
- Implement retry with backoff
- Consider static fallback for critical paths

### 5. Monitor Registration Health

Key metrics to track:
- Heartbeat success rate
- Registration latency
- Eviction rate
- Self-preservation activation (Eureka)

---

## Summary

Active registration is a powerful pattern that trades operational complexity for dynamic service discovery capabilities. The core mechanism across all implementations involves:

1. **Registration** - Service announces itself
2. **Heartbeats/Leases** - Continuous proof of liveness
3. **Eviction** - Removal of unresponsive instances
4. **Watch/Query** - Consumers track available instances

Choose active registration when you need real-time service discovery with health awareness. Accept that you're adding a critical dependency and plan accordingly for its failure modes.
