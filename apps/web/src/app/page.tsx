import { AppShell } from "@/components/app-shell";
import { OverviewDashboard } from "@/components/dashboard/overview-dashboard";

export default function HomePage() {
  return (
    <AppShell>
      <OverviewDashboard />
    </AppShell>
  );
}
