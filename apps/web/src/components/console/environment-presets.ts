export interface ConsoleEnvironmentPreset {
  id: string;
  label: string;
  description: string;
  defaultHost: string;
  variables: Record<string, string>;
}

/**
 * Local and mock environment presets for developer console testing.
 * Production credentials and secrets are intentionally excluded.
 */
export const CONSOLE_ENVIRONMENT_PRESETS: readonly ConsoleEnvironmentPreset[] = [
  {
    id: "local-dev",
    label: "Local Dev (Compose)",
    description: "Default local GatewayForge route host targeting Compose gateway",
    defaultHost: "api.gatewayforge.local",
    variables: {
      env: "dev",
      api_version: "v1",
    },
  },
  {
    id: "mock-sandbox",
    label: "Mock Sandbox",
    description: "Deterministic mock-upstream host for contract & fault-injection checks",
    defaultHost: "mock.gatewayforge.local",
    variables: {
      env: "mock",
      api_version: "v1",
    },
  },
  {
    id: "staging-sim",
    label: "Staging Simulation",
    description: "Pre-release route host simulation without production secrets",
    defaultHost: "staging.gatewayforge.local",
    variables: {
      env: "staging",
      api_version: "v1",
    },
  },
] as const;

export function interpolatePresetVariables(input: string, vars: Record<string, string>): string {
  return input.replace(/\{\{\s*([a-zA-Z0-9_]+)\s*\}\}/g, (match, key: string) => {
    const val = vars[key];
    return val !== undefined ? val : match;
  });
}
