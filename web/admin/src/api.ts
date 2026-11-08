// Client of the bridge's admin API (/admin/api): CLI logins and embedding
// models.

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

export interface Model {
  name: string;
  size: number;
  modified_at: string;
  family?: string;
  parameter_size?: string;
  // null when the server does not report the model's capabilities.
  embedding: boolean | null;
}

export type PullState = "running" | "succeeded" | "failed" | "canceled";

export interface Pull {
  model: string;
  state: PullState;
  status: string;
  completed: number;
  total: number;
  error?: string;
  started_at: string;
}

export interface Embeddings {
  enabled: boolean;
  client_url?: string;
  reachable: boolean;
  version?: string;
  error?: string;
  models: Model[];
  pulls: Pull[];
}

export interface TestResult {
  model: string;
  dimensions: number;
  millis: number;
}

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

const BASE = "/admin/api";

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
    return this.call("GET", "/logins");
  }

  start(cli: string): Promise<Flow> {
    return this.call("POST", `/logins/${cli}`);
  }

  sendCode(cli: string, text: string): Promise<void> {
    return this.call("POST", `/logins/${cli}/input`, { text });
  }

  cancel(cli: string): Promise<void> {
    return this.call("DELETE", `/logins/${cli}`);
  }

  embeddings(): Promise<Embeddings> {
    return this.call("GET", "/embeddings");
  }

  pullModel(model: string): Promise<Pull> {
    return this.call("POST", "/embeddings/pulls", { model });
  }

  cancelPull(model: string): Promise<void> {
    return this.call("DELETE", `/embeddings/pulls?model=${encodeURIComponent(model)}`);
  }

  deleteModel(model: string): Promise<void> {
    return this.call("DELETE", `/embeddings/models?model=${encodeURIComponent(model)}`);
  }

  testModel(model: string, text: string): Promise<TestResult> {
    return this.call("POST", "/embeddings/test", { model, text });
  }
}
