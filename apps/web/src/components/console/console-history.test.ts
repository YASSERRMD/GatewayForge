import { afterEach, describe, expect, it } from "vitest";
import {
  appendSanitizedHistory,
  CONSOLE_HISTORY_KEY,
  loadSanitizedHistory,
  MAX_HISTORY_ENTRIES,
  sanitizeConsoleRequest,
} from "./console-history";

describe("Sanitized console request history", () => {
  afterEach(() => {
    window.localStorage.clear();
  });

  it("strips Authorization, X-Api-Key, Cookie, secret query params, and gwf_* tokens before storage", () => {
    const secretKey = "gwf_deadbeef1234.0123456789abcdef0123456789abcdef";
    const jwt = "Bearer eyJhbGciOiJSUzI1NiJ9.payload.sig";

    appendSanitizedHistory(
      {
        method: "POST",
        host: "api.local",
        path: "/v1/orders?page=2&token=supersecret&api_key=leakme",
        headers: {
          Accept: "application/json",
          Authorization: jwt,
          "X-Api-Key": secretKey,
          Cookie: "gwf_session=secretcookie",
          "X-Custom-Safe": "trace-ok",
        },
        body: `{"note":"do not store ${secretKey}"}`,
      },
      200
    );

    const rawStorage = window.localStorage.getItem(CONSOLE_HISTORY_KEY) ?? "";
    expect(rawStorage).not.toContain(secretKey);
    expect(rawStorage).not.toContain("eyJhbGciOiJSUzI1NiJ9");
    expect(rawStorage).not.toContain("secretcookie");
    expect(rawStorage).not.toContain("supersecret");
    expect(rawStorage).not.toContain("leakme");

    const items = loadSanitizedHistory();
    expect(items).toHaveLength(1);
    expect(items[0]?.path).toBe("/v1/orders?page=2");
    expect(items[0]?.headers).toEqual({
      Accept: "application/json",
      "X-Custom-Safe": "trace-ok",
    });
  });

  it("enforces bounded persistence of at most 20 entries", () => {
    for (let i = 0; i < 25; i++) {
      appendSanitizedHistory(
        {
          method: "GET",
          host: "api.local",
          path: `/v1/item/${i}`,
          headers: {},
          body: "",
        },
        200
      );
    }
    const items = loadSanitizedHistory();
    expect(items).toHaveLength(MAX_HISTORY_ENTRIES);
    expect(items[0]?.path).toBe("/v1/item/24");

    const single = sanitizeConsoleRequest({
      method: "GET",
      host: "api.local",
      path: "/v1/ok",
      headers: { "X-Secret-Token": "abc" },
      body: "",
    });
    expect(single.headers).toEqual({});
  });
});
