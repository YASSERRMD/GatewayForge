import * as React from "react";
import { AlertCircle, Inbox, RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export function Skeleton({ className }: { className?: string }) {
  return (
    <div
      aria-hidden="true"
      className={cn("animate-pulse rounded-md bg-[var(--muted)]", className)}
    />
  );
}

export function EmptyState({
  title,
  description,
  action,
}: {
  title: string;
  description: string;
  action?: React.ReactNode;
}) {
  return (
    <div className="flex flex-col items-center justify-center rounded-lg border border-dashed border-[var(--border-strong)] bg-[var(--card)] px-6 py-10 text-center">
      <Inbox className="h-8 w-8 text-[var(--muted-foreground)]" aria-hidden="true" />
      <h3 className="mt-3 text-sm font-semibold text-[var(--foreground)]">{title}</h3>
      <p className="mt-1 max-w-md text-xs text-[var(--muted-foreground)]">{description}</p>
      {action && <div className="mt-4">{action}</div>}
    </div>
  );
}

export function ErrorBanner({
  title = "Unable to load data",
  message,
  onRetry,
}: {
  title?: string;
  message: string;
  onRetry?: () => void;
}) {
  return (
    <div
      role="alert"
      className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-900"
    >
      <div className="flex items-start gap-2.5">
        <AlertCircle className="mt-0.5 h-4 w-4 shrink-0 text-red-700" aria-hidden="true" />
        <div>
          <p className="font-medium">{title}</p>
          <p className="text-xs text-red-800">{message}</p>
        </div>
      </div>
      {onRetry && (
        <Button type="button" variant="outline" size="sm" onClick={onRetry}>
          <RefreshCw className="mr-1.5 h-3.5 w-3.5" aria-hidden="true" />
          Retry
        </Button>
      )}
    </div>
  );
}
