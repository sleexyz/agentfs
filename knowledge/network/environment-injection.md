# Environment Injection for Service Discovery and Port Assignment

Environment injection is the "12-factor app" approach to configuring services: the platform sets environment variables (especially `$PORT`) and the app reads them at startup. This is the simplest service discovery pattern - no registries, no sidecars, just environment variables.

---

## The 12-Factor Port Contract

From the [12-factor methodology](https://12factor.net/port-binding):

> "The web app exports HTTP as a service by binding to a port, and listening to requests coming in on that port."

**Core principles:**
1. App is **completely self-contained** - bundles its own web server
2. App **reads port from environment** - never hardcodes
3. Platform **assigns the port** - app doesn't choose
4. Platform **handles routing** - from public URL to the assigned port

```
┌─────────────────────────────────────────────────────────────┐
│                        Platform                              │
│                                                              │
│   Internet ──▶ Router ──▶ [PORT=5432] ──▶ App Instance      │
│                    │                                         │
│                    └──▶ [PORT=5433] ──▶ App Instance        │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

**The contract is simple:**
- Platform says: "I'll set `$PORT`, you listen on it"
- App says: "I'll bind to whatever port you give me"

---

## Platform Comparison: How Ports Get Assigned

### Heroku (The Original)

**Port Assignment:**
- Single port per dyno, set via `$PORT`
- Port is dynamically assigned at dyno startup
- Different for each dyno restart

**Readiness Detection:**
- Must bind within **60 seconds** or R10 Boot Timeout
- Router starts sending traffic once port is bound
- No explicit health check - binding IS the signal

**Routing:**
- Random selection across dynos
- Routers maintain connection pools
- Quarantine unhealthy dynos for 5 seconds

```bash
# Heroku's PORT assignment
$ heroku run printenv PORT
5432

# Different each time
$ heroku run printenv PORT
8721
```

**Key Environment Variables:**
| Variable | Description |
|----------|-------------|
| `PORT` | Port to bind to (required) |
| `DYNO` | Dyno identifier (web.1, worker.2) |
| `HEROKU_APP_NAME` | Application name |

**Reference:** [Heroku Runtime Principles](https://devcenter.heroku.com/articles/runtime-principles)

---

### Google Cloud Run

**Port Assignment:**
- Default port: **8080**
- Platform injects `$PORT` environment variable
- Can configure custom port in deployment settings

**Readiness Detection:**
- **TCP startup probe** by default - Cloud Run waits for port to accept connections
- Optional HTTP/gRPC startup probes for more control
- Traffic starts flowing once startup probe passes

**Key distinction from Heroku:**
Cloud Run has explicit **startup probes** vs Heroku's "bind within timeout":

```yaml
# Cloud Run service.yaml - explicit startup probe
spec:
  containers:
    - image: myapp
      ports:
        - containerPort: 8080
      startupProbe:
        httpGet:
          path: /healthz
          port: 8080
        initialDelaySeconds: 0
        periodSeconds: 10
        failureThreshold: 3
```

**Key Environment Variables:**
| Variable | Description |
|----------|-------------|
| `PORT` | Port to bind to (default 8080) |
| `K_SERVICE` | Cloud Run service name |
| `K_REVISION` | Current revision name |
| `K_CONFIGURATION` | Configuration name |

**Reference:** [Cloud Run Container Contract](https://cloud.google.com/run/docs/container-contract)

---

### Fly.io

**Port Assignment:**
- Configured in `fly.toml` via `internal_port`
- Platform proxies external 80/443 to your internal port
- App doesn't read `$PORT` - it's config-driven

```toml
# fly.toml
[http_service]
  internal_port = 8080
  force_https = true
```

**Readiness Detection:**
- TCP checks: confirm port is listening
- HTTP checks: hit endpoint and expect 2xx
- Failing checks remove instance from rotation (don't restart)

```toml
[[http_service.checks]]
  grace_period = "5s"
  interval = "10s"
  method = "GET"
  path = "/health"
  timeout = "2s"
```

**Service Discovery:**
- Internal DNS: `<app>.internal` resolves to all instances
- Region-aware: `<region>.<app>.internal`
- Private 6PN networking between apps in same org

**Key Environment Variables:**
| Variable | Description |
|----------|-------------|
| `FLY_APP_NAME` | Application name |
| `FLY_REGION` | Region code (ord, lhr, etc.) |
| `FLY_PRIVATE_IP` | IPv6 address on 6PN network |
| `FLY_VM_MEMORY_MB` | Allocated memory |
| `PRIMARY_REGION` | Primary region for the app |

**Reference:** [Fly.io Configuration](https://fly.io/docs/reference/configuration/)

---

### Render

**Port Assignment:**
- Default port: **10000**
- Platform sets `$PORT` environment variable
- Must bind to **0.0.0.0** (not localhost)

**Readiness Detection:**
- Implicit - platform routes traffic once port is listening
- No explicit health check configuration for startup

**Private Networking:**
- Services in same region share private network
- Discovery hostname: `<service>-discovery.internal`
- Can listen on multiple ports for internal traffic

**Key Environment Variables:**
| Variable | Description |
|----------|-------------|
| `PORT` | Port to bind to (default 10000) |
| `RENDER` | Always "true" on Render |
| `RENDER_EXTERNAL_HOSTNAME` | Public hostname |
| `RENDER_EXTERNAL_URL` | Full public URL |
| `RENDER_SERVICE_NAME` | Service name |

**Reference:** [Render Environment Variables](https://render.com/docs/environment-variables)

---

## Procfile Runners: Local Development

### Foreman (Ruby)

The original Procfile runner, sets `$PORT` per process type:

```
# Procfile
web: bundle exec rails server -p $PORT
worker: bundle exec sidekiq
```

**Port Assignment Algorithm:**
- Base port: 5000 (configurable with `-p`)
- Each process TYPE gets +100
- Each process INSTANCE gets +1

```bash
$ foreman start -f Procfile
# web.1    -> PORT=5000
# web.2    -> PORT=5001
# worker.1 -> PORT=5100
# worker.2 -> PORT=5101
```

**Environment Loading:**
- Reads `.env` file automatically
- Variables available to all processes
- No watching/hot-reload

**Reference:** [Foreman Manual](https://ddollar.github.io/foreman/)

---

### Overmind (Go + tmux)

Modern Procfile runner with tmux integration:

**Port Assignment:**
- Same algorithm as Foreman (base + 100 per type)
- Default base: 5000, step: 100
- Can disable with `-N` flag

```bash
# Customize port assignment
$ overmind start -p 3000 -P 10
# web.1 -> PORT=3000
# api.1 -> PORT=3010

# Disable PORT injection entirely
$ overmind start -N
```

**Key Features:**
- Runs each process in tmux session
- Can connect to process: `overmind connect web`
- Restart individual processes without killing others
- Captures full output (no clipping)

**Environment:**
- Loads `.overmind.env` and `.env`
- Exposes `OVERMIND_PROCESS_<NAME>_PORT` for cross-process discovery

**Reference:** [Overmind README](https://github.com/DarthSim/overmind)

---

## Handling Non-Compliant Apps

The fundamental tension: **what if the app doesn't read `$PORT`?**

### Strategy 1: Wrapper Scripts

Create a startup script that transforms `$PORT` into app-specific config:

```bash
#!/bin/bash
# entrypoint.sh - Wrapper for app that expects --port flag

exec ./myapp --port="${PORT:-8080}" "$@"
```

For apps that need config files:

```bash
#!/bin/bash
# Generate config from environment, then start

cat > /app/config.json <<EOF
{
  "server": {
    "port": ${PORT:-8080},
    "host": "0.0.0.0"
  }
}
EOF

exec ./myapp --config=/app/config.json
```

---

### Strategy 2: Config Templating

**envsubst (built into GNU gettext):**

```bash
# nginx.conf.template
server {
    listen ${PORT};
    ...
}
```

```bash
#!/bin/bash
envsubst '${PORT}' < /etc/nginx/nginx.conf.template > /etc/nginx/nginx.conf
exec nginx -g 'daemon off;'
```

**ERB Templates (Heroku nginx buildpack approach):**

```erb
# config/nginx.conf.erb
server {
    listen <%= ENV['PORT'] %>;
    ...
}
```

The buildpack processes ERB at startup, before launching nginx.

**Reference:** [Heroku nginx buildpack](https://github.com/heroku/heroku-buildpack-nginx)

---

### Strategy 3: Port Forwarding with socat

For stubborn apps that truly cannot be configured:

```bash
#!/bin/bash
# App listens on 3000, platform expects $PORT

# Start the app in background on its hardcoded port
./myapp &

# Forward platform's port to app's port
exec socat TCP-LISTEN:${PORT},fork TCP:localhost:3000
```

This adds latency but works for any app.

---

### Strategy 4: Reverse Proxy Sidecar

Run nginx/caddy as a sidecar that:
1. Listens on `$PORT`
2. Proxies to app on hardcoded port

```
# Procfile
web: ./start-nginx.sh
app: ./myapp --port 3000
```

Where `start-nginx.sh` generates config from `$PORT` and proxies to localhost:3000.

---

### Strategy 5: Platform-Specific Override

Some platforms let you override the port they expect:

**Cloud Run:**
```yaml
# Specify the port your app uses, Cloud Run injects that as $PORT
spec:
  containers:
    - ports:
        - containerPort: 3000  # Your app's hardcoded port
```

**Fly.io:**
```toml
# fly.toml - tell Fly where your app listens
[http_service]
  internal_port = 3000  # Your app's port, no $PORT needed
```

---

## The .profile.d Pattern (Heroku)

Heroku's buildpack system uses `.profile.d/*.sh` scripts to set environment variables at runtime:

**Execution Order:**
1. Heroku sets `$HOME`, `$PORT`
2. Heroku sets config vars from `heroku config`
3. Shell sources all `.profile.d/*.sh` scripts
4. Shell sources `$HOME/.profile`
5. Command from Procfile runs

**Example buildpack adding to PATH:**

```bash
# .profile.d/myapp.sh (created by buildpack)
export PATH="/app/bin:$PATH"
export LD_LIBRARY_PATH="/app/lib:$LD_LIBRARY_PATH"

# Set default if not overridden by config var
export LANG=${LANG:-en_US.UTF-8}
```

**Why this matters:**
- Buildpacks can inject runtime configuration
- Apps can have different behavior per environment
- Environment is modified BEFORE app starts

**Reference:** [Heroku Buildpack API](https://devcenter.heroku.com/articles/buildpack-api)

---

## Health Checks and Readiness

How does the platform know the app is ready?

### Implicit (Heroku model)

"If you're listening on the port, you're ready"

```
Timeline:
0s      App starts
...     App initializes
45s     App calls listen(PORT)  <-- Router starts sending traffic
60s     Boot timeout (if not listening)
```

**Pros:** Simple, no extra code needed
**Cons:** App might accept connections before truly ready

---

### Explicit Startup Probes (Cloud Run/K8s model)

Platform polls an endpoint until it returns success:

```yaml
startupProbe:
  httpGet:
    path: /healthz
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 10
  failureThreshold: 30  # Give app 5 minutes to start
```

**Startup vs Liveness vs Readiness:**

| Probe | Purpose | On Failure |
|-------|---------|------------|
| Startup | Is the app initialized? | Keep waiting (don't run other probes) |
| Liveness | Is the app stuck/deadlocked? | Restart the container |
| Readiness | Can the app handle traffic right now? | Remove from load balancer |

---

### Health Check Endpoints

Best practice is lightweight endpoints:

```go
// Liveness: "Am I running?"
func livenessHandler(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
}

// Readiness: "Am I ready for traffic?"
func readinessHandler(w http.ResponseWriter, r *http.Request) {
    if !db.Ping() || !cache.Ping() {
        w.WriteHeader(http.StatusServiceUnavailable)
        return
    }
    w.WriteHeader(http.StatusOK)
}
```

---

## Docker Compose: Internal Service Discovery

Docker Compose provides DNS-based discovery without explicit port injection:

```yaml
services:
  web:
    build: ./web
    environment:
      - API_URL=http://api:8080  # Use service name
    ports:
      - "3000:3000"  # Expose to host

  api:
    build: ./api
    # No ports exposed - only reachable by other services
    expose:
      - "8080"
```

**Key differences from PaaS:**
- Apps can hardcode their ports (8080, 3000, etc.)
- Discovery is by service NAME, not port injection
- `ports` = exposed to host, `expose` = internal only

**Reference:** [Docker Compose Networking](https://docs.docker.com/compose/how-tos/networking/)

---

## systemd Socket Activation

Unix's original "environment injection" - systemd creates sockets, passes them to services:

```ini
# myapp.socket
[Socket]
ListenStream=8080

[Install]
WantedBy=sockets.target
```

```ini
# myapp.service
[Service]
ExecStart=/usr/bin/myapp
# Socket passed as fd 3 (or STDIN with Accept=yes)
```

**How it works:**
1. systemd creates socket, binds to port
2. Connection arrives, systemd starts service
3. Service receives socket as file descriptor
4. Service calls accept() on the socket

**Advantages:**
- Service doesn't need privileges to bind low ports
- On-demand activation (service only runs when needed)
- Seamless restarts (socket stays open)

**This is the ancestor of the 12-factor port pattern** - the idea that "something else" handles port binding.

**Reference:** [systemd socket activation](https://mgdm.net/weblog/systemd-socket-activation/)

---

## Summary: When to Use Environment Injection

### Use Environment Injection When:

- Building **new apps** that can read `$PORT`
- Deploying to **PaaS platforms** (Heroku, Cloud Run, Render)
- Running **multiple instances** that need different ports
- Following **12-factor principles**

### Handle Non-Compliant Apps By:

1. **Wrapper scripts** - Transform `$PORT` to app-specific flags
2. **Config templating** - Generate config files at startup
3. **Port forwarding** - socat or nginx sidecar
4. **Platform override** - Tell platform what port your app uses

### The Simplicity vs Flexibility Tension

| Approach | Simplicity | Flexibility |
|----------|------------|-------------|
| Environment Injection | High - just read `$PORT` | Requires app cooperation |
| Service Mesh (Envoy) | Low - sidecar complexity | Works with any app |
| Active Registration (Consul) | Medium - client library | App controls metadata |
| Reverse Tunnel | High - no app changes | Limited to HTTP |

**Environment injection wins when:**
- Apps are written/controlled by you
- Platform handles routing complexity
- Simplicity matters more than features

**Environment injection loses when:**
- Apps can't be modified
- Need sophisticated routing (canary, A/B)
- Need service mesh features (mTLS, tracing)

---

## Platform Quick Reference

| Platform | Default Port | Port Variable | Readiness Signal |
|----------|-------------|---------------|------------------|
| Heroku | Dynamic | `$PORT` | Bind within 60s |
| Cloud Run | 8080 | `$PORT` | Startup probe / TCP |
| Fly.io | Config-driven | `internal_port` | Health checks |
| Render | 10000 | `$PORT` | Port listening |
| Foreman | 5000 (+100/type) | `$PORT` | N/A (local) |
| Overmind | 5000 (+100/type) | `$PORT` | N/A (local) |
