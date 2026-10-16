import { useMemo, useState } from "react";
import { AdminApi } from "./api";
import { CliCard } from "./components/CliCard";
import { KeyForm } from "./components/KeyForm";
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
  const { data, error, needsKey, refresh } = useLogins(api);
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
      <h1>agent-bridge logins</h1>
      <p className="lead">
        Log in to the CLIs on this server with their official login. The credentials stay with each CLI; the bridge never
        reads them.
      </p>
      <KeyForm initial={key} onUse={useKey} onRefresh={refresh} error={actionError || error} needsKey={needsKey} />
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
    </main>
  );
}
