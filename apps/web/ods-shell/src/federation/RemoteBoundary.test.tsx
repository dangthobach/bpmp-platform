// @vitest-environment jsdom
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { RemoteBoundary } from "./RemoteBoundary";

describe("RemoteBoundary", () => {
  it("contains a remote render exception and lets the user retry without blocking Shell siblings", () => {
    let shouldThrow = true;
    const reportError = vi.fn();
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    function UnstableRemote() {
      if (shouldThrow) throw new Error("remote render failure");
      return <p>Recovered remote</p>;
    }

    try {
      render(<>
        <p>Shell navigation remains available</p>
        <RemoteBoundary remoteName="task-mfe" onError={reportError}>
          <UnstableRemote />
        </RemoteBoundary>
      </>);

      expect(screen.getByText("Shell navigation remains available")).toBeTruthy();
      expect(screen.getByText("task-mfe is unavailable")).toBeTruthy();
      expect(reportError).toHaveBeenCalledOnce();

      shouldThrow = false;
      fireEvent.click(screen.getByRole("button", { name: /retry/i }));
      expect(screen.getByText("Recovered remote")).toBeTruthy();
      expect(screen.getByText("Shell navigation remains available")).toBeTruthy();
    } finally {
      consoleError.mockRestore();
    }
  });
});
