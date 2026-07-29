export interface ServerSentEvent {
  id: string;
  event: string;
  data: string;
}

export async function readServerSentEvents(
  stream: ReadableStream<Uint8Array>,
  onEvent: (event: ServerSentEvent) => void,
  signal: AbortSignal,
): Promise<void> {
  const reader = stream.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  try {
    while (!signal.aborted) {
      const result = await reader.read();
      buffer += decoder.decode(result.value, { stream: !result.done });
      buffer = buffer.replaceAll("\r\n", "\n");
      let boundary = buffer.indexOf("\n\n");
      while (boundary >= 0) {
        const block = buffer.slice(0, boundary);
        buffer = buffer.slice(boundary + 2);
        const event = parseBlock(block);
        if (event) onEvent(event);
        boundary = buffer.indexOf("\n\n");
      }
      if (result.done) return;
    }
  } finally {
    reader.releaseLock();
  }
}

function parseBlock(block: string): ServerSentEvent | null {
  let id = "";
  let event = "message";
  const data: string[] = [];
  for (const line of block.split("\n")) {
    if (!line || line.startsWith(":")) continue;
    const separator = line.indexOf(":");
    const field = separator < 0 ? line : line.slice(0, separator);
    const raw = separator < 0 ? "" : line.slice(separator + 1);
    const value = raw.startsWith(" ") ? raw.slice(1) : raw;
    if (field === "id" && !value.includes("\0")) id = value;
    if (field === "event") event = value;
    if (field === "data") data.push(value);
  }
  return data.length > 0 ? { id, event, data: data.join("\n") } : null;
}
