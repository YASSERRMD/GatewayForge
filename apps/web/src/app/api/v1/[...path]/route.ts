import type { NextRequest } from "next/server";

export const dynamic = "force-dynamic";

function resolveUpstreamBase(): string {
  return (
    process.env.CONTROL_PLANE_INTERNAL_URL ||
    process.env.NEXT_PUBLIC_CONTROL_PLANE_URL ||
    "http://127.0.0.1:18080"
  ).replace(/\/$/, "");
}

const FORWARDED_REQ_HEADERS = [
  "accept",
  "content-type",
  "cookie",
  "x-csrf-token",
  "if-match",
  "if-none-match",
  "idempotency-key",
  "x-request-id",
  "traceparent",
];

async function proxyToControlPlane(
  req: NextRequest,
  context: { params: Promise<{ path: string[] }> }
): Promise<Response> {
  const { path } = await context.params;
  const search = req.nextUrl.search || "";
  const targetUrl = `${resolveUpstreamBase()}/api/v1/${path.join("/")}${search}`;

  const headers = new Headers();
  for (const name of FORWARDED_REQ_HEADERS) {
    const val = req.headers.get(name);
    if (val) {
      headers.set(name, val);
    }
  }

  const hasBody = req.method !== "GET" && req.method !== "HEAD";
  const body = hasBody ? await req.arrayBuffer() : undefined;

  let upstream: Response;
  try {
    upstream = await fetch(targetUrl, {
      method: req.method,
      headers,
      body: body && body.byteLength > 0 ? body : undefined,
      redirect: "manual",
    });
  } catch {
    return Response.json(
      {
        status: 503,
        code: "control_plane_unreachable",
        title: "Service Unavailable",
        detail: "Control plane API is currently unreachable.",
      },
      { status: 503 }
    );
  }

  const resHeaders = new Headers();
  const contentType = upstream.headers.get("content-type");
  if (contentType) resHeaders.set("content-type", contentType);
  const etag = upstream.headers.get("etag");
  if (etag) resHeaders.set("etag", etag);
  const cacheControl = upstream.headers.get("cache-control");
  if (cacheControl) resHeaders.set("cache-control", cacheControl);
  const reqId = upstream.headers.get("x-request-id");
  if (reqId) resHeaders.set("x-request-id", reqId);

  // Preserve all Set-Cookie headers verbatim (HttpOnly SameSite=Lax session cookies).
  const setCookies =
    typeof upstream.headers.getSetCookie === "function"
      ? upstream.headers.getSetCookie()
      : [];
  for (const c of setCookies) {
    resHeaders.append("set-cookie", c);
  }

  if (upstream.status === 204 || upstream.status === 304) {
    return new Response(null, { status: upstream.status, headers: resHeaders });
  }
  const respBody = await upstream.arrayBuffer();
  return new Response(respBody, { status: upstream.status, headers: resHeaders });
}

export const GET = proxyToControlPlane;
export const POST = proxyToControlPlane;
export const PUT = proxyToControlPlane;
export const PATCH = proxyToControlPlane;
export const DELETE = proxyToControlPlane;
