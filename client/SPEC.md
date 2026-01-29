# AgentFS Timeline Visualizer

A smooth, fast visualization of filesystem changes over time with a scrubber interface.

## Core Experience

```
┌─────────────────────────────────────────────────────────────────────┐
│  AgentFS Timeline                                        v1 → v12   │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  ▼ src/                                                             │
│    ├── main.go                                    ● modified        │
│    ├── config.go                                  + added           │
│    ▼ internal/                                                      │
│      ├── diff/                                                      │
│      │   └── diff.go                              ● modified        │
│      └── mount/                                                     │
│          └── mount.go                                               │
│  ▶ .git/                                          (collapsed)       │
│  ▶ node_modules/                                  (collapsed)       │
│  ├── README.md                                    - deleted         │
│  └── go.mod                                                         │
│                                                                     │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  ○───○───○───●───○───○───○───○───○───○───○───○                      │
│  v1  v2  v3  v4  v5  v6  v7  v8  v9  v10 v11 v12                   │
│              ↑                                                      │
│           current                                                   │
│                                                                     │
│  [◀◀] [▶ Play] [▶▶]                    Speed: [1x ▼]               │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

## Features

### File Tree
- Expandable/collapsible directory tree
- Auto-collapse: `.git/`, `node_modules/`, `.agentfs/`, `vendor/`
- Change indicators: `+` added (green), `●` modified (yellow), `-` deleted (red, strikethrough)
- Smooth highlight animations when changes occur
- Click file to see diff content (optional panel)

### Timeline Scrubber
- Drag to scrub through checkpoints
- Keyboard: arrow keys for step, shift+arrow for jump
- Visual markers for each checkpoint
- Timestamp tooltips on hover
- Smooth interpolation between states

### Playback Controls
- Play/pause auto-advance through timeline
- Speed control: 0.5x, 1x, 2x, 4x
- Step forward/backward buttons
- Loop toggle

### View Modes
1. **Incremental**: Show changes from previous checkpoint (v3 → v4)
2. **Cumulative**: Show all changes from baseline (v1 → v4)
3. **Heatmap**: Color intensity by change frequency

## Technology Stack

### Recommended: Svelte + Tauri

| Layer | Technology | Why |
|-------|------------|-----|
| **UI Framework** | **Svelte 5** | Compiles to vanilla JS, fine-grained reactivity perfect for scrubbing, built-in transitions/animations, tiny bundle |
| **Desktop Shell** | **Tauri 2** | Rust backend (pairs with Go CLI), ~10MB binary, native performance, can shell out to `agentfs` commands |
| **File Tree** | **Custom + virtual scroll** | Need full control for 60fps updates; tanstack-virtual or svelte-virtual-list for virtualization |
| **Timeline** | **Canvas 2D** | Smooth scrubbing, custom rendering, no DOM overhead |
| **Animations** | **Svelte motion** | Spring physics, tweened values, tied to reactivity |
| **State** | **Svelte stores** | Simple, reactive, perfect for this scale |

### Why Svelte over React

For scrubber UIs, React's reconciliation is the bottleneck. When scrubbing at 60fps:
- React: diff virtual DOM → reconcile → commit → paint (often drops frames)
- Svelte: direct DOM updates via compiled reactivity (consistent 60fps)

Svelte's `tweened` and `spring` stores are built for exactly this kind of smooth interpolation.

### Alternative: Web-only (no Tauri)

Could serve from `agentfs serve` command:
```bash
agentfs serve --port 3000  # Serves visualization + JSON API
```

Simpler deployment, but loses native file watching and shell integration.

## Data Architecture

### Index Structure (computed on app start)

```typescript
interface Index {
  mountPath: string;
  checkpoints: Checkpoint[];
  // Pre-computed for instant lookup
  manifests: Map<number, Manifest>;        // version → full file list
  deltas: Map<string, Delta>;              // "v1:v2" → changes between them
}

interface Checkpoint {
  version: number;
  timestamp: Date;
  fileCount: number;
  summary: { added: number; modified: number; deleted: number };
}

interface Manifest {
  files: Map<string, FileInfo>;
}

interface FileInfo {
  path: string;
  size: number;
  mtime: number;
  mode: number;
  isDir: boolean;
  isSymlink: boolean;
}

interface Delta {
  added: string[];
  modified: string[];
  deleted: string[];
}
```

### Index Generation (on app start)

```
1. List all checkpoints from .agentfs/
2. For each checkpoint (can parallelize):
   a. Mount sparse bundle (or use cached manifest if exists)
   b. Walk and build manifest
   c. Unmount
