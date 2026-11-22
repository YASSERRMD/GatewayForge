import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import {
  defaultRateLimitConfig,
  RateLimitPolicyForm,
  validateRateLimitPolicyConfig,
} from "./ratelimit-policy-form";

describe("RateLimitPolicyForm & validation", () => {
  it("enforces burst >= requests_per_window, header_name when key_by=header, and critical+fail_open guardrail", () => {
    const errs = validateRateLimitPolicyConfig("rate_limit", {
      algorithm: "token_bucket",
      key_by: "header",
      header_name: "",
      requests_per_window: 200,
      window_seconds: 0,
      burst: 50,
      security_class: "critical",
      fail_mode: "fail_open",
    });

    expect(errs.burst).toMatch(/greater than or equal/i);
    expect(errs.window_seconds).toMatch(/between 1 and 86,400/i);
    expect(errs.header_name).toMatch(/header_name is required/i);
    expect(errs.fail_mode).toMatch(/critical security_class cannot use fail_open/i);

    const valid = validateRateLimitPolicyConfig("rate_limit", {
      ...defaultRateLimitConfig(),
      security_class: "critical",
      fail_mode: "fail_closed",
    });
    expect(Object.keys(valid)).toHaveLength(0);
  });

  it("renders inline critical+fail_open guardrail banner and updates fields", () => {
    const onChange = vi.fn();
    render(
      <RateLimitPolicyForm
        policyType="rate_limit"
        config={{
          ...defaultRateLimitConfig(),
          security_class: "critical",
          fail_mode: "fail_open",
        }}
        errors={{}}
        onChange={onChange}
      />
    );

    expect(screen.getByRole("alert")).toHaveTextContent(/cannot run in fail_open mode/i);
    fireEvent.change(screen.getByLabelText("Redis Outage Mode"), { target: { value: "fail_closed" } });
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ fail_mode: "fail_closed" }));
  });
});
