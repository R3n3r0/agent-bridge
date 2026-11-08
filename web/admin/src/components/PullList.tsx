import type { Pull } from "../api";
import { formatBytes } from "../format";

interface Props {
  pulls: Pull[];
  onCancel: (model: string) => void;
}

// PullList shows the downloads in progress and how the recent ones ended.
export function PullList({ pulls, onCancel }: Props) {
  if (pulls.length === 0) return null;
  return (
    <ul className="pulls" aria-label="Downloads">
      {pulls.map((p) => {
        const pct = p.total > 0 ? Math.min(100, Math.round((p.completed / p.total) * 100)) : null;
        return (
          <li key={p.model + p.started_at}>
            <div className="row head">
              <span>
                <b>{p.model}</b>{" "}
                {p.state === "running" && (
                  <span className="hint">
                    {p.status}
                    {pct !== null && ` · ${formatBytes(p.completed)} of ${formatBytes(p.total)}`}
                  </span>
                )}
                {p.state === "succeeded" && <span className="outcome ok">downloaded</span>}
                {p.state === "canceled" && <span className="hint">download canceled</span>}
                {p.state === "failed" && <span className="outcome bad">download failed: {p.error}</span>}
              </span>
              {p.state === "running" && (
                <button type="button" onClick={() => onCancel(p.model)}>
                  Cancel
                </button>
              )}
            </div>
            {p.state === "running" && <progress max={100} value={pct ?? undefined} aria-label={`${p.model} progress`} />}
          </li>
        );
      })}
    </ul>
  );
}
