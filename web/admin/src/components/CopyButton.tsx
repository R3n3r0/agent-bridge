import { useState } from "react";

export function CopyButton({ text }: { text: string }) {
  const [label, setLabel] = useState("Copy");
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setLabel("Copied");
    } catch {
      setLabel("Select and copy");
    }
    setTimeout(() => setLabel("Copy"), 1500);
  };
  return (
    <button type="button" onClick={copy}>
      {label}
    </button>
  );
}
