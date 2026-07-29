import { describe, expect, it } from "vitest";
import { readServerSentEvents } from "./sse";

describe("readServerSentEvents", () => {
  it("parses chunked events, multiline data and comments", async () => {
    const encoder = new TextEncoder();
    const chunks = [
      "id: cursor-1\r\nevent: work-item.changed\r\ndata: {\"part\":",
      "1}\r\n\r\n: heartbeat\n\nevent: audit.changed\ndata: first\ndata: second\n\n",
    ];
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        for (const chunk of chunks) controller.enqueue(encoder.encode(chunk));
        controller.close();
      },
    });
    const values: Array<{ id: string; event: string; data: string }> = [];
    await readServerSentEvents(
      stream,
      (event) => values.push(event),
      new AbortController().signal,
    );
    expect(values).toEqual([
      {
        id: "cursor-1",
        event: "work-item.changed",
        data: "{\"part\":1}",
      },
      { id: "", event: "audit.changed", data: "first\nsecond" },
    ]);
  });
});
