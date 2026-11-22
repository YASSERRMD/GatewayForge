import { AppShell } from "@/components/app-shell";
import { ServicesManager } from "@/components/management/services-manager";

export default function ServicesPage() {
  return (
    <AppShell>
      <ServicesManager />
    </AppShell>
  );
}
