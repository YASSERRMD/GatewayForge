import { describe, it, expect, vi, beforeEach } from "vitest";
import { ApiError, apiRequest } from "./client";

describe("apiRequest and typed Problem Details error mapping", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("injects CSRF header and credentials on mutations", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ id: "svc-1" }), {
        status: 201,
        headers: { "Content-Type": "application/json" },
      })
    );

    const out = await apiRequest<{ id: string }>("/api/v1/orgs/o1/services", {
      method: "POST",
      body: { slug: "orders", name: "Orders" },
      csrfToken: "csrf-abc",
    });

    expect(out.id).toBe("svc-1");
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/orgs/o1/services",
      expect.objectContaining({
        method: "POST",
        credentials: "include",
        headers: expect.objectContaining({
          "X-CSRF-Token": "csrf-abc",
          "Content-Type": "application/json",
        }),
      })
    );
  });

  it("maps 401/403/409/422/5xx Problem Details into typed ApiError and triggers onUnauthorized", async () => {
    const onUnauthorized = vi.fn();
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ status: 401, code: "unauthorized", detail: "Expired" }), {
        status: 401,
        headers: { "Content-Type": "application/problem+json" },
      })
    );

    await expect(
      apiRequest("/api/v1/orgs/o1/services", { onUnauthorized })
    ).rejects.toSatisfy((err: unknown) => {
      expect(err).toBeInstanceOf(ApiError);
      const ae = err as ApiError;
      expect(ae.isUnauthorized).toBe(true);
      expect(ae.friendlyMessage()).toMatch(/expired/i);
      return true;
    });
    expect(onUnauthorized).toHaveBeenCalledTimes(1);

    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          status: 409,
          code: "revision_conflict",
          detail: "Modified concurrently.",
          request_id: "req_123",
        }),
        { status: 409, headers: { "Content-Type": "application/problem+json" } }
      )
    );

    await expect(apiRequest("/api/v1/orgs/o1/services/s1", { method: "PATCH" })).rejects.toSatisfy(
      (err: unknown) => {
        const ae = err as ApiError;
        expect(ae.isConflict).toBe(true);
        expect(ae.requestId).toBe("req_123");
        return true;
      }
    );

    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          status: 422,
          code: "validation_error",
          detail: "Bad input",
          errors: [{ field: "slug", message: "must be lowercase" }],
        }),
        { status: 422, headers: { "Content-Type": "application/problem+json" } }
      )
    );

    await expect(apiRequest("/api/v1/orgs/o1/services", { method: "POST" })).rejects.toSatisfy(
      (err: unknown) => {
        const ae = err as ApiError;
        expect(ae.isValidation).toBe(true);
        expect(ae.fieldErrors).toHaveLength(1);
        expect(ae.fieldErrors[0]?.field).toBe("slug");
        return true;
      }
    );
  });
});
