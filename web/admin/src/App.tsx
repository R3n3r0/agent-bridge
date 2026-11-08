import { useCallback, useMemo, useState } from "react";
import { AdminApi } from "./api";
import { CliCard } from "./components/CliCard";
import { EmbeddingsCard } from "./components/EmbeddingsCard";
import { KeyForm } from "./components/KeyForm";
import { useEmbeddings } from "./hooks/useEmbeddings";
import { useLogins } from "./hooks/useLogins";

const KEY_STORAGE = "ab-key";

function storedKey(): string {
  try {
    return sessionStorage.getItem(KEY_STORAGE) ?? "";
  } catch {
    return "";
  }
}

export function App() {
  const [key, setKey] = useState(storedKey);
  const api = useMemo(() => new AdminApi(key), [key]);
  const { data, error, needsKey, refresh: refreshLogins } = useLogins(api);
  const embed = useEmbeddings(api, data !== null);
  const refreshEmbed = embed.refresh;
  const refresh = useCallback(() => {
    refreshLogins();
    refreshEmbed();
  }, [refreshLogins, refreshEmbed]);
  const [actionError, setActionError] = useState("");

  const useKey = (k: string) => {
    try {
      sessionStorage.setItem(KEY_STORAGE, k);
    } catch {
      // Storage may be unavailable: the key then lasts until reload.
    }
    setKey(k);
    refresh();
  };

  const act = async (fn: () => Promise<unknown>) => {
    try {
      await fn();
      setActionError("");
    } catch (e) {
      setActionError(e instanceof Error ? e.message : String(e));
      throw e;
    } finally {
      refresh();
    }
  };

  return (
    <main>
      <h1>agent-bridge admin</h1>
      <p className="lead">
        Log in to the CLIs on this server with their official login. The credentials stay with each CLI; the bridge never
        reads them.
      </p>
      <KeyForm initial={key} onUse={useKey} onRefresh={refresh} error={actionError || error} needsKey={needsKey} />
      {data && <h2>CLI logins</h2>}
      {data?.clis.map((c) => (
        <CliCard
          key={c.cli}
          cli={c}
          isBackend={c.cli === data.backend}
          onStart={() => act(() => api.start(c.cli)).catch(() => {})}
          onSendCode={(code) => act(() => api.sendCode(c.cli, code))}
          onCancel={() => act(() => api.cancel(c.cli)).catch(() => {})}
        />
      ))}
      {data && embed.data && (
        <>
          <h2>Embeddings</h2>
          <EmbeddingsCard
            data={embed.data}
            onPull={(m) => act(() => api.pullModel(m)).then(() => {})}
            onCancelPull={(m) => act(() => api.cancelPull(m)).catch(() => {})}
            onDelete={(m) => act(() => api.deleteModel(m)).catch(() => {})}
            onTest={async (m) => {
              const r = await api.testModel(m, "agent-bridge embedding test");
              return `${r.dimensions} dimensions, ${r.millis} ms`;
            }}
          />
        </>
      )}
    </main>
  );
}
