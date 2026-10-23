import { afterEach, describe, expect, it, vi } from "vitest";

import { api, setCsrf } from "./api";

afterEach(() => {
  setCsrf("");
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("CSRF recovery", () => {
  it("does not rotate an already available token", async () => {
    setCsrf("stored-token");
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    const { restoreCsrf } = await import("./api");
    await restoreCsrf();

    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("refreshes the token and retries a rejected write once", async () => {
    setCsrf("stale-token");
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ detail: "invalid CSRF token" }), {
          status: 403,
          headers: { "Content-Type": "application/json" },
        }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ csrf: "fresh-token" }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ ok: true }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
    vi.stubGlobal("fetch", fetchMock);

    await expect(
      api<{ ok: boolean }>("/api/me/config", { method: "PATCH", body: "{}" }),
    ).resolves.toEqual({ ok: true });

    expect(fetchMock).toHaveBeenCalledTimes(3);
    const firstHeaders = fetchMock.mock.calls[0][1].headers as Headers;
    const retriedHeaders = fetchMock.mock.calls[2][1].headers as Headers;
    expect(firstHeaders.get("X-CSRF-Token")).toBe("stale-token");
    expect(retriedHeaders.get("X-CSRF-Token")).toBe("fresh-token");
  });
});
