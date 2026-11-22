"use client";

import * as React from "react";
import { History, Trash2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Select } from "@/components/ui/input";
import {
  appendSanitizedHistory,
  clearSanitizedHistory,
  loadSanitizedHistory,
  type SanitizedHistoryEntry,
} from "@/components/console/console-history";
import {
  CONSOLE_ENVIRONMENT_PRESETS,
  interpolatePresetVariables,
} from "@/components/console/environment-presets";
import {
  RequestConsole,
  type ConsoleExecuteRequest,
  type ConsoleExecuteResponse,
} from "@/components/console/request-console";

export interface ConsoleWorkspaceProps {
  headerSlot?: React.ReactNode;
  presetHost?: string;
}

export function ConsoleWorkspace({ headerSlot, presetHost }: ConsoleWorkspaceProps) {
  const [selectedPresetId, setSelectedPresetId] = React.useState(
    CONSOLE_ENVIRONMENT_PRESETS[0]?.id ?? "local-dev"
  );
  const activePreset = React.useMemo(
    () =>
      CONSOLE_ENVIRONMENT_PRESETS.find((p) => p.id === selectedPresetId) ??
      CONSOLE_ENVIRONMENT_PRESETS[0],
    [selectedPresetId]
  );

  const [optInHistory, setOptInHistory] = React.useState(false);
  const [history, setHistory] = React.useState<SanitizedHistoryEntry[]>([]);
  const [draft, setDraft] = React.useState<(Partial<ConsoleExecuteRequest> & { nonce?: number }) | undefined>(
    undefined
  );

  React.useEffect(() => {
    setHistory(loadSanitizedHistory());
  }, []);

  const handleExecuted = React.useCallback(
    (req: ConsoleExecuteRequest, resp: ConsoleExecuteResponse) => {
      if (!optInHistory) return;
      const resolvedPath = activePreset
        ? interpolatePresetVariables(req.path, activePreset.variables)
        : req.path;
      setHistory(appendSanitizedHistory({ ...req, path: resolvedPath }, resp.status));
    },
    [optInHistory, activePreset]
  );

  const envSwitcher = (
    <div className="flex flex-wrap items-center gap-2">
      <label htmlFor="console-env-preset" className="text-xs font-medium text-[var(--muted-foreground)]">
        Environment preset
      </label>
      <Select
        id="console-env-preset"
        aria-label="Environment preset"
        value={selectedPresetId}
        onChange={(e) => setSelectedPresetId(e.target.value)}
        className="w-auto min-w-[200px]"
      >
        {CONSOLE_ENVIRONMENT_PRESETS.map((preset) => (
          <option key={preset.id} value={preset.id}>
            {preset.label} ({preset.defaultHost})
          </option>
        ))}
      </Select>
      {headerSlot}
    </div>
  );

  const handleClear = () => {
    clearSanitizedHistory();
    setHistory([]);
  };

  const sidebar = (
    <Card className="h-full">
      <CardHeader>
        <div className="flex items-center justify-between gap-2">
          <CardTitle className="flex items-center gap-1.5 text-sm">
            <History className="h-4 w-4" aria-hidden="true" />
            Sanitized History
          </CardTitle>
          {history.length > 0 && (
            <Button type="button" variant="ghost" size="sm" aria-label="Clear history" onClick={handleClear}>
              <Trash2 className="h-3.5 w-3.5" aria-hidden="true" />
            </Button>
          )}
        </div>
        <CardDescription>
          Opt-in local presets (max 20). Authorization, API keys, cookies, and secret tokens are stripped before saving.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <label className="flex items-center gap-2 text-xs text-[var(--foreground)]">
          <input
            type="checkbox"
            checked={optInHistory}
            onChange={(e) => setOptInHistory(e.target.checked)}
            className="h-4 w-4 rounded border-[var(--border-strong)]"
          />
          Save non-secret requests locally
        </label>

        {history.length === 0 ? (
          <p className="text-xs text-[var(--muted-foreground)]">
            {optInHistory
              ? "Executed requests will appear here with credentials stripped."
              : "History saving is disabled by default."}
          </p>
        ) : (
          <ul className="space-y-2">
            {history.map((item) => (
              <li key={item.id}>
                <button
                  type="button"
                  onClick={() =>
                    setDraft({
                      method: item.method,
                      host: item.host,
                      path: item.path,
                      headers: item.headers,
                      body: item.body,
                      nonce: Date.now(),
                    })
                  }
                  className="w-full rounded-md border border-[var(--border)] p-2 text-left transition-colors hover:bg-[var(--muted)]"
                >
                  <div className="flex items-center justify-between gap-1">
                    <Badge variant="default">{item.method}</Badge>
                    {item.status && (
                      <span className="font-mono text-[11px] text-[var(--muted-foreground)]">
                        HTTP {item.status}
                      </span>
                    )}
                  </div>
                  <p className="mt-1 truncate font-mono text-xs text-[var(--foreground)]">
                    {item.host}
                    {item.path}
                  </p>
                </button>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );

  return (
    <RequestConsole
      headerSlot={envSwitcher}
      sidebarSlot={sidebar}
      presetHost={presetHost ?? activePreset?.defaultHost}
      externalDraft={draft}
      onExecuted={handleExecuted}
    />
  );
}
