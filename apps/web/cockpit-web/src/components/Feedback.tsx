import { AlertCircle, Inbox } from "lucide-react";
import { Button } from "./Button";

export function LoadingRows() {
  return (
    <div className="loading-rows" aria-label="Loading">
      {Array.from({ length: 7 }, (_, index) => (
        <span key={index} />
      ))}
    </div>
  );
}

export function EmptyState({
  title,
  detail,
}: {
  title: string;
  detail: string;
}) {
  return (
    <div className="empty-state">
      <Inbox size={28} aria-hidden="true" />
      <strong>{title}</strong>
      <span>{detail}</span>
    </div>
  );
}

export function ErrorState({
  message,
  retry,
}: {
  message: string;
  retry: () => void;
}) {
  return (
    <div className="error-state" role="alert">
      <AlertCircle size={20} aria-hidden="true" />
      <span>{message}</span>
      <Button onClick={retry}>Retry</Button>
    </div>
  );
}
