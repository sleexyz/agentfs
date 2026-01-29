import type { Checkpoint, Delta, Index, Manifest } from './types';

const API_BASE = import.meta.env.DEV ? 'http://localhost:3000' : '';

async function fetchJSON<T>(path: string): Promise<T> {
  const response = await fetch(`${API_BASE}${path}`);
  if (!response.ok) {
    throw new Error(`API error: ${response.status} ${response.statusText}`);
  }
  return response.json();
}

export const api = {
  async getCheckpoints(): Promise<Checkpoint[]> {
    return fetchJSON<Checkpoint[]>('/api/checkpoints');
  },

  async getManifest(version: number): Promise<Manifest> {
    return fetchJSON<Manifest>(`/api/manifest/${version}`);
  },

  async getDiff(v1: number, v2: number): Promise<Delta> {
    return fetchJSON<Delta>(`/api/diff/${v1}/${v2}`);
  },

  async getIndex(): Promise<Index> {
    return fetchJSON<Index>('/api/index');
  },
};
