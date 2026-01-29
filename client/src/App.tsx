import { useEffect, useState, useMemo } from 'react';
import { api } from './api';
import { FileTree } from './components/FileTree';
import { Timeline } from './components/Timeline';
import type { ChangeType, Index, FileInfo } from './types';
import './App.css';

function App() {
  const [index, setIndex] = useState<Index | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [selectedVersion, setSelectedVersion] = useState<number | null>(null);

  // Load data on mount
  useEffect(() => {
    async function loadData() {
      try {
        setLoading(true);
        const data = await api.getIndex();
        setIndex(data);

        // Select the latest checkpoint by default
        if (data.checkpoints.length > 0) {
          const sorted = [...data.checkpoints].sort((a, b) => b.version - a.version);
          setSelectedVersion(sorted[0].version);
        }
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to load data');
      } finally {
        setLoading(false);
      }
    }

    loadData();
  }, []);

  // Get the manifest and changes for the selected version
  const { files, changes } = useMemo(() => {
    if (!index || selectedVersion === null) {
      return { files: {} as Record<string, FileInfo>, changes: new Map<string, ChangeType>() };
    }

    const manifest = index.manifests[`v${selectedVersion}`];
    if (!manifest) {
      return { files: {} as Record<string, FileInfo>, changes: new Map<string, ChangeType>() };
    }

    // Find the previous version to compute changes
    const sortedVersions = [...index.checkpoints]
      .map(cp => cp.version)
      .sort((a, b) => a - b);

    const currentIdx = sortedVersions.indexOf(selectedVersion);
    const changes = new Map<string, ChangeType>();

    if (currentIdx > 0) {
      const prevVersion = sortedVersions[currentIdx - 1];
      const deltaKey = `v${prevVersion}:v${selectedVersion}`;
      const delta = index.deltas[deltaKey];

      if (delta) {
        delta.added.forEach(path => changes.set(path, 'added'));
        delta.modified.forEach(path => changes.set(path, 'modified'));
        delta.deleted.forEach(path => changes.set(path, 'deleted'));
      }
    }

    return { files: manifest.files, changes };
  }, [index, selectedVersion]);

  // Get the selected checkpoint info
  const selectedCheckpoint = useMemo(() => {
    if (!index || selectedVersion === null) return null;
    return index.checkpoints.find(cp => cp.version === selectedVersion);
  }, [index, selectedVersion]);

  if (loading) {
    return (
      <div className="app loading">
        <div className="loading-spinner" />
        <p>Loading checkpoint data...</p>
      </div>
    );
  }

  if (error) {
    return (
      <div className="app error">
        <h1>Error</h1>
        <p>{error}</p>
        <p className="hint">Make sure <code>agentfs serve --cors</code> is running.</p>
      </div>
    );
  }

  if (!index) {
    return (
      <div className="app error">
        <h1>No Data</h1>
        <p>No checkpoint data available.</p>
      </div>
    );
  }

  return (
    <div className="app">
      <header className="app-header">
        <h1>AgentFS Timeline</h1>
        <span className="store-name">{index.storeName}</span>
      </header>

      <main className="app-main">
        <section className="file-tree-section">
          <header className="section-header">
            <h2>Files</h2>
            {selectedCheckpoint && (
              <span className="checkpoint-info">
                v{selectedCheckpoint.version}
                {selectedCheckpoint.message && ` — ${selectedCheckpoint.message}`}
              </span>
            )}
          </header>
          <div className="file-tree-container">
            <FileTree files={files} changes={changes} />
          </div>
        </section>

        <section className="timeline-section">
          <header className="section-header">
            <h2>Timeline</h2>
            <span className="checkpoint-count">
              {index.checkpoints.length} checkpoint{index.checkpoints.length !== 1 ? 's' : ''}
            </span>
          </header>
          <Timeline
            checkpoints={index.checkpoints}
            selectedVersion={selectedVersion}
            onSelect={setSelectedVersion}
          />
          {selectedCheckpoint && (
            <div className="checkpoint-summary">
              <SummaryBadge type="added" count={selectedCheckpoint.summary.added} />
              <SummaryBadge type="modified" count={selectedCheckpoint.summary.modified} />
              <SummaryBadge type="deleted" count={selectedCheckpoint.summary.deleted} />
            </div>
          )}
        </section>
      </main>
    </div>
  );
}

function SummaryBadge({ type, count }: { type: ChangeType; count: number }) {
  if (count === 0) return null;

  const label = type === 'added' ? '+' : type === 'modified' ? '~' : '-';
  return (
    <span className={`summary-badge ${type}`}>
      {label}{count}
    </span>
  );
}

export default App;
