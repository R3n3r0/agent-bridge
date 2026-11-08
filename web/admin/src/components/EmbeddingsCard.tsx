import { useState } from "react";
import type { Embeddings } from "../api";
import { CopyButton } from "./CopyButton";
import { ModelList } from "./ModelList";
import { PullForm } from "./PullForm";
import { PullList } from "./PullList";

interface Props {
  data: Embeddings;
  onPull: (model: string) => Promise<void>;
  onCancelPull: (model: string) => void;
  onDelete: (model: string) => void;
  onTest: (model: string) => Promise<string>;
}

// EmbeddingsCard manages the local embedding models served by Ollama.
// Clients call Ollama's OpenAI-compatible endpoint directly.
export function EmbeddingsCard({ data, onPull, onCancelPull, onDelete, onTest }: Props) {
  const [test, setTest] = useState<{ model: string; text: string; ok: boolean } | null>(null);

  if (!data.enabled) {
    return (
      <section className="card" aria-label="Embedding models">
        <div className="row head">
          <span className="name">Embedding models</span>
          <span className="badge none">not enabled</span>
        </div>
        <p className="hint">
          Set <code>ollama_url</code> (or <code>AGENT_BRIDGE_OLLAMA_URL</code>) to the address of an Ollama server to manage
          local embedding models from here. With Docker Compose: <code>COMPOSE_PROFILES=embeddings</code> in <code>.env</code>,
          then <code>make up</code>.
        </p>
      </section>
    );
  }

  const runTest = async (model: string) => {
    setTest({ model, text: "computing a test embedding…", ok: true });
    try {
      setTest({ model, text: await onTest(model), ok: true });
    } catch (e) {
      setTest({ model, text: e instanceof Error ? e.message : String(e), ok: false });
    }
  };
  const remove = (model: string) => {
    if (window.confirm(`Delete ${model}? It can be downloaded again later.`)) onDelete(model);
  };
  const installed = new Set(data.models.map((m) => m.name));

  return (
    <section className="card" aria-label="Embedding models">
      <div className="row head">
        <span className="name">Embedding models</span>
        {data.reachable ? (
          <span className="badge ok">Ollama {data.version}</span>
        ) : (
          <span className="badge bad">Ollama not reachable</span>
        )}
      </div>
      {!data.reachable && (
        <p className="hint">
          {data.error} With Docker Compose, start the Ollama service: <code>COMPOSE_PROFILES=embeddings</code> in{" "}
          <code>.env</code>, then <code>make up</code>.
        </p>
      )}
      {data.client_url && (
        <div className="endpoint">
          <div className="step-title">Endpoint for your clients</div>
          <div className="row">
            <code className="url">{data.client_url}</code>
            <CopyButton text={data.client_url} />
          </div>
          <p className="hint">
            OpenAI-compatible: use it as the <code>base_url</code> of the embeddings client, with the model name below and any
            API key. Chat stays on this bridge.
          </p>
        </div>
      )}
      {data.reachable && (
        <>
          <ModelList models={data.models} onTest={(m) => void runTest(m)} onDelete={remove} />
          {test && (
            <p className={`outcome ${test.ok ? "ok" : "bad"}`} role="status">
              {test.model}: {test.text}
            </p>
          )}
          <PullList pulls={data.pulls} onCancel={onCancelPull} />
          <PullForm installed={installed} onPull={onPull} />
        </>
      )}
    </section>
  );
}
