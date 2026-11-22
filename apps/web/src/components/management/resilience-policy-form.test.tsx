import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import {
  defaultRetryConfig,
  ResiliencePolicyForm,
  validateResiliencePolicyConfig,
} from "./resilience-policy-form";

describe("ResiliencePolicyForm & schema validation", () => {
  it("forbids non-idempotent POST/PATCH retries and renders safety warning", () => {
    const errs = validateResiliencePolicyConfig("retry", {
      ...defaultRetryConfig(),
      retryable_methods: ["GET", "POST"],
    });
    expect(errs.retryable_methods).toMatch(/POST, PATCH.*forbidden/i);

    render(
      <ResiliencePolicyForm
        policyType="retry"
        config={defaultRetryConfig()}
        errors={{}}
        onChange={vi.fn()}
      />
    );
    expect(screen.getByRole("note")).toHaveTextContent(/Non-idempotent retry safety/i);
    expect(screen.queryByLabelText("POST")).not.toBeInTheDocument();
  });

  it("validates timeout bounds, circuit breaker bounds, and CORS wildcard+credentials guardrail", () => {
    const toErrs = validateResiliencePolicyConfig("timeout", {
      connect_timeout_ms: 15000,
      total_timeout_ms: 5000,
    });
    expect(toErrs.connect_timeout_ms).toMatch(/cannot exceed total timeout/i);

    const cbErrs = validateResiliencePolicyConfig("circuit_breaker", {
      failure_threshold: 0,
      success_threshold: 2,
      open_duration_ms: 100,
      half_open_max_in_flight: 2,
    });
    expect(cbErrs.failure_threshold).toMatch(/between 1 and 100/i);
    expect(cbErrs.open_duration_ms).toMatch(/between 500 and 300,000/i);

    const corsErrs = validateResiliencePolicyConfig("cors", {
      allowed_origins: ["*"],
      allow_credentials: true,
    });
    expect(corsErrs.allow_credentials).toMatch(/wildcard/i);
  });
});
