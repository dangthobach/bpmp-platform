import { describe, expect, it } from "vitest";
import { withTimeout } from "./runtime";

describe("withTimeout", () => {
  it("rejects a remote that does not settle before the boundary deadline", async () => {
    await expect(withTimeout(new Promise(() => undefined), 5, "remote timeout")).rejects.toThrow("remote timeout");
  });
});
