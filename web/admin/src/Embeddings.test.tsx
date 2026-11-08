import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { App } from "./App";
import type { Embeddings } from "./api";

interface Call {
  method: string;
  url: string;
  body?: string;
}

let calls: Call[];
let embed: Embeddings;

beforeEach(() => {
  calls = [];
  sessionStorage.setItem("ab-key", "k");
  embed = {
    enabled: true,
    client_url: "http://127.0.0.1:11434/v1",
    reachable: true,
    version: "0.12.0",
    models: [
      { name: "bge-m3:latest", size: 1157672605, modified_at: "2026-11-08T10:00:00Z", family: "bert", parameter_size: "566.70M", embedding: true },
      { name: "llama3.2:latest", size: 2019393189, modified_at: "2026-11-08T10:00:00Z", embedding: false },
    ],
    pulls: [],
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init: RequestInit) => {
      const method = init.method ?? "GET";
      calls.push({ method, url, body: init.body as string | undefined });
      if (url.endsWith("/logins")) return new Response(JSON.stringify({ backend: "codex", clis: [] }), { status: 200 });
      if (method === "GET") return new Response(JSON.stringify(embed), { status: 200 });
      if (url.endsWith("/embeddings/test")) {
        const { model } = JSON.parse(init.body as string);
        if (model !== "bge-m3:latest") return new Response(JSON.stringify({ error: { message: "no embeddings" } }), { status: 400 });
        return new Response(JSON.stringify({ model, dimensions: 1024, millis: 85 }), { status: 200 });
      }
      if (url.endsWith("/embeddings/pulls") && method === "POST") {
        const { model } = JSON.parse(init.body as string);
        embed.pulls = [{ model, state: "running", status: "pulling 970aa74c", completed: 400e6, total: 1000e6, started_at: "2026-11-08T10:01:00Z" }];
        return new Response(JSON.stringify(embed.pulls[0]), { status: 200 });
      }
      return new Response(null, { status: 204 });
    }),
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  sessionStorage.clear();
});

async function section() {
  render(<App />);
  return within(await screen.findByRole("region", { name: "Embedding models" }));
}

test("shows the endpoint and the installed models", async () => {
  const s = await section();
  expect(s.getByText("Ollama 0.12.0")).toBeTruthy();
  expect(s.getByText("http://127.0.0.1:11434/v1")).toBeTruthy();
  expect(s.getByText("bge-m3:latest")).toBeTruthy();
  expect(s.getByText("not an embedding model")).toBeTruthy();
  expect(s.getByRole("button", { name: "Download bge-m3" }).textContent).toBe("Installed");
});

test("tests a model", async () => {
  const s = await section();
  fireEvent.click(s.getAllByText("Test")[0]);
  expect(await s.findByText("bge-m3:latest: 1024 dimensions, 85 ms")).toBeTruthy();
});

test("downloads a suggested model and shows the progress", async () => {
  const s = await section();
  fireEvent.click(s.getByRole("button", { name: "Download nomic-embed-text" }));
  await waitFor(() => expect(calls.some((c) => c.method === "POST" && c.body === '{"model":"nomic-embed-text"}')).toBe(true));
  expect(await s.findByText(/400 MB of 1.0 GB/)).toBeTruthy();
  fireEvent.click(s.getByText("Cancel"));
  await waitFor(() => expect(calls.some((c) => c.method === "DELETE" && c.url.endsWith("/embeddings/pulls?model=nomic-embed-text"))).toBe(true));
});

test("downloads a model by name and deletes one after confirmation", async () => {
  const s = await section();
  fireEvent.change(s.getByLabelText("Model name"), { target: { value: "hf.co/user/repo:Q4" } });
  const downloads = s.getAllByText("Download");
  fireEvent.click(downloads[downloads.length - 1]);
  await waitFor(() => expect(calls.some((c) => c.body === '{"model":"hf.co/user/repo:Q4"}')).toBe(true));
  vi.stubGlobal("confirm", vi.fn(() => true));
  fireEvent.click(s.getAllByText("Delete")[1]);
  await waitFor(() => expect(calls.some((c) => c.method === "DELETE" && c.url.endsWith("/embeddings/models?model=llama3.2%3Alatest"))).toBe(true));
});

test("explains how to start Ollama when it is not reachable", async () => {
  embed = { enabled: true, reachable: false, client_url: "http://127.0.0.1:11434/v1", error: "ollama is not reachable at http://ollama:11434", models: [], pulls: [] };
  const s = await section();
  expect(s.getByText("Ollama not reachable")).toBeTruthy();
  expect(s.getByText(/ollama is not reachable at/)).toBeTruthy();
  expect(s.queryByText("Download")).toBeNull();
});
