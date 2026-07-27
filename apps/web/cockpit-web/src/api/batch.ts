export interface BatchPolicy {
  chunkSize: number;
  concurrency: number;
}

export interface BatchResult<T> {
  succeeded: T[];
  failed: Array<{ item: T; error: unknown }>;
}

export async function executeBatch<T>(
  items: readonly T[],
  policy: BatchPolicy,
  operation: (item: T) => Promise<void>,
  onProgress?: (processed: number, total: number) => void,
): Promise<BatchResult<T>> {
  if (policy.chunkSize <= 0 || policy.concurrency <= 0) {
    throw new Error("Batch policy must be positive");
  }
  const result: BatchResult<T> = { succeeded: [], failed: [] };
  let processed = 0;
  for (let offset = 0; offset < items.length; offset += policy.chunkSize) {
    const chunk = items.slice(offset, offset + policy.chunkSize);
    let cursor = 0;
    const workers = Array.from(
      { length: Math.min(policy.concurrency, chunk.length) },
      async () => {
        while (cursor < chunk.length) {
          const index = cursor;
          cursor += 1;
          const item = chunk[index];
          if (item === undefined) continue;
          try {
            await operation(item);
            result.succeeded.push(item);
          } catch (error) {
            result.failed.push({ item, error });
          } finally {
            processed += 1;
            onProgress?.(processed, items.length);
          }
        }
      },
    );
    await Promise.all(workers);
  }
  return result;
}
