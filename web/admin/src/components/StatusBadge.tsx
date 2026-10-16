import type { CliStatus } from "../api";

// statusLabel is the text of the badge of a CLI.
export function statusLabel(c: CliStatus): string {
  if (c.flow?.state === "running") return "login in progress";
  if (!c.installed) return "not installed";
  if (c.logged_in === true) return "logged in";
  if (c.logged_in === false) return "not logged in";
  return "unknown";
}

const tone: Record<string, string> = {
  "login in progress": "run",
  "not installed": "bad",
  "logged in": "ok",
  "not logged in": "bad",
  unknown: "none",
};

export function StatusBadge({ cli }: { cli: CliStatus }) {
  const label = statusLabel(cli);
  return <span className={`badge ${tone[label]}`}>{label}</span>;
}
