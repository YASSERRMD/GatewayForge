import { AppShell } from "@/components/app-shell";
import { UpstreamsManager } from "@/components/management/upstreams-manager";

export default function UpstreamsPage() {
  return (
    <AppShell>
      <UpstreamsManager />
    </AppShell>
  );
}
