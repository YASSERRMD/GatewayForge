import { AppShell } from "@/components/app-shell";
import { RequestConsole } from "@/components/console/request-console";

export default function ConsolePage() {
  return (
    <AppShell>
      <RequestConsole />
    </AppShell>
  );
}
