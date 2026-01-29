import type { Checkpoint } from '../types';
import './Timeline.css';

interface TimelineProps {
  checkpoints: Checkpoint[];
  selectedVersion: number | null;
  onSelect: (version: number) => void;
}

export function Timeline({ checkpoints, selectedVersion, onSelect }: TimelineProps) {
  if (checkpoints.length === 0) {
    return <div className="timeline-empty">No checkpoints</div>;
  }

  // Sort by version ascending for display
  const sorted = [...checkpoints].sort((a, b) => a.version - b.version);

  return (
    <div className="timeline">
      <div className="timeline-track">
        {sorted.map((cp, index) => (
          <TimelineMarker
            key={cp.version}
            checkpoint={cp}
            isSelected={cp.version === selectedVersion}
            isFirst={index === 0}
            isLast={index === sorted.length - 1}
            onClick={() => onSelect(cp.version)}
          />
        ))}
      </div>
    </div>
  );
}

interface TimelineMarkerProps {
  checkpoint: Checkpoint;
  isSelected: boolean;
  isFirst: boolean;
  isLast: boolean;
  onClick: () => void;
}

function TimelineMarker({ checkpoint, isSelected, isFirst, isLast, onClick }: TimelineMarkerProps) {
  const hasChanges = checkpoint.summary.added + checkpoint.summary.modified + checkpoint.summary.deleted > 0;

  return (
    <div className={`timeline-marker-container ${isFirst ? 'first' : ''} ${isLast ? 'last' : ''}`}>
      {!isFirst && <div className="timeline-line" />}
      <button
        className={`timeline-marker ${isSelected ? 'selected' : ''} ${hasChanges ? 'has-changes' : ''}`}
        onClick={onClick}
        title={formatTooltip(checkpoint)}
      >
        <div className="marker-dot" />
        <div className="marker-label">v{checkpoint.version}</div>
      </button>
    </div>
  );
}

function formatTooltip(cp: Checkpoint): string {
  const parts: string[] = [`v${cp.version}`];

  if (cp.message) {
    parts.push(cp.message);
  }

  const changes: string[] = [];
  if (cp.summary.added > 0) changes.push(`+${cp.summary.added}`);
  if (cp.summary.modified > 0) changes.push(`~${cp.summary.modified}`);
  if (cp.summary.deleted > 0) changes.push(`-${cp.summary.deleted}`);

  if (changes.length > 0) {
    parts.push(`(${changes.join(', ')})`);
  }

  parts.push(new Date(cp.timestamp).toLocaleString());

  return parts.join('\n');
}
