import { useCallback, useEffect, useState, type ReactNode } from "react";
import type { RemoteRouteModule, RemoteRouteProps } from "@ods/mfe-contract";
import { Box, Button, Chip, ErrorPanel, Page, Paper, Stack, StatusPanel, TextField, Typography } from "@ods/shared-ui";

const MAX_UPLOAD_BYTES = 50 * 1024 * 1024;

interface DocumentRecord {
  id: string;
  owner_ref_type: string;
  owner_ref_id: string;
  file_name: string;
  mime_type: string;
  size_bytes: number;
  status: string;
  legal_hold: boolean;
  ocr_status: string;
  version: number;
}

interface ListResponse {
  documents: DocumentRecord[];
  as_of: string;
}

interface UploadSessionResponse {
  upload_session: { id: string; signed_upload_url: string; status: string; version: number; expires_at: string };
}

function DocumentWorkspace({ platform }: RemoteRouteProps): ReactNode {
  const [ownerRefId, setOwnerRefId] = useState("case-1001");
  const [fileName, setFileName] = useState("kyc-pack.pdf");
  const [sizeBytes, setSizeBytes] = useState("1048576");
  const [mimeType, setMimeType] = useState("application/pdf");
  const [documents, setDocuments] = useState<DocumentRecord[]>([]);
  const [asOf, setAsOf] = useState("");
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const loadDocuments = useCallback(async () => {
    const res = await platform.callApi<ListResponse>({ method: "GET", path: "/v1/documents?limit=25" });
    if (!res.ok) {
      setError(res.problem.detail || res.problem.title);
      return;
    }
    setDocuments(res.data.documents ?? []);
    setAsOf(res.data.as_of ?? "");
    setError("");
  }, [platform]);

  useEffect(() => {
    void loadDocuments();
  }, [loadDocuments]);

  const handleCreateUpload = async () => {
    const parsedSize = Number(sizeBytes);
    if (!Number.isFinite(parsedSize) || parsedSize <= 0 || parsedSize > MAX_UPLOAD_BYTES) {
      setError("File size must be between 1 byte and 50 MiB (52,428,800 bytes).");
      return;
    }
    setBusy(true);
    setError("");
    const res = await platform.callApi<UploadSessionResponse>({
      method: "POST",
      path: "/v1/documents/uploads",
      idempotencyKey: `doc-upload-${ownerRefId}-${fileName}-${parsedSize}`,
      body: {
        owner_ref_type: "CASE",
        owner_ref_id: ownerRefId,
        file_name: fileName,
        declared_mime: mimeType,
        declared_size_bytes: parsedSize,
      },
    });
    setBusy(false);
    if (!res.ok) {
      setError(res.problem.detail || res.problem.title);
      return;
    }
    setNotice(`Upload session ${res.data.upload_session.id} created (${res.data.upload_session.status}).`);
    await loadDocuments();
  };

  const handleDownload = async (doc: DocumentRecord) => {
    const res = await platform.callApi<{ url: string; expires_at: string }>({
      method: "POST",
      path: `/v1/documents/${encodeURIComponent(doc.id)}/download-url`,
      idempotencyKey: `dl-${doc.id}-${doc.version}`,
    });
    if (!res.ok) {
      setError(res.problem.detail || res.problem.title);
      return;
    }
    setNotice(`Short-lived signed download URL issued (expires ${res.data.expires_at}).`);
  };

  const handleTriggerOCR = async (doc: DocumentRecord) => {
    const res = await platform.callApi<DocumentRecord>({
      method: "POST",
      path: `/v1/documents/${encodeURIComponent(doc.id)}/ocr`,
      idempotencyKey: `ocr-${doc.id}-${doc.version}`,
    });
    if (!res.ok) {
      setError(res.problem.detail || res.problem.title);
      return;
    }
    setNotice(`OCR queued for ${doc.file_name}.`);
    await loadDocuments();
  };

  return (
    <Page title="Document Management & Quarantine">
      <Stack spacing={2.5}>
        {error ? <ErrorPanel title="Document operation failed" detail={error} onRetry={() => void loadDocuments()} /> : null}
        {notice ? <StatusPanel title="Document status" detail={notice} /> : null}

        <Paper variant="outlined" sx={{ p: 2.5 }}>
          <Typography variant="h6" sx={{ mb: 1.5 }}>Create Streaming Upload Session (Max 50 MiB)</Typography>
          <Stack direction={{ xs: "column", md: "row" }} spacing={1.5}>
            <TextField label="Case / Owner Ref" value={ownerRefId} onChange={(e) => setOwnerRefId(e.target.value)} size="small" />
            <TextField label="File Name" value={fileName} onChange={(e) => setFileName(e.target.value)} size="small" />
            <TextField label="MIME Type" value={mimeType} onChange={(e) => setMimeType(e.target.value)} size="small" />
            <TextField label="Declared Size (bytes)" value={sizeBytes} onChange={(e) => setSizeBytes(e.target.value)} size="small" />
            <Button variant="contained" disabled={busy} onClick={() => void handleCreateUpload()}>Create Session</Button>
          </Stack>
        </Paper>

        <Paper variant="outlined" sx={{ p: 2.5 }}>
          <Stack direction="row" justifyContent="space-between" alignItems="center" sx={{ mb: 1.5 }}>
            <Typography variant="h6">Documents ({documents.length})</Typography>
            {asOf ? <Typography variant="caption" color="text.secondary">As of {asOf}</Typography> : null}
          </Stack>
          <Stack spacing={1.25}>
            {documents.length === 0 ? (
              <Typography color="text.secondary">No documents found for this tenant.</Typography>
            ) : documents.map((doc) => (
              <Box key={doc.id} sx={{ p: 1.5, border: "1px solid #e2e7e3", borderRadius: 1.5, display: "flex", justifyContent: "space-between", alignItems: "center", gap: 2 }}>
                <Box>
                  <Typography fontWeight={700}>{doc.file_name}</Typography>
                  <Typography variant="caption" color="text.secondary">
                    {doc.owner_ref_type}:{doc.owner_ref_id} · {doc.mime_type} · {doc.size_bytes} bytes · OCR: {doc.ocr_status}
                  </Typography>
                </Box>
                <Stack direction="row" spacing={1} alignItems="center">
                  <Chip size="small" label={doc.status} color={doc.status === "CLEAN" ? "success" : doc.status === "REJECTED" ? "error" : "warning"} />
                  {doc.legal_hold ? <Chip size="small" label="Legal Hold" color="warning" variant="outlined" /> : null}
                  <Button size="small" variant="outlined" disabled={doc.status !== "CLEAN"} onClick={() => void handleDownload(doc)}>Download</Button>
                  <Button size="small" variant="outlined" disabled={doc.status !== "CLEAN"} onClick={() => void handleTriggerOCR(doc)}>Trigger OCR</Button>
                </Stack>
              </Box>
            ))}
          </Stack>
        </Paper>
      </Stack>
    </Page>
  );
}

export const contractVersion = "1.0";
export const route = { path: "/documents", title: "Documents", Component: DocumentWorkspace };
const moduleContract: RemoteRouteModule = { contractVersion, route };
export default moduleContract;
