import { useState, type FormEvent } from "react";

// Suggested models: name, approximate download size, what it is good for.
const SUGGESTED: [string, string, string][] = [
  ["bge-m3", "~1.2 GB", "multilingual, Italian included"],
  ["nomic-embed-text", "~270 MB", "English, fast"],
  ["mxbai-embed-large", "~670 MB", "English, more accurate"],
  ["all-minilm", "~46 MB", "tiny, for tests"],
];

interface Props {
  installed: Set<string>;
  onPull: (model: string) => Promise<void>;
}

export function PullForm({ installed, onPull }: Props) {
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);

  const pull = async (model: string) => {
    setBusy(true);
    try {
      await onPull(model);
      setName("");
    } catch {
      // The error is shown by the page; the name stays to fix it.
    } finally {
      setBusy(false);
    }
  };
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (name.trim()) void pull(name.trim());
  };

  return (
    <div className="pull">
      <div className="step-title">Download a model</div>
      <ul className="suggest">
        {SUGGESTED.map(([model, size, what]) => {
          const have = installed.has(model) || installed.has(`${model}:latest`);
          return (
            <li key={model}>
              <button type="button" disabled={busy || have} onClick={() => void pull(model)} aria-label={`Download ${model}`}>
                {have ? "Installed" : "Download"}
              </button>
              <span>
                <b>{model}</b> <span className="hint">{size} · {what}</span>
              </span>
            </li>
          );
        })}
      </ul>
      <form className="row" onSubmit={submit}>
        <input
          aria-label="Model name"
          placeholder="Other model from ollama.com/search?c=embedding, e.g. snowflake-arctic-embed2"
          autoComplete="off"
          spellCheck={false}
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        <button type="submit" className="primary" disabled={busy || !name.trim()}>
          Download
        </button>
      </form>
    </div>
  );
}
