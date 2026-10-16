import { useState, type FormEvent } from "react";

interface Props {
  onSend: (code: string) => Promise<void>;
}

// CodeInput is where the operator pastes the code the sign-in page shows.
// Its value is component state, so the periodic refresh of the page never
// clears it.
export function CodeInput({ onSend }: Props) {
  const [code, setCode] = useState("");
  const [sending, setSending] = useState(false);
  const [sent, setSent] = useState(false);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const text = code.trim();
    if (!text || sending) return;
    setSending(true);
    try {
      await onSend(text);
      setCode("");
      setSent(true);
    } catch {
      // The page shows the error; the code stays to be sent again.
    } finally {
      setSending(false);
    }
  };
  return (
    <form onSubmit={submit}>
      <div className="row">
        <input
          aria-label="Code from the sign-in page"
          placeholder="Code shown after signing in"
          autoComplete="off"
          spellCheck={false}
          value={code}
          onChange={(e) => setCode(e.target.value)}
        />
        <button type="submit" className="primary" disabled={sending || !code.trim()}>
          Send code
        </button>
      </div>
      {sent && <div className="hint">Code sent: waiting for the CLI to confirm…</div>}
    </form>
  );
}
