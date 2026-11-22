import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { Button } from "./button";
import { Badge } from "./badge";
import { Input, Label, FieldError, Select } from "./input";
import { Dialog } from "./dialog";
import { EmptyState, ErrorBanner, Skeleton } from "./states";

describe("GatewayForge Design System", () => {
  it("renders accessible form controls with labels and error alerts", () => {
    render(
      <div>
        <Label htmlFor="svc-name">Service Name</Label>
        <Input id="svc-name" placeholder="orders-api" />
        <Select aria-label="Environment" defaultValue="prod">
          <option value="prod">Production</option>
        </Select>
        <FieldError message="Name is required" />
      </div>
    );
    expect(screen.getByLabelText("Service Name")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("Name is required");
    expect(screen.getByLabelText("Environment")).toHaveValue("prod");
  });

  it("handles Dialog keyboard Escape dismissal and ARIA modal roles", () => {
    const onClose = vi.fn();
    render(
      <Dialog open={true} onClose={onClose} title="Confirm Action" description="Review carefully.">
        <Button onClick={onClose}>Confirm</Button>
      </Dialog>
    );
    const dlg = screen.getByRole("dialog");
    expect(dlg).toHaveAttribute("aria-modal", "true");
    fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("renders status badges, skeletons, empty states, and retryable error banners", () => {
    const retry = vi.fn();
    render(
      <div>
        <Badge variant="success">Healthy</Badge>
        <Skeleton className="h-4 w-24" />
        <EmptyState title="No routes" description="Create your first route." />
        <ErrorBanner message="Control plane unreachable" onRetry={retry} />
      </div>
    );
    expect(screen.getByText("Healthy")).toBeInTheDocument();
    expect(screen.getByText("No routes")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /retry/i }));
    expect(retry).toHaveBeenCalledTimes(1);
  });
});
