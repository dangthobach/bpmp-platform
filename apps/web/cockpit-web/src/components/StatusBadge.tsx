export function StatusBadge({ status }: { status: string }) {
  const normalized = status.toLowerCase().replaceAll("_", "-");
  return (
    <span className={`status status--${normalized}`}>
      <span className="status__dot" aria-hidden="true" />
      {status.replaceAll("_", " ")}
    </span>
  );
}
