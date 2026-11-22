import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ResponsePanel } from "./response-panel";

describe("ResponsePanel", () => {
  it("escapes HTML/script payloads safely, displays truncation warnings, and renders headers", () => {
    const { container } = render(
      <ResponsePanel
        running={false}
        response={{
          status: 200,
          duration_ms: 42,
          headers: {
            "Content-Type": ["text/html"],
            "X-Request-Id": ["req-abc-123"],
          },
          body: '<script>window.__xss=true</script><h1>Hello</h1>',
          body_truncated: true,
          body_bytes: 262144,
          is_binary: false,
          content_type: "text/html",
          gateway_target: "http://gateway:8081",
        }}
      />
    );

    expect(screen.getByText("HTTP 200")).toBeInTheDocument();
    expect(screen.getByText("42 ms")).toBeInTheDocument();
    expect(screen.getByText(/exceeded the 256 KiB console preview limit/i)).toBeInTheDocument();
    expect(screen.getByTestId("console-response-body")).toHaveTextContent(
      "<script>window.__xss=true</script><h1>Hello</h1>"
    );
    // Crucial XSS check: zero actual <script> DOM nodes injected
    expect(container.querySelectorAll("script")).toHaveLength(0);

    fireEvent.click(screen.getByRole("tab", { name: /headers/i }));
    expect(screen.getByText("X-Request-Id")).toBeInTheDocument();
    expect(screen.getByText("req-abc-123")).toBeInTheDocument();
  });

  it("suppresses inline rendering and download for binary responses", () => {
    render(
      <ResponsePanel
        running={false}
        response={{
          status: 200,
          duration_ms: 9,
          headers: { "Content-Type": ["application/pdf"] },
          body: "",
          body_truncated: false,
          body_bytes: 4096,
          is_binary: true,
          content_type: "application/pdf",
          gateway_target: "http://gateway:8081",
        }}
      />
    );

    expect(screen.getByText(/Binary payload detected/i)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /download/i })).not.toBeInTheDocument();
  });
});
