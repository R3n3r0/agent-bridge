import { useState, type FormEvent } from "react";

interface Props {
  initial: string;
  onUse: (key: string) => void;
  onRefresh: () => void;
  error: string;
  needsKey: boolean;
}

export function KeyForm({ initial, onUse, onRefresh, error, needsKey }: Props) {
  const [value, setValue] = useState(initial);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    onUse(value.trim());
  };
  return (
    <form className="card" onSubmit={submit}>
      <div className="row">
        <input
          type="password"
          aria-label="Bridge API key"
          placeholder="Bridge API key (AGENT_BRIDGE_API_KEY)"
          autoComplete="off"
          value={value}
          onChange={(e) => setValue(e.target.value)}
        />
        <button type="submit" className="primary">
          Use key
        </button>
        <button type="button" onClick={onRefresh}>
          Refresh
        </button>
      </div>
      <div className="err" role="alert">
        {needsKey ? "Enter the bridge API key to continue." : error}
      </div>
    </form>
  );
}
