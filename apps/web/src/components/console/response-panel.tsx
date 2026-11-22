"use client";

import * as React from "react";
import { Download, FileWarning } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { ConsoleExecuteResponse } from "@/components/console/request-console";

function statusVariant(code: number): "success" | "warning" | "destructive" | "default" {
  if (code >= 200 && code < 300) return "success";
  if (code >= 400 && code < 500) return "warning";
  if (code >= 500) return "destructive";
  return "default";
}

function formatSafeBody(resp: ConsoleExecuteResponse): string {
  if (resp.is_binary) return "";
  if (resp.content_type.toLowerCase().includes("json")) {
    try {
      return JSON.stringify(JSON.parse(resp.body), null, 2);
    } catch {
      return resp.body;
    }
  }
  return resp.body;
}

export function ResponsePanel({
  response,
  running,
}: {
  response: ConsoleExecuteResponse | null;
  running: boolean;
}) {
  const [tab, setTab] = React.useState<"body" | "headers">("body");

  const headerEntries = React.useMemo(() => {
    if (!response) return [];
    return Object.entries(response.headers).sort(([a], [b]) => a.localeCompare(b));
  }, [response]);

  const formattedBody = React.useMemo(
    () => (response ? formatSafeBody(response) : ""),
    [response]
  );

  const handleSafeDownload = () => {
    if (!response || response.is_binary || !response.body) return;
    // Always download as text/plain to prevent browser active content execution (HTML/SVG XSS).
    const blob = new Blob([formattedBody], { type: "text/plain;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    const isJson = response.content_type.toLowerCase().includes("json");
    link.href = url;
    link.download = isJson ? "gateway-response.json" : "gateway-response.txt";
    document.body.appendChild(link);
    link.click();
    link.remove();
    URL.revokeObjectURL(url);
  };

  return (
    <Card className="h-full">
      <CardHeader className="flex flex-wrap items-start justify-between gap-2">
        <div>
          <CardTitle>Response</CardTitle>
          <CardDescription>
            {response
              ? `Via ${response.gateway_target} · ${response.body_bytes.toLocaleString()} bytes`
              : "Gateway status, timing, headers, and safely escaped payload."}
          </CardDescription>
        </div>
        {response && (
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant={statusVariant(response.status)}>HTTP {response.status}</Badge>
            <Badge variant="default">{response.duration_ms} ms</Badge>
            {!response.is_binary && response.body.length > 0 && (
              <Button type="button" variant="outline" size="sm" onClick={handleSafeDownload}>
                <Download className="mr-1 h-3.5 w-3.5" aria-hidden="true" />
                Download (.txt)
              </Button>
            )}
          </div>
        )}
      </CardHeader>
      <CardContent className="space-y-3">
        {running ? (
          <p className="text-sm text-[var(--muted-foreground)]">Dispatching request through GatewayForge…</p>
        ) : !response ? (
          <p className="text-sm text-[var(--muted-foreground)]">
            Send a request to inspect status code, latency, response headers, and payload.
          </p>
        ) : (
          <>
            <div className="flex items-center gap-1 border-b border-[var(--border)] pb-2" role="tablist">
              <Button
                type="button"
                role="tab"
                aria-selected={tab === "body"}
                variant={tab === "body" ? "default" : "ghost"}
                size="sm"
                onClick={() => setTab("body")}
              >
                Body
              </Button>
              <Button
                type="button"
                role="tab"
                aria-selected={tab === "headers"}
                variant={tab === "headers" ? "default" : "ghost"}
                size="sm"
                onClick={() => setTab("headers")}
              >
                Headers ({headerEntries.length})
              </Button>
            </div>

            {tab === "body" ? (
              <div className="space-y-2.5">
                {response.body_truncated && (
                  <div
                    role="status"
                    className="flex items-start gap-2 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-900"
                  >
                    <FileWarning className="mt-0.5 h-4 w-4 shrink-0 text-amber-700" aria-hidden="true" />
                    <span>
                      Response body exceeded the 256 KiB console preview limit and was truncated for browser safety.
                    </span>
                  </div>
                )}

                {response.is_binary ? (
                  <div
                    role="note"
                    className="rounded-md border border-[var(--border)] bg-[var(--muted)]/40 p-4 text-xs text-[var(--muted-foreground)]"
                  >
                    Binary payload detected ({response.content_type || "application/octet-stream"},{" "}
                    {response.body_bytes.toLocaleString()} bytes). Inline rendering is disabled for safety.
                  </div>
                ) : (
                  <pre
                    data-testid="console-response-body"
                    className="max-h-96 overflow-auto whitespace-pre-wrap break-all rounded-md border border-[var(--border)] bg-[var(--muted)]/40 p-3 font-mono text-xs text-[var(--foreground)]"
                  >
                    {formattedBody || "(empty response body)"}
                  </pre>
                )}
              </div>
            ) : (
              <div className="max-h-96 overflow-auto">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Header</TableHead>
                      <TableHead>Value</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {headerEntries.map(([name, vals]) => (
                      <TableRow key={name}>
                        <TableCell className="font-mono text-xs font-medium">{name}</TableCell>
                        <TableCell className="font-mono text-xs break-all text-[var(--muted-foreground)]">
                          {vals.join(", ")}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
          </>
        )}
      </CardContent>
    </Card>
  );
}
