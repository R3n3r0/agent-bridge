import { useCallback, useEffect, useRef, useState } from "react";
import { AdminApi, type Embeddings } from "../api";

const POLL_MS = 2000;

export interface EmbeddingsState {
  data: Embeddings | null;
  error: string;
  refresh: () => void;
}

// useEmbeddings loads the embedding models and refreshes them every two
// seconds while a download is running. It waits for active (the key
// accepted) before the first request. Late responses are ignored.
export function useEmbeddings(api: AdminApi, active: boolean): EmbeddingsState {
  const [data, setData] = useState<Embeddings | null>(null);
  const [error, setError] = useState("");
  const seq = useRef(0);
  const [tick, setTick] = useState(0);

  const refresh = useCallback(() => setTick((t) => t + 1), []);

  useEffect(() => {
    const mine = ++seq.current;
    if (!active) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    api
      .embeddings()
      .then((d) => {
        if (mine !== seq.current) return;
        setData(d);
        setError("");
        if (d.pulls.some((p) => p.state === "running")) timer = setTimeout(refresh, POLL_MS);
      })
      .catch((e: unknown) => {
        if (mine !== seq.current) return;
        setData(null);
        setError(e instanceof Error ? e.message : String(e));
      });
    return () => clearTimeout(timer);
  }, [api, active, tick, refresh]);

  return { data, error, refresh };
}
