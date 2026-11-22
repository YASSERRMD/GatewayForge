import type { ConsoleExecuteRequest } from "@/components/console/request-console";

export const CONSOLE_HISTORY_KEY = "gatewayforge_console_history_v1";
export const MAX_HISTORY_ENTRIES = 20;
const MAX_STORED_BODY_CHARS = 2048;

const SENSITIVE_HEADER_KEYS = new Set([
  "authorization",
  "proxy-authorization",
  "x-api-key",
  "api-key",
  "x-auth-token",
  "cookie",
  "set-cookie",
  "x-csrf-token",
  "x-gatewayforge-sync-token",
]);

const SENSITIVE_QUERY_KEYS = new Set([
  "api_key",
  "apikey",
  "key",
  "token",
  "access_token",
  "secret",
  "password",
  "sig",
  "signature",
]);

const GWF_KEY_RE = /\bgwf_[A-Za-z0-9]{6,}[._][A-Za-z0-9_-]{8,}\b/g;
const BEARER_RE = /\bBearer\s+[A-Za-z0-9._~+/=-]{8,}\b/gi;

export interface SanitizedHistoryEntry {
  id: string;
  savedAt: string;
  method: string;
  host: string;
  path: string;
  headers: Record<string, string>;
  body: string;
  status?: number;
}

export function sanitizePathQuery(rawPath: string): string {
  const qIdx = rawPath.indexOf("?");
  if (qIdx < 0) return rawPath;
  const pathname = rawPath.slice(0, qIdx);
  const query = rawPath.slice(qIdx + 1);
  const params = new URLSearchParams(query);
  const clean = new URLSearchParams();
  params.forEach((val, key) => {
    if (SENSITIVE_QUERY_KEYS.has(key.toLowerCase())) return;
    if (GWF_KEY_RE.test(val)) return;
    clean.append(key, val);
  });
  const qs = clean.toString();
  return qs ? `${pathname}?${qs}` : pathname;
}

export function sanitizeConsoleRequest(
  req: ConsoleExecuteRequest,
  status?: number
): SanitizedHistoryEntry {
  const safeHeaders: Record<string, string> = {};
  for (const [k, v] of Object.entries(req.headers)) {
    const lk = k.trim().toLowerCase();
    if (!lk || SENSITIVE_HEADER_KEYS.has(lk)) continue;
    if (lk.includes("secret") || lk.includes("token") || lk.includes("key") || lk.includes("password")) {
      continue;
    }
    if (GWF_KEY_RE.test(v) || BEARER_RE.test(v)) continue;
    safeHeaders[k.trim()] = v;
  }

  const safeBody = req.body
    .slice(0, MAX_STORED_BODY_CHARS)
    .replace(GWF_KEY_RE, "[REDACTED]")
    .replace(BEARER_RE, "Bearer [REDACTED]");

  return {
    id: `hist-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
    savedAt: new Date().toISOString(),
    method: req.method,
    host: req.host,
    path: sanitizePathQuery(req.path),
    headers: safeHeaders,
    body: safeBody,
    status,
  };
}

export function loadSanitizedHistory(): SanitizedHistoryEntry[] {
  try {
    const raw = window.localStorage.getItem(CONSOLE_HISTORY_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as unknown;
    if (!Array.isArray(parsed)) return [];
    return parsed.slice(0, MAX_HISTORY_ENTRIES) as SanitizedHistoryEntry[];
  } catch {
    return [];
  }
}

export function appendSanitizedHistory(
  req: ConsoleExecuteRequest,
  status?: number
): SanitizedHistoryEntry[] {
  const entry = sanitizeConsoleRequest(req, status);
  const prev = loadSanitizedHistory();
  const next = [entry, ...prev].slice(0, MAX_HISTORY_ENTRIES);
  try {
    window.localStorage.setItem(CONSOLE_HISTORY_KEY, JSON.stringify(next));
  } catch {
    // ignore quota errors
  }
  return next;
}

export function clearSanitizedHistory(): void {
  try {
    window.localStorage.removeItem(CONSOLE_HISTORY_KEY);
  } catch {
    // ignore
  }
}
