import type { FieldErrorItem, ProblemDetails } from "./types";

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly title: string;
  readonly detail: string;
  readonly requestId?: string;
  readonly fieldErrors: FieldErrorItem[];

  constructor(problem: ProblemDetails) {
    const message = problem.detail || problem.title || `Request failed with status ${problem.status}`;
    super(message);
    this.name = "ApiError";
    this.status = problem.status;
    this.code = problem.code || "request_failed";
    this.title = problem.title || "Request Error";
    this.detail = message;
    this.requestId = problem.request_id;
    this.fieldErrors = problem.errors ?? [];
  }

  get isUnauthorized(): boolean {
    return this.status === 401;
  }

  get isForbidden(): boolean {
    return this.status === 403;
  }

  get isConflict(): boolean {
    return this.status === 409;
  }

  get isValidation(): boolean {
    return this.status === 422 || this.status === 400;
  }

  get isServerError(): boolean {
    return this.status >= 500;
  }

  friendlyMessage(): string {
    if (this.isUnauthorized) {
      return "Your session has expired. Please sign in again.";
    }
    if (this.isForbidden) {
      return this.detail || "You do not have permission to perform this action in this organization.";
    }
    if (this.isConflict) {
      return this.detail || "This resource was modified concurrently. Refresh to view the latest revision.";
    }
    if (this.isServerError) {
      return "The control plane encountered an internal error. Please retry shortly.";
    }
    return this.detail;
  }
}

export interface ApiRequestOptions {
  method?: "GET" | "POST" | "PATCH" | "PUT" | "DELETE";
  body?: unknown;
  csrfToken?: string;
  ifMatch?: string;
  signal?: AbortSignal;
  onUnauthorized?: () => void;
}

const MUTATION_METHODS = new Set(["POST", "PATCH", "PUT", "DELETE"]);

export async function apiRequest<T>(path: string, opts: ApiRequestOptions = {}): Promise<T> {
  const method = opts.method ?? "GET";
  const headers: Record<string, string> = {
    Accept: "application/json",
  };
  if (opts.body !== undefined) {
    headers["Content-Type"] = "application/json";
  }
  if (MUTATION_METHODS.has(method) && opts.csrfToken) {
    headers["X-CSRF-Token"] = opts.csrfToken;
  }
  if (opts.ifMatch) {
    headers["If-Match"] = opts.ifMatch;
  }

  let res: Response;
  try {
    res = await fetch(path, {
      method,
      credentials: "include",
      headers,
      body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
      signal: opts.signal,
    });
  } catch (err) {
    if (err instanceof DOMException && err.name === "AbortError") {
      throw err;
    }
    throw new ApiError({
      status: 503,
      code: "network_error",
      title: "Network Error",
      detail: "Unable to reach the GatewayForge control plane.",
    });
  }

  if (res.status === 204) {
    return undefined as T;
  }

  const contentType = res.headers.get("content-type") ?? "";
  const isJson = contentType.includes("json");
  const payload = isJson ? ((await res.json().catch(() => null)) as unknown) : null;

  if (!res.ok) {
    const prob = (payload && typeof payload === "object" ? payload : {}) as Partial<ProblemDetails>;
    const apiErr = new ApiError({
      status: res.status,
      code: prob.code ?? `http_${res.status}`,
      title: prob.title ?? res.statusText,
      detail: prob.detail ?? `Request failed (${res.status})`,
      request_id: prob.request_id,
      errors: Array.isArray(prob.errors) ? prob.errors : [],
    });
    if (apiErr.isUnauthorized && opts.onUnauthorized) {
      opts.onUnauthorized();
    }
    throw apiErr;
  }

  return payload as T;
}
