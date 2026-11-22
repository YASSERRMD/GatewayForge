import { AppShell } from "@/components/app-shell";
import { DraftPublishPanel } from "@/components/releases/draft-publish-panel";

export default function ReleasesPage() {
  return (
    <AppShell>
      <div className="space-y-6">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight text-[var(--foreground)]">Releases</h1>
          <p className="text-sm text-[var(--muted-foreground)]">
            Validate drafts, inspect semantic configuration diffs, publish immutable snapshots, and manage rollbacks.
          </p>
        </div>
        <DraftPublishPanel />
      </div>
    </AppShell>
  );
}
