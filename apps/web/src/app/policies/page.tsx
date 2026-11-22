import { AppShell } from "@/components/app-shell";
import { PoliciesManager } from "@/components/management/policies-manager";

export default function PoliciesPage() {
  return (
    <AppShell>
      <PoliciesManager />
    </AppShell>
  );
}
