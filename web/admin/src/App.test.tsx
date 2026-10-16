import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { App } from "./App";
import type { Logins } from "./api";

interface Call {
  method: string;
  url: string;
  auth: string | null;
  admin: string | null;
  body?: string;
}

let calls: Call[];
let state: Logins;

function running(extra: Partial<NonNullable<Logins["clis"][0]["flow"]>>) {
  return {
    state: "running" as const,
    output: "",
    urls: ["https://sign-in.test/x"],
    codes: [],
    started_at: "2026-10-16T10:00:00Z",
    accepts_input: false,
    ...extra,
  };
}

beforeEach(() => {
  calls = [];
  sessionStorage.clear();
  state = {
    backend: "codex",
    clis: [
      { cli: "claude", binary: "claude", installed: true, logged_in: false, detail: "none" },
      { cli: "codex", binary: "codex", installed: true, logged_in: true, detail: "Logged in using ChatGPT" },
      { cli: "kimi", binary: "kimi", installed: false, logged_in: null, detail: "not installed" },
    ],
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init: RequestInit) => {
      const headers = (init.headers ?? {}) as Record<string, string>;
      calls.push({ method: init.method ?? "GET", url, auth: headers["Authorization"] ?? null, admin: headers["X-Agent-Bridge-Admin"] ?? null, body: init.body as string | undefined });
      if (headers["Authorization"] !== "Bearer k") {
        return new Response(JSON.stringify({ error: { message: "missing or invalid bearer token" } }), { status: 401 });
      }
      if (init.method === "GET") return new Response(JSON.stringify(state), { status: 200 });
      return new Response(null, { status: 204 });
    }),
  );
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

async function withKey() {
  render(<App />);
  await screen.findByText("Enter the bridge API key to continue.");
  fireEvent.change(screen.getByLabelText("Bridge API key"), { target: { value: "k" } });
  fireEvent.click(screen.getByText("Use key"));
  await screen.findByText("Logged in using ChatGPT");
}

test("asks for the key, then lists the CLIs", async () => {
  await withKey();
  expect(screen.getByText("this server's backend")).toBeTruthy();
  expect(screen.getAllByText("not logged in")).toHaveLength(1);
  expect(screen.getByText("not installed")).toBeTruthy();
  expect(sessionStorage.getItem("ab-key")).toBe("k");
  expect(calls.every((c) => c.admin === "1")).toBe(true);
});

test("starts a login", async () => {
  await withKey();
  fireEvent.click(screen.getAllByText("Log in")[0]);
  await waitFor(() => expect(calls.some((c) => c.method === "POST" && c.url === "/admin/api/logins/claude")).toBe(true));
});

test("shows the sign-in link and the one-time code", async () => {
  state.clis[1].flow = running({ codes: ["Z3C8-E3Q7O"] });
  await withKey();
  expect(screen.getByText("Z3C8-E3Q7O")).toBeTruthy();
  expect(screen.getByText("Open sign-in page ↗").getAttribute("href")).toBe("https://sign-in.test/x");
  fireEvent.click(screen.getByText("Cancel login"));
  await waitFor(() => expect(calls.some((c) => c.method === "DELETE" && c.url === "/admin/api/logins/codex")).toBe(true));
});

test("the refresh does not clear the code being typed, and the code is sent", async () => {
  state.clis[0].flow = running({ accepts_input: true });
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"], shouldAdvanceTime: true });
  await withKey();
  const input = screen.getByLabelText("Code from the sign-in page") as HTMLInputElement;
  fireEvent.change(input, { target: { value: "abc#st" } });
  const getCount = () => calls.filter((c) => c.method === "GET").length;
  for (let i = 0; i < 3; i++) {
    const before = getCount();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2100);
    });
    await waitFor(() => expect(getCount()).toBeGreaterThan(before));
  }
  expect((screen.getByLabelText("Code from the sign-in page") as HTMLInputElement).value).toBe("abc#st");

  fireEvent.change(input, { target: { value: "abc#state" } });
  fireEvent.click(screen.getByText("Send code"));
  await screen.findByText("Code sent: waiting for the CLI to confirm…");
  const sent = calls.find((c) => c.url === "/admin/api/logins/claude/input");
  expect(sent?.body).toBe(JSON.stringify({ text: "abc#state" }));
  expect(input.value).toBe("");
});

test("shows the outcome of a finished login", async () => {
  state.clis[0].flow = { ...running({}), state: "failed", output: "Login failed: 400" };
  await withKey();
  expect(screen.getByText("Login failed: see the CLI output below.")).toBeTruthy();
  expect(screen.getByText("Login failed: 400")).toBeTruthy();
});
