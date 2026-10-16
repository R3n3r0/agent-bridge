// Client of the bridge's admin API (/admin/api/logins).

export type FlowState = "running" | "succeeded" | "failed" | "canceled";

export interface Flow {
  state: FlowState;
  output: string;
  urls: string[];
  codes: string[];
  started_at: string;
  accepts_input: boolean;
}

export interface CliStatus {
  cli: string;
  binary: string;
  installed: boolean;
  logged_in: boolean | null;
  detail?: string;
  flow?: Flow;
}

export interface Logins {
  backend: string;
  clis: CliStatus[];
}

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

const BASE = "/admin/api/logins";

export class AdminApi {
  constructor(private readonly key: string) {}

  private async call<T>(method: string, path: string, body?: unknown): Promise<T> {
    // The admin header is required on changes: other sites cannot send it.
    const headers: Record<string, string> = { "X-Agent-Bridge-Admin": "1" };
    if (this.key) headers["Authorization"] = `Bearer ${this.key}`;
    if (body !== undefined) headers["Content-Type"] = "application/json";
    const r = await fetch(BASE + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
    if (r.status === 204) return undefined as T;
    const json = await r.json().catch(() => ({}));
    if (!r.ok) throw new ApiError(json?.error?.message ?? r.statusText, r.status);
    return json as T;
  }

  logins(): Promise<Logins> {
    return this.call("GET", "");
  }

  start(cli: string): Promise<Flow> {
    return this.call("POST", `/${cli}`);
  }

  sendCode(cli: string, text: string): Promise<void> {
    return this.call("POST", `/${cli}/input`, { text });
  }

  cancel(cli: string): Promise<void> {
    return this.call("DELETE", `/${cli}`);
  }
}