3. Compute deltas between adjacent checkpoints
4. Cache to .agentfs/index.json for faster subsequent loads
```

This is a one-time cost on startup (~5s for 20 checkpoints). After that, everything is in memory.

### Write-Through Cache (Future)

When `agentfs checkpoint` runs, it can optionally write:
```
.agentfs/bands/v5/manifest.json
```

If present, skip mounting during index generation. But this is an optimization, not required.

## File Tree Component

### Virtual Scrolling

Only render visible nodes. For a tree with 10,000 files but 50 visible rows:
- Render ~60 nodes (50 + buffer)
- Update on scroll
- Critical for smooth scrubbing

### Tree State

```typescript
interface TreeState {
  expanded: Set<string>;      // Expanded directory paths
  nodes: TreeNode[];          // Flattened visible nodes
  changes: Map<string, ChangeType>;  // path → 'added' | 'modified' | 'deleted'
}

interface TreeNode {
  path: string;
  name: string;
  depth: number;
  isDir: boolean;
  isExpanded: boolean;
  hasChildren: boolean;
  change?: ChangeType;
}
```

### Default Collapsed

```typescript
const AUTO_COLLAPSE = [
  '.git',
  'node_modules',
  '.agentfs',
  'vendor',
  '__pycache__',
  '.next',
  'dist',
  'build',
];
```

## Timeline Component

### Canvas Rendering

The timeline should use Canvas for smooth scrubbing:
- Draw checkpoint markers
- Draw connections/lines
- Highlight current position
- Handle drag events
- Render at device pixel ratio for crisp display

### Scrub Interpolation

When dragging between v3 and v4, interpolate the visual position but snap the data:
```typescript
// Visual position can be anywhere
let visualPosition = 3.7;

// Data always snaps to integer version
let activeVersion = Math.round(visualPosition);
```

### Animation on Checkpoint Change

When activeVersion changes:
1. Compute delta from previous displayed version
2. Animate out removed files (fade + slide)
3. Animate in added files (fade + slide)
4. Pulse modified files

Use Svelte's `crossfade` transition for this.

## Performance Targets

| Metric | Target |
|--------|--------|
| Initial load (20 checkpoints) | < 5s |
| Subsequent load (cached) | < 500ms |
| Scrub latency | < 16ms (60fps) |
| Tree render (10k files visible) | < 16ms |
| Memory (100 checkpoints, 50k files) | < 200MB |

## CLI Integration

### New Commands

```bash
# Serve the visualization
agentfs ui [--port 3000]

# Export index as JSON (for external tools)
agentfs index [--output index.json]

# Watch mode - stream changes
agentfs watch --json
```

### JSON API (if web-served)

```
GET /api/checkpoints         → list checkpoints
GET /api/manifest/:version   → file manifest for version
GET /api/diff/:v1/:v2        → delta between versions
GET /api/file/:version/*path → file content at version
WS  /api/watch               → live updates
```

## Implementation Phases

### Phase 1: Static Viewer
- [ ] Svelte project setup
- [ ] Index generation (shell out to `agentfs diff --json`)
- [ ] File tree component (no virtualization yet)
- [ ] Basic timeline with click-to-select
- [ ] Change highlighting

### Phase 2: Smooth Scrubbing
- [ ] Canvas timeline with drag
- [ ] Virtual scrolling for tree
- [ ] Animated transitions
- [ ] Keyboard navigation

### Phase 3: Desktop App
- [ ] Tauri integration
- [ ] Native file watching
- [ ] System tray / menu bar
- [ ] Auto-update

### Phase 4: Advanced Features
- [ ] Diff panel (show file contents)
- [ ] Heatmap mode
- [ ] Search/filter
- [ ] Bookmark checkpoints
- [ ] Export timeline as video/gif

## Open Questions

1. **Web vs Desktop first?** - Web is simpler to start, can always wrap in Tauri later
2. **Live updates?** - Watch for new checkpoints while running?
3. **Multi-mount support?** - Visualize multiple agentfs mounts?
4. **Diff content view?** - Show actual file diffs or just tree changes?

## Inspiration

- **Git heatmaps** (gource) - animated repository visualization
- **Time Machine UI** - Apple's backup scrubber
- **VS Code timeline** - file history in sidebar
- **Replay.io** - time-travel debugging UI
