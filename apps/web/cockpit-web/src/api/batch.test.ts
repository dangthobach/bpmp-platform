import { describe, expect, it } from "vitest";
import { executeBatch } from "./batch";

describe("executeBatch", () => {
  it("bounds concurrency and reports every item exactly once", async () => {
    const items = Array.from({ length: 41 }, (_, index) => index);
    let active = 0;
    let maximum = 0;
    const result = await executeBatch(
      items,
      { chunkSize: 11, concurrency: 3 },
      async (item) => {
        active += 1;
        maximum = Math.max(maximum, active);
        await Promise.resolve();
        active -= 1;
        if (item % 10 === 0) throw new Error("failed");
      },
    );
    expect(maximum).toBeLessThanOrEqual(3);
    expect(result.succeeded.length + result.failed.length).toBe(items.length);
    expect(new Set([...result.succeeded, ...result.failed.map(({ item }) => item)]).size)
      .toBe(items.length);
  });
});
