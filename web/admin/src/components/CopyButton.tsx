import { useState } from "react";

export function CopyButton({ text, label: idle = "Copy" }: { text: string; label?: string }) {
  const [label, setLabel] = useState(idle);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setLabel("Copied");
    } catch {
      setLabel("Select and copy");
    }
    setTimeout(() => setLabel(idle), 1500);
  };
  return (
    <button type="button" onClick={copy}>
      {label}
    </button>
  );
}
