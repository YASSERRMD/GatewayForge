import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { RecentActivityPanel } from "./recent-activity-panel";

describe("RecentActivityPanel", () => {
  it("renders events, opens redacted detail dialog, and paginates", () => {
    const onPageChange = vi.fn();
    render(
      <RecentActivityPanel
        events={[
          {
            id: "ev-1",
            action: "publication.publish",
            resource_type: "publication",
            resource_id: "pub-1",
            request_id: "req_101",
            outcome: "success",
            details: { version: 2 },
            created_at: "2026-11-22T09:00:00Z",
          },
        ]}
        total={12}
        limit={5}
        offset={0}
        loading={false}
        error={null}
        onPageChange={onPageChange}
      />
    );

    expect(screen.getByText("publication.publish")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /inspect publication\.publish/i }));
    expect(screen.getByRole("dialog")).toHaveTextContent("req_101");

    fireEvent.click(screen.getByRole("button", { name: /close dialog/i }));
    fireEvent.click(screen.getByRole("button", { name: /next/i }));
    expect(onPageChange).toHaveBeenCalledWith(5);
  });

  it("shows error banner when access is denied or API fails", () => {
    render(
      <RecentActivityPanel
        events={[]}
        total={0}
        limit={5}
        offset={0}
        loading={false}
        error="Forbidden (403)"
        onPageChange={vi.fn()}
      />
    );
    expect(screen.getByRole("alert")).toHaveTextContent("Forbidden (403)");
  });
});
