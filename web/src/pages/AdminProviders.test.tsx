import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AdminProviders } from "./AdminProviders";

type Call = { url: string; method: string; body?: unknown };

const calls: Call[] = [];
let responses: Record<string, unknown> = {};

function installFetch() {
  // Bun"s vitest shim lacks vi.stubGlobal, so assign the global directly.
  (globalThis as { fetch: unknown }).fetch = async (
    input: RequestInfo | URL,
    init?: RequestInit,
  ) => {
    const url = String(input);
    const method = (init?.method || "GET").toUpperCase();
    const body = init?.body ? JSON.parse(String(init.body)) : undefined;
    calls.push({ url, method, body });
    const match = Object.keys(responses)
      .filter((key) => url.endsWith(key))
      .sort((a, b) => b.length - a.length)[0];
    const payload = match ? responses[match] : {};
    return {
      ok: true,
      status: 200,
      headers: { get: () => "application/json" },
      json: async () => payload,
      text: async () => JSON.stringify(payload),
    };
  };
}

const deepseek = {
  provider_id: "deepseek",
  display_name: "DeepSeek",
  kind: "deepseek",
  base_url: "https://api.deepseek.com",
  currency: "CNY",
  has_api_key: true,
  low_balance_threshold: 10,
  enabled: true,
  allow_hosts: [],
  settings: {},
  script_version: 0,
  supports_balance: true,
  created_at: "",
  updated_at: "2026-01-01T00:00:00Z",
  last_balance: {
    provider: "deepseek",
    currency: "CNY",
    total: 12.5,
    granted: 0,
    topped_up: 12.5,
    available: true,
    low: false,
    source: "deepseek-native",
    fetched_at: "2026-01-01T00:00:00Z",
  },
};

const kinds = [
  {
    kind: "deepseek",
    label: "DeepSeek",
    supports_balance: true,
    requires_script: false,
    requires_base_url: true,
    description: "",
  },
  {
    kind: "js",
    label: "JavaScript adapter",
    supports_balance: true,
    requires_script: true,
    requires_base_url: false,
    description: "",
  },
];

beforeEach(() => {
  calls.length = 0;
  responses = {
    "/api/admin/provider-kinds": { ok: true, kinds },
    "/api/admin/providers": { ok: true, providers: [deepseek], deepseek },
    "/api/admin/providers/deepseek": { ok: true, provider: { ...deepseek, script: "" } },
  };
  installFetch();
});

afterEach(() => {
  cleanup();
});

describe("AdminProviders", () => {
  it("renders the provider list with its balance", async () => {
    render(<AdminProviders setToast={() => {}} />);
    expect(await screen.findByText("DeepSeek")).toBeTruthy();
    expect(screen.getByText(/12.5 CNY/)).toBeTruthy();
  });

  it("falls back to the legacy deepseek envelope from an older server", async () => {
    responses["/api/admin/providers"] = { ok: true, deepseek };
    render(<AdminProviders setToast={() => {}} />);
    expect(await screen.findByText("DeepSeek")).toBeTruthy();
  });

  it("never sends an empty api_key when saving", async () => {
    render(<AdminProviders setToast={() => {}} />);
    fireEvent.click(await screen.findByText("編輯"));
    const save = await screen.findByText("儲存 Provider");
    await waitFor(() => expect(screen.getByDisplayValue("https://api.deepseek.com")).toBeTruthy());
    fireEvent.click(save);
    await waitFor(() => {
      const put = calls.find((call) => call.method === "PUT");
      expect(put).toBeTruthy();
      expect((put?.body as Record<string, unknown>).api_key).toBeNull();
    });
  });

  it("sends a script only for the js kind", async () => {
    render(<AdminProviders setToast={() => {}} />);
    fireEvent.click(await screen.findByText("新增 Provider"));
    fireEvent.change(screen.getByPlaceholderText("openrouter"), {
      target: { value: "custom" },
    });
    const kindSelect = screen.getByDisplayValue("DeepSeek");
    fireEvent.change(kindSelect, { target: { value: "js" } });
    const editor = await screen.findByPlaceholderText(/function fetchBalance/);
    fireEvent.change(editor, {
      target: { value: "function fetchBalance(ctx) { return []; }" },
    });
    fireEvent.click(screen.getByText("驗證腳本"));
    await waitFor(() => expect(calls.some((call) => call.url.endsWith("/validate"))).toBe(true));
  });

  it("asks for confirmation before deleting", async () => {
    const confirm = vi.fn(() => false);
    (window as unknown as { confirm: unknown }).confirm = confirm;
    render(<AdminProviders setToast={() => {}} />);
    fireEvent.click(await screen.findByText("刪除"));
    expect(confirm).toHaveBeenCalled();
    expect(calls.some((call) => call.method === "DELETE")).toBe(false);
  });
});
