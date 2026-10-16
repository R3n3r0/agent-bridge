import type { CliStatus } from "../api";
import { LoginFlow } from "./LoginFlow";
import { StatusBadge, statusLabel } from "./StatusBadge";

interface Props {
  cli: CliStatus;
  isBackend: boolean;
  onStart: () => void;
  onSendCode: (code: string) => Promise<void>;
  onCancel: () => void;
}

export function CliCard({ cli, isBackend, onStart, onSendCode, onCancel }: Props) {
  const busy = cli.flow?.state === "running";
  const detail = cli.detail && cli.detail.toLowerCase().replace(/\.$/, "") !== statusLabel(cli) ? cli.detail : "";
  return (
    <section className="card" aria-label={cli.cli}>
      <div className="row head">
        <div className="title">
          <span className="name">{cli.cli}</span>
          {isBackend && <span className="tag">this server's backend</span>}
          <StatusBadge cli={cli} />
        </div>
        <button type="button" className={cli.logged_in ? "" : "primary"} disabled={!cli.installed || busy} onClick={onStart}>
          {cli.logged_in ? "Log in again" : "Log in"}
        </button>
      </div>
      {detail && <div className="detail">{detail}</div>}
      {/* A new login gets a new component, so its state starts empty. */}
      {cli.flow && <LoginFlow key={cli.flow.started_at} flow={cli.flow} onSendCode={onSendCode} onCancel={onCancel} />}
    </section>
  );
}
