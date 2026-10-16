import { useState, type ReactNode } from "react";
import type { Flow } from "../api";
import { CodeInput } from "./CodeInput";
import { CopyButton } from "./CopyButton";

interface Props {
  flow: Flow;
  onSendCode: (code: string) => Promise<void>;
  onCancel: () => void;
}

function Step({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <li>
      <div className="step-title">{title}</div>
      {children}
    </li>
  );
}

const outcome: Record<string, string> = {
  succeeded: "Login completed.",
  canceled: "Login canceled.",
  failed: "Login failed: see the CLI output below.",
};

// LoginFlow guides the operator through a running login and shows how it
// ended.
export function LoginFlow({ flow, onSendCode, onCancel }: Props) {
  const running = flow.state === "running";
  const [showOutput, setShowOutput] = useState(flow.state === "failed");
  return (
    <div>
      {running ? (
        <>
          <ol className="steps">
            {flow.urls.length > 0 && (
              <Step title="Open the sign-in page and log in">
                <a className="btn primary" href={flow.urls[0]} target="_blank" rel="noopener noreferrer">
                  Open sign-in page ↗
                </a>
              </Step>
            )}
            {flow.codes.map((code) => (
              <Step key={code} title="Enter this one-time code if the page asks for it">
                <div className="row">
                  <span className="code">{code}</span>
                  <CopyButton text={code} />
                </div>
              </Step>
            ))}
            {flow.accepts_input && (
              <Step title="Paste the code the sign-in page gives you">
                <CodeInput onSend={onSendCode} />
              </Step>
            )}
            <Step title="Wait here: the state updates by itself">
              <div className="hint">Started {new Date(flow.started_at).toLocaleTimeString()}</div>
            </Step>
          </ol>
          <button type="button" onClick={onCancel}>
            Cancel login
          </button>
        </>
      ) : (
        <div className={`outcome ${flow.state === "succeeded" ? "ok" : "bad"}`}>{outcome[flow.state]}</div>
      )}
      <details open={showOutput} onToggle={(e) => setShowOutput(e.currentTarget.open)}>
        <summary>CLI output</summary>
        <pre>{flow.output || "(waiting for the CLI…)"}</pre>
      </details>
    </div>
  );
}
