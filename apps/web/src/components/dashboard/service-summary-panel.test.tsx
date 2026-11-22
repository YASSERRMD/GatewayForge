import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { ServiceSummaryPanel } from "./service-summary-panel";

describe("ServiceSummaryPanel", () => {
  it("renders authentic services, attached route counts, and top configured routes", () => {
    render(
      <ServiceSummaryPanel
        loading={false}
        error={null}
        services={[
          {
            id: "svc-1",
            org_id: "org-1",
            slug: "orders-api",
            name: "Orders API",
            description: "Order management",
            enabled: true,
            revision: 3,
            created_at: "2026-11-22T08:00:00Z",
            updated_at: "2026-11-22T08:30:00Z",
          },
        ]}
        routes={[
          {
            id: "rt-1",
            org_id: "org-1",
            service_id: "svc-1",
            upstream_pool_id: "pool-1",
            name: "orders-read",
            host: "api.acme.local",
            path_prefix: "/v1/orders",
            methods: ["GET"],
            strip_prefix: false,
            priority: 100,
            enabled: true,
            revision: 1,
          },
        ]}
      />
    );
    expect(screen.getByText("Orders API")).toBeInTheDocument();
    expect(screen.getByText("orders-api")).toBeInTheDocument();
    expect(screen.getByText("api.acme.local/v1/orders")).toBeInTheDocument();
    expect(screen.getByText("rev-3")).toBeInTheDocument();
  });

  it("renders empty state when no services exist", () => {
    render(<ServiceSummaryPanel loading={false} error={null} services={[]} routes={[]} />);
    expect(screen.getByText(/no services configured/i)).toBeInTheDocument();
  });
});
