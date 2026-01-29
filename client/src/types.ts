// Type definitions matching the Go API

export interface Checkpoint {
  version: number;
  message?: string;
  timestamp: string;
  fileCount: number;
  parentVersion?: number;
  summary: Summary;
}

export interface Summary {
  added: number;
  modified: number;
  deleted: number;
}

export interface FileInfo {
  path: string;
  size: number;
  mtime: number;
  mode: number;
  isDir: boolean;
  isSymlink: boolean;
}

export interface Manifest {
  version: number;
  files: Record<string, FileInfo>;
}

export interface Delta {
  fromVersion: number;
  toVersion: number;
  added: string[];
  modified: string[];
  deleted: string[];
}

export interface Index {
  mountPath: string;
  storePath: string;
  storeName: string;
  checkpoints: Checkpoint[];
  manifests: Record<string, Manifest>;
  deltas: Record<string, Delta>;
}

export type ChangeType = 'added' | 'modified' | 'deleted' | 'unchanged';

// File tree node for display
export interface TreeNode {
  path: string;
  name: string;
  depth: number;
  isDir: boolean;
  isExpanded: boolean;
  hasChildren: boolean;
  change?: ChangeType;
  size?: number;
}
