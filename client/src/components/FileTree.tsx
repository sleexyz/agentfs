import { useMemo, useState } from 'react';
import type { ChangeType, FileInfo, TreeNode } from '../types';
import './FileTree.css';

// Directories to auto-collapse by default
const AUTO_COLLAPSE = new Set([
  '.git',
  'node_modules',
  '.agentfs',
  'vendor',
  '__pycache__',
  '.next',
  'dist',
  'build',
  '.cache',
  'coverage',
]);

interface FileTreeProps {
  files: Record<string, FileInfo>;
  changes: Map<string, ChangeType>;
}

export function FileTree({ files, changes }: FileTreeProps) {
  const [expanded, setExpanded] = useState<Set<string>>(() => {
    // Start with root directories expanded, except for AUTO_COLLAPSE dirs
    const initial = new Set<string>();
    initial.add(''); // Root is always expanded
    return initial;
  });

  const nodes = useMemo(() => {
    return buildTreeNodes(files, changes, expanded);
  }, [files, changes, expanded]);

  const toggleExpand = (path: string) => {
    setExpanded(prev => {
      const next = new Set(prev);
      if (next.has(path)) {
        next.delete(path);
      } else {
        next.add(path);
      }
      return next;
    });
  };

  if (nodes.length === 0) {
    return <div className="file-tree-empty">No files</div>;
  }

  return (
    <div className="file-tree">
      {nodes.map(node => (
        <FileTreeRow
          key={node.path}
          node={node}
          onToggle={() => toggleExpand(node.path)}
        />
      ))}
    </div>
  );
}

interface FileTreeRowProps {
  node: TreeNode;
  onToggle: () => void;
}

function FileTreeRow({ node, onToggle }: FileTreeRowProps) {
  const indent = node.depth * 16;

  return (
    <div
      className={`file-tree-row ${node.change || ''}`}
      style={{ paddingLeft: `${indent}px` }}
    >
      {node.isDir ? (
        <button className="expand-btn" onClick={onToggle}>
          {node.isExpanded ? '▼' : '▶'}
        </button>
      ) : (
        <span className="file-icon">  </span>
      )}
      <span className="node-name">
        {node.isDir ? (
          <span className="dir-name">{node.name}/</span>
        ) : (
          node.name
        )}
      </span>
      {node.change && (
        <span className={`change-indicator ${node.change}`}>
          {node.change === 'added' && '+'}
          {node.change === 'modified' && '●'}
          {node.change === 'deleted' && '−'}
        </span>
      )}
    </div>
  );
}

function buildTreeNodes(
  files: Record<string, FileInfo>,
  changes: Map<string, ChangeType>,
  expanded: Set<string>
): TreeNode[] {
  // Build directory structure
  const dirs = new Set<string>();
  const filePaths = Object.keys(files);

  // Collect all directory paths
  for (const path of filePaths) {
    const parts = path.split('/');
    let current = '';
    for (let i = 0; i < parts.length - 1; i++) {
      current = current ? `${current}/${parts[i]}` : parts[i];
      dirs.add(current);
    }
  }

  // Get change status for a directory (propagate from children)
  const getDirChange = (dirPath: string): ChangeType | undefined => {
    const prefix = dirPath ? dirPath + '/' : '';
    let hasAdded = false;
    let hasModified = false;
    let hasDeleted = false;

    for (const [path, change] of changes.entries()) {
      if (path.startsWith(prefix) || path === dirPath) {
        if (change === 'added') hasAdded = true;
        if (change === 'modified') hasModified = true;
        if (change === 'deleted') hasDeleted = true;
      }
    }

    if (hasAdded && hasDeleted) return 'modified'; // Mixed changes
    if (hasAdded) return 'added';
    if (hasDeleted) return 'deleted';
    if (hasModified) return 'modified';
    return undefined;
  };

  // Build flat list of visible nodes
  const nodes: TreeNode[] = [];
  const allPaths = [...dirs, ...filePaths].sort();

  // Track which directories are visible (all ancestors expanded)
  const isVisible = (path: string): boolean => {
    if (!path) return true;
    const parts = path.split('/');
    let current = '';
    for (let i = 0; i < parts.length - 1; i++) {
      current = current ? `${current}/${parts[i]}` : parts[i];
      if (!expanded.has(current)) return false;
    }
    return true;
  };

  // Check if a directory should be auto-collapsed
  const shouldAutoCollapse = (path: string): boolean => {
    const name = path.split('/').pop() || '';
    return AUTO_COLLAPSE.has(name);
  };

  // Process each path
  const seen = new Set<string>();
  for (const path of allPaths) {
    if (seen.has(path)) continue;
    seen.add(path);

    if (!isVisible(path)) continue;

    const isDir = dirs.has(path);
    const name = path.split('/').pop() || path;
    const depth = path.split('/').filter(Boolean).length;

    // Check if directory has children
    const hasChildren = isDir && allPaths.some(p => p.startsWith(path + '/'));

    // Determine if expanded
    // For auto-collapse dirs, they start collapsed unless explicitly expanded
    let isExpanded = expanded.has(path);
    if (isDir && !expanded.has(path) && !shouldAutoCollapse(path)) {
      // Non-auto-collapse dirs at depth 0-1 start expanded
      if (depth <= 1) {
        isExpanded = true;
      }
    }

    nodes.push({
      path,
      name,
      depth,
      isDir,
      isExpanded,
      hasChildren,
      change: isDir ? getDirChange(path) : changes.get(path),
      size: files[path]?.size,
    });
  }

  return nodes;
}
