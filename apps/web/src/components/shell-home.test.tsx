import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ShellHome, NAV_ITEMS } from "@/components/shell-home";

describe("ShellHome", () => {
  it("renders the GatewayForge heading and primary navigation", () => {
    render(<ShellHome />);
    expect(screen.getByRole("heading", { name: /GatewayForge Control Plane/i })).toBeInTheDocument();
    expect(screen.getByRole("navigation", { name: /Primary/i })).toBeInTheDocument();
    expect(NAV_ITEMS).toHaveLength(10);
  });
});
