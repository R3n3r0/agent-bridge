import { useCallback, useEffect, useRef, useState } from "react";
import { AdminApi, ApiError, type Logins } from "../api";

const POLL_MS = 2000;

export interface LoginsState {
  data: Logins | null;
  error: string;
  needsKey: boolean;
  refresh: () => void;
}

// useLogins loads the state of the CLIs and refreshes it every two seconds
// while a login is running. Late responses of older requests are ignored.
export function useLogins(api: AdminApi): LoginsState {
  const [data, setData] = useState<Logins | null>(null);
  const [error, setError] = useState("");
  const [needsKey, setNeedsKey] = useState(false);
  const seq = useRef(0);
  const [tick, setTick] = useState(0);

  const refresh = useCallback(() => setTick((t) => t + 1), []);

  useEffect(() => {
    const mine = ++seq.current;
    let timer: ReturnType<typeof setTimeout> | undefined;
    api
      .logins()
      .then((d) => {
        if (mine !== seq.current) return;
        setData(d);
        setError("");
        setNeedsKey(false);
        if (d.clis.some((c) => c.flow?.state === "running")) timer = setTimeout(refresh, POLL_MS);
      })
      .catch((e: unknown) => {
        if (mine !== seq.current) return;
        setData(null);
        const unauthorized = e instanceof ApiError && e.status === 401;
        setNeedsKey(unauthorized);
        setError(unauthorized ? "" : e instanceof Error ? e.message : String(e));
      });
    return () => clearTimeout(timer);
  }, [api, tick, refresh]);

  return { data, error, needsKey, refresh };
}
