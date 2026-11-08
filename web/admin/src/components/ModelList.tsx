import type { Model } from "../api";
import { formatBytes } from "../format";
import { CopyButton } from "./CopyButton";

interface Props {
  models: Model[];
  onTest: (model: string) => void;
  onDelete: (model: string) => void;
}

// ModelList shows the installed models; models that are not embedding
// models (e.g. chat models) are listed but cannot be tested.
export function ModelList({ models, onTest, onDelete }: Props) {
  if (models.length === 0) return <p className="hint">No models installed yet: download one below.</p>;
  return (
    <ul className="models" aria-label="Installed models">
      {models.map((m) => (
        <li key={m.name} className="row head">
          <span>
            <b>{m.name}</b>{" "}
            <span className="hint">
              {formatBytes(m.size)}
              {m.parameter_size && ` · ${m.parameter_size}`}
              {m.family && ` · ${m.family}`}
            </span>{" "}
            {m.embedding === false && <span className="tag">not an embedding model</span>}
          </span>
          <span className="row">
            <CopyButton text={m.name} label="Copy name" />
            <button type="button" disabled={m.embedding === false} onClick={() => onTest(m.name)}>
              Test
            </button>
            <button type="button" onClick={() => onDelete(m.name)}>
              Delete
            </button>
          </span>
        </li>
      ))}
    </ul>
  );
}
