import { AppShell } from "@/components/app-shell";
import { AuditExplorer } from "@/components/insights/audit-explorer";

export default function InsightsPage() {
  return (
    <AppShell>
      <AuditExplorer />
    </AppShell>
  );
}
