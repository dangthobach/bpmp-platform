import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import useSWR from "swr";
import type { PlatformServices } from "@ods/mfe-contract";
import {
  Box,
  Button,
  Chip,
  Divider,
  ErrorPanel,
  FormControl,
  InputLabel,
  LoadingPanel,
  MenuItem,
  Paper,
  Select,
  Stack,
  StatusPanel,
  TextField,
  Typography,
} from "@ods/shared-ui";
import {
  computeSideBySideDiff,
  computeSmsMetrics,
  createCommandPair,
  defaultReleasesSeed,
  defaultTemplatesSeed,
  defaultTemplateVersionsSeed,
  formatVersionSnapshotForDiff,
  renderFallbackTemplate,
  schemaJsonToVariables,
  variablesToSchemaJson,
  type BatchTemplateActionResponse,
  type NotificationChannel,
  type PreviewMode,
  type RenderPreviewResponse,
  type TemplateLifecycleStatus,
  type TemplateRelease,
  type TemplateSummary,
  type TemplateSyntax,
  type TemplateVariableField,
  type TemplateVariant,
  type TemplateVersion,
  type VariableFieldType,
} from "../types";
import {
  applyDiagnosticQuickFix,
  diagnoseTemplateSources,
  type TemplateDiagnostic,
  type TemplateSourceTarget,
} from "../utils/handlebarsDiagnostics";

interface TemplatesListResponse {
  templates?: TemplateSummary[];
}

interface TemplateTagsResponse {
  tags?: string[];
  rows?: Array<{ tag?: string }>;
}

interface TemplateDetailResponse {
  versions?: TemplateVersion[];
  releases?: TemplateRelease[];
}

interface ServerValidateResponse {
  valid?: boolean;
  diagnostics?: Array<{
    code: string;
    severity: "error" | "warning";
    target: TemplateSourceTarget;
    line: number;
    column: number;
    message: string;
    quick_fix?: {
      label: string;
      replace_start: number;
      replace_end: number;
      replacement: string;
    };
  }>;
}

interface Props {
  platform: PlatformServices;
  replayDeliveryId?: string | undefined;
  onClearReplay?: (() => void) | undefined;
  onNavigateToSend?: ((templateKey: string, schemaMajor: number, channel: NotificationChannel) => void) | undefined;
}

const ALL_CHANNELS: NotificationChannel[] = ["EMAIL", "SMS", "PUSH", "IN_APP", "ZNS", "WEBHOOK"];

export function TemplatesWorkspace({
  platform,
  replayDeliveryId,
  onClearReplay,
  onNavigateToSend,
}: Props): ReactNode {
  const [filterChannel, setFilterChannel] = useState<string>("ALL");
  const [filterStatus, setFilterStatus] = useState<string>("ALL");
  const [filterTag, setFilterTag] = useState<string>("ALL");
  const [searchQuery, setSearchQuery] = useState<string>("");
  const [showDeleted, setShowDeleted] = useState<boolean>(false);

  const listQueryPath = useMemo(() => {
    const params = new URLSearchParams({ page_size: "50" });
    if (filterChannel !== "ALL") params.set("channel", filterChannel);
    if (filterStatus !== "ALL") params.set("status", filterStatus);
    if (filterTag !== "ALL") params.set("tag", filterTag);
    if (searchQuery.trim()) params.set("q", searchQuery.trim());
    return `/v1/notifications/templates?${params.toString()}`;
  }, [filterChannel, filterStatus, filterTag, searchQuery]);

  const templatesQuery = useSWR<TemplatesListResponse>(
    `notification-mfe:templates:${listQueryPath}`,
    async () => {
      const res = await platform.callApi<TemplatesListResponse>({ method: "GET", path: listQueryPath });
      if (!res.ok) throw new Error(res.problem.title || "Failed to load templates");
      return res.data;
    },
    { shouldRetryOnError: false },
  );

  const tagsQuery = useSWR<TemplateTagsResponse>(
    "notification-mfe:template-tags",
    async () => {
      const res = await platform.callApi<TemplateTagsResponse>({
        method: "GET",
        path: "/v1/notifications/templates/tags",
      });
      if (!res.ok) return {};
      return res.data;
    },
    { shouldRetryOnError: false },
  );

  const [localTemplates, setLocalTemplates] = useState<TemplateSummary[]>([]);
  const [templatePatches, setTemplatePatches] = useState<Record<string, Partial<TemplateSummary>>>({});

  const baseTemplates = useMemo(() => {
    const apiItems = templatesQuery.data?.templates;
    return apiItems && apiItems.length > 0 ? apiItems : defaultTemplatesSeed;
  }, [templatesQuery.data]);

  const allTemplates = useMemo(
    () =>
      [...baseTemplates, ...localTemplates].map((item) => ({
        ...item,
        ...(templatePatches[item.id] ?? {}),
      })),
    [baseTemplates, localTemplates, templatePatches],
  );

  const availableTags = useMemo(() => {
    const set = new Set<string>();
    for (const tag of tagsQuery.data?.tags ?? []) set.add(tag);
    for (const row of tagsQuery.data?.rows ?? []) {
      if (row.tag) set.add(row.tag);
    }
    for (const tmpl of allTemplates) {
      for (const tag of tmpl.tags ?? []) set.add(tag);
    }
    return Array.from(set).sort();
  }, [tagsQuery.data, allTemplates]);

  const filteredTemplates = useMemo(() => {
    return allTemplates.filter((tmpl) => {
      if (!showDeleted && tmpl.is_deleted) return false;
      if (filterChannel !== "ALL" && !(tmpl.channels ?? []).includes(filterChannel as NotificationChannel)) return false;
      if (filterStatus !== "ALL" && tmpl.status !== filterStatus) return false;
      if (filterTag !== "ALL" && !(tmpl.tags ?? []).includes(filterTag)) return false;
      if (searchQuery.trim()) {
        const q = searchQuery.trim().toLowerCase();
        const hay = `${tmpl.template_key} ${tmpl.name} ${(tmpl.tags ?? []).join(" ")}`.toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    });
  }, [allTemplates, showDeleted, filterChannel, filterStatus, filterTag, searchQuery]);

  const [selectedTemplateId, setSelectedTemplateId] = useState<string>("tmpl-case-submitted");
  const activeTemplate = useMemo(
    () => allTemplates.find((item) => item.id === selectedTemplateId) ?? filteredTemplates[0] ?? allTemplates[0],
    [allTemplates, filteredTemplates, selectedTemplateId],
  );

  // Batch selection & actions (D3)
  const [selectedBatchIds, setSelectedBatchIds] = useState<string[]>([]);
  const [batchStatusValue, setBatchStatusValue] = useState<TemplateLifecycleStatus>("ARCHIVED");
  const [batchTagMode, setBatchTagMode] = useState<"add" | "remove" | "replace">("add");
  const [batchTagInput, setBatchTagInput] = useState<string>("reviewed");
  const [batchResults, setBatchResults] = useState<BatchTemplateActionResponse["results"] | null>(null);

  const detailQuery = useSWR<TemplateDetailResponse>(
    activeTemplate ? `notification-mfe:template-detail:${activeTemplate.id}` : null,
    async () => {
      if (!activeTemplate) return {};
      const res = await platform.callApi<TemplateDetailResponse>({
        method: "GET",
        path: `/v1/notifications/templates/${encodeURIComponent(activeTemplate.id)}/versions`,
      });
      if (!res.ok) return {};
      return res.data;
    },
    { shouldRetryOnError: false },
  );

  const [localVersionsByTemplate, setLocalVersionsByTemplate] = useState<Record<string, TemplateVersion[]>>(
    defaultTemplateVersionsSeed,
  );
  const [localReleasesByTemplate, setLocalReleasesByTemplate] = useState<Record<string, TemplateRelease[]>>(
    defaultReleasesSeed,
  );

  const versions = useMemo(() => {
    if (!activeTemplate) return [];
    const fromApi = detailQuery.data?.versions;
    if (fromApi && fromApi.length > 0) return fromApi;
    return localVersionsByTemplate[activeTemplate.id] ?? [];
  }, [activeTemplate, detailQuery.data?.versions, localVersionsByTemplate]);

  const releases = useMemo(() => {
    if (!activeTemplate) return [];
    const fromApi = detailQuery.data?.releases;
    if (fromApi && fromApi.length > 0) return fromApi;
    return localReleasesByTemplate[activeTemplate.id] ?? [];
  }, [activeTemplate, detailQuery.data?.releases, localReleasesByTemplate]);

  const [selectedVersionId, setSelectedVersionId] = useState<string>("ver-case-v2-draft");
  const selectedVersion = useMemo(
    () => versions.find((v) => v.id === selectedVersionId) ?? versions[versions.length - 1] ?? versions[0],
    [versions, selectedVersionId],
  );

  // Editor state
  const [editorMode, setEditorMode] = useState<"split" | "edit" | "preview">("split");
  const [schemaMode, setSchemaMode] = useState<"table" | "json">("table");
  const [selectedChannel, setSelectedChannel] = useState<NotificationChannel>("EMAIL");
  const [syntax, setSyntax] = useState<TemplateSyntax>("handlebars/v1");
  const [changeNote, setChangeNote] = useState<string>("");
  const [schemaEditor, setSchemaEditor] = useState<string>("");
  const [variableFields, setVariableFields] = useState<TemplateVariableField[]>([]);
  const [subjectEditor, setSubjectEditor] = useState<string>("");
  const [htmlEditor, setHtmlEditor] = useState<string>("");
  const [textEditor, setTextEditor] = useState<string>("");
  const [providerTemplateId, setProviderTemplateId] = useState<string>("");
  const [paramMappingEditor, setParamMappingEditor] = useState<string>("{}");
  const [serverDiagnostics, setServerDiagnostics] = useState<TemplateDiagnostic[]>([]);
  const [showVersionHistory, setShowVersionHistory] = useState<boolean>(true);
  const [diffCompareVersionId, setDiffCompareVersionId] = useState<string>("ver-case-v1");

  // Create template form
  const [newTemplateKey, setNewTemplateKey] = useState("");
  const [newTemplateName, setNewTemplateName] = useState("");
  const [newTemplateTags, setNewTemplateTags] = useState("workflow");

  // Preview state
  const [previewMode, setPreviewMode] = useState<PreviewMode>("DRAFT");
  const [previewViewport, setPreviewViewport] = useState<"DESKTOP" | "MOBILE" | "SOURCE">("DESKTOP");
  const [previewDeliveryId, setPreviewDeliveryId] = useState<string>("dlv-01936a80-0001");
  const [sampleDataJson, setSampleDataJson] = useState<string>(
    JSON.stringify(
      {
        caseTrackingCode: "HS-2026-00918",
        customerName: "Nguyen Van A",
        branchName: "Chi nhanh Hoan Kiem",
        approvedAmount: "250000000 VND",
      },
      null,
      2,
    ),
  );
  const [previewResult, setPreviewResult] = useState<RenderPreviewResponse | null>(null);
  const [previewLoading, setPreviewLoading] = useState(false);

  const [statusBanner, setStatusBanner] = useState<{ title: string; detail: string } | null>(null);
  const [errorBanner, setErrorBanner] = useState<{ title: string; detail: string } | null>(null);
  const [busy, setBusy] = useState(false);

  const subjectInputRef = useRef<HTMLInputElement | null>(null);
  const htmlInputRef = useRef<HTMLTextAreaElement | null>(null);
  const textInputRef = useRef<HTMLTextAreaElement | null>(null);

  useEffect(() => {
    if (!selectedVersion) return;
    setSyntax(selectedVersion.syntax || "handlebars/v1");
    setChangeNote(selectedVersion.change_note ?? "");
    setSchemaEditor(selectedVersion.variable_schema_json);
    setVariableFields(schemaJsonToVariables(selectedVersion.variable_schema_json));
    const variant = selectedVersion.variants.find((item) => item.channel === selectedChannel);
    setSubjectEditor(variant?.subject_tmpl ?? "");
    setHtmlEditor(variant?.html_tmpl ?? "");
    setTextEditor(variant?.text_tmpl ?? "");
    setProviderTemplateId(variant?.provider_template_id ?? "");
    setParamMappingEditor(variant?.param_mapping_json ?? "{}");
    setServerDiagnostics([]);
  }, [selectedVersion, selectedChannel]);

  useEffect(() => {
    if (replayDeliveryId) {
      setPreviewMode("DELIVERY_REPLAY");
      setPreviewDeliveryId(replayDeliveryId);
      setEditorMode("preview");
    }
  }, [replayDeliveryId]);

  const schemaValid = useMemo(() => {
    try {
      JSON.parse(schemaEditor || "{}");
      return true;
    } catch {
      return false;
    }
  }, [schemaEditor]);

  const isImmutablePublished = selectedVersion?.status === "PUBLISHED";

  const declaredVariableSet = useMemo(
    () => new Set(variableFields.map((v) => v.key.trim()).filter(Boolean)),
    [variableFields],
  );

  const clientDiagnosticReport = useMemo(() => {
    if (syntax !== "handlebars/v1" || selectedChannel === "ZNS") {
      return {
        diagnostics: [] as TemplateDiagnostic[],
        errorCount: 0,
        warningCount: 0,
        bySource: { subject: [], html: [], text: [] } as Record<TemplateSourceTarget, TemplateDiagnostic[]>,
      };
    }
    return diagnoseTemplateSources({
      subjectOrTitle: subjectEditor,
      htmlBody: htmlEditor,
      textBody: textEditor,
      declaredVariables: declaredVariableSet,
    });
  }, [syntax, selectedChannel, subjectEditor, htmlEditor, textEditor, declaredVariableSet]);

  const allDiagnostics = useMemo(
    () => [...clientDiagnosticReport.diagnostics, ...serverDiagnostics],
    [clientDiagnosticReport.diagnostics, serverDiagnostics],
  );

  const syncVariablesToSchema = (nextFields: TemplateVariableField[]) => {
    setVariableFields(nextFields);
    setSchemaEditor(variablesToSchemaJson(nextFields));
    const nextSample: Record<string, string> = {};
    for (const field of nextFields) {
      if (field.key.trim()) nextSample[field.key.trim()] = field.sampleValue;
    }
    setSampleDataJson(JSON.stringify(nextSample, null, 2));
  };

  const handleAddVariable = () => {
    syncVariablesToSchema([
      ...variableFields,
      { key: `var_${variableFields.length + 1}`, label: "New Variable", sampleValue: "Sample", required: false, type: "string" },
    ]);
  };

  const handleUpdateVariable = (idx: number, patch: Partial<TemplateVariableField>) => {
    syncVariablesToSchema(variableFields.map((item, i) => (i === idx ? { ...item, ...patch } : item)));
  };

  const handleRemoveVariable = (idx: number) => {
    syncVariablesToSchema(variableFields.filter((_, i) => i !== idx));
  };

  const handleSchemaJsonChange = (nextJson: string) => {
    setSchemaEditor(nextJson);
    const parsed = schemaJsonToVariables(nextJson);
    if (parsed.length > 0) setVariableFields(parsed);
  };

  const handleInsertVariableToken = (varKey: string, target: TemplateSourceTarget = "html") => {
    const token = syntax === "handlebars/v1" ? `{{${varKey}}}` : `{{ .data.${varKey} }}`;
    if (target === "subject") setSubjectEditor((prev) => `${prev}${token}`);
    else if (target === "text") setTextEditor((prev) => `${prev}${token}`);
    else setHtmlEditor((prev) => `${prev}${token}`);
  };

  const handleJumpToDiagnostic = (diag: TemplateDiagnostic) => {
    const el =
      diag.source === "subject"
        ? subjectInputRef.current
        : diag.source === "text"
          ? textInputRef.current
          : htmlInputRef.current;
    if (el) {
      el.focus();
      if (typeof el.setSelectionRange === "function") {
        el.setSelectionRange(diag.offsetStart, diag.offsetEnd);
      }
    }
  };

  const handleApplyQuickFix = (diag: TemplateDiagnostic) => {
    if (!diag.quickFix || isImmutablePublished) return;
    if (diag.source === "subject") setSubjectEditor((prev) => applyDiagnosticQuickFix(prev, diag.quickFix!));
    else if (diag.source === "text") setTextEditor((prev) => applyDiagnosticQuickFix(prev, diag.quickFix!));
    else setHtmlEditor((prev) => applyDiagnosticQuickFix(prev, diag.quickFix!));
    setStatusBanner({ title: "Quick fix applied", detail: `${diag.code}: ${diag.quickFix.label}` });
  };

  const buildCurrentVariant = (): TemplateVariant =>
    selectedChannel === "ZNS"
      ? {
          id: `var-${selectedVersion?.id ?? "draft"}-zns`,
          channel: "ZNS",
          locale: activeTemplate?.default_locale ?? "vi-VN",
          variant_kind: "PROVIDER_TEMPLATE",
          provider_template_id: providerTemplateId,
          param_mapping_json: paramMappingEditor,
        }
      : {
          id: `var-${selectedVersion?.id ?? "draft"}-${selectedChannel.toLowerCase()}`,
          channel: selectedChannel,
          locale: activeTemplate?.default_locale ?? "vi-VN",
          variant_kind: "INLINE",
          subject_tmpl: subjectEditor,
          html_tmpl: htmlEditor,
          text_tmpl: textEditor,
        };

  const handleServerValidate = async () => {
    if (busy) return;
    setBusy(true);
    setErrorBanner(null);
    const res = await platform.callApi<ServerValidateResponse>({
      method: "POST",
      path: "/v1/notifications/templates/validate",
      body: {
        syntax,
        variable_schema_json: schemaEditor,
        variants: [buildCurrentVariant()],
      },
    });
    setBusy(false);
    if (!res.ok) {
      setErrorBanner({ title: res.problem.title || "Server validation failed", detail: `Code: ${res.problem.code}` });
      return;
    }
    const mapped: TemplateDiagnostic[] = (res.data.diagnostics ?? []).map((d, idx) => ({
      id: `server-${d.code}-${idx}`,
      source: d.target,
      sourceLabel: `Server (${d.target})`,
      severity: d.severity,
      code: d.code as TemplateDiagnostic["code"],
      message: d.message,
      line: d.line,
      column: d.column,
      offsetStart: 0,
      offsetEnd: 0,
      lineSnippet: "",
      tokenText: d.code,
      origin: "server",
      ...(d.quick_fix
        ? {
            quickFix: {
              label: d.quick_fix.label,
              replaceStart: d.quick_fix.replace_start,
              replaceEnd: d.quick_fix.replace_end,
              replacement: d.quick_fix.replacement,
            },
          }
        : {}),
    }));
    setServerDiagnostics(mapped);
    setStatusBanner({
      title: mapped.length === 0 ? "Server validation passed" : `Server validation returned ${mapped.length} issue(s)`,
      detail: `Validated syntax=${syntax} against authoritative AST allowlist & schema rules.`,
    });
  };

  const runPreview = async () => {
    setPreviewLoading(true);
    const res = await platform.callApi<RenderPreviewResponse>({
      method: "POST",
      path: "/v1/notifications/preview",
      body: {
        mode: previewMode,
        template_version_id: selectedVersion?.id ?? "",
        delivery_id: previewMode === "DELIVERY_REPLAY" ? previewDeliveryId : "",
        channel: selectedChannel,
        locale: activeTemplate?.default_locale ?? "vi-VN",
        draft_variable_schema_json: schemaEditor,
        draft_variant: buildCurrentVariant(),
        sample_data_json: sampleDataJson,
      },
    });
    setPreviewLoading(false);
    if (res.ok) setPreviewResult(res.data);
  };

  useEffect(() => {
    void runPreview();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [previewMode, previewDeliveryId, selectedVersion?.id, selectedChannel]);

  const effectiveHtml = useMemo(() => {
    if (previewResult?.html) return previewResult.html;
    const rawHtml = htmlEditor || "<p>No HTML template configured for this channel.</p>";
    return renderFallbackTemplate(rawHtml, sampleDataJson);
  }, [previewResult?.html, htmlEditor, sampleDataJson]);

  const effectiveSubject = useMemo(() => {
    if (previewResult?.subject) return previewResult.subject;
    return renderFallbackTemplate(subjectEditor, sampleDataJson);
  }, [previewResult?.subject, subjectEditor, sampleDataJson]);

  const effectiveSmsText = useMemo(() => {
    if (previewResult?.text) return previewResult.text;
    return renderFallbackTemplate(textEditor, sampleDataJson);
  }, [previewResult?.text, textEditor, sampleDataJson]);

  const smsMetrics = useMemo(() => {
    const local = computeSmsMetrics(effectiveSmsText);
    return {
      encoding: (previewResult?.sms_encoding as "GSM-7" | "UCS-2" | undefined) ?? local.encoding,
      charCount: previewResult?.sms_char_count ?? local.charCount,
      segments: previewResult?.sms_segments ?? local.segments,
    };
  }, [effectiveSmsText, previewResult]);

  const handleCreateTemplate = async () => {
    if (!newTemplateKey.trim() || !newTemplateName.trim() || busy) return;
    setBusy(true);
    setErrorBanner(null);
    const pair = createCommandPair("ntf-create-tmpl");
    const tags = newTemplateTags.split(",").map((t) => t.trim()).filter(Boolean);
    const res = await platform.callApi<{ template?: TemplateSummary }>({
      method: "POST",
      path: "/v1/notifications/templates",
      commandId: pair.commandId,
      idempotencyKey: pair.idempotencyKey,
      body: {
        template_key: newTemplateKey.trim(),
        name: newTemplateName.trim(),
        application_id: "app-case-service",
        category_id: "cat-case-updates",
        default_locale: "vi-VN",
        tags,
      },
    });
    setBusy(false);
    if (!res.ok) {
      setErrorBanner({ title: res.problem.title || "Create template failed", detail: `Code: ${res.problem.code}` });
      return;
    }
    const created: TemplateSummary = res.data.template ?? {
      id: `tmpl-${newTemplateKey.trim().replace(/[^a-z0-9-]/gi, "-")}`,
      template_key: newTemplateKey.trim(),
      name: newTemplateName.trim(),
      application_id: "app-case-service",
      category_id: "cat-case-updates",
      default_locale: "vi-VN",
      status: "DRAFT",
      channels: ["EMAIL"],
      tags,
      usage_24h: 0,
      row_version: 1,
    };
    setLocalTemplates((prev) => [...prev, created]);
    setSelectedTemplateId(created.id);
    setNewTemplateKey("");
    setNewTemplateName("");
    setStatusBanner({ title: "Template created", detail: `Created ${created.template_key} (${pair.commandId}).` });
  };

  const handleSaveDraft = async () => {
    if (!activeTemplate || !selectedVersion || isImmutablePublished || !schemaValid || busy) return;
    setBusy(true);
    setErrorBanner(null);
    const pair = createCommandPair("ntf-save-draft");
    const updatedVariant = buildCurrentVariant();
    const nextVariants = [...selectedVersion.variants.filter((v) => v.channel !== selectedChannel), updatedVariant];
    const res = await platform.callApi<{ version?: TemplateVersion }>({
      method: "PUT",
      path: `/v1/notifications/templates/${encodeURIComponent(activeTemplate.id)}/versions/draft`,
      commandId: pair.commandId,
      idempotencyKey: pair.idempotencyKey,
      body: {
        version_id: selectedVersion.id,
        schema_major: selectedVersion.schema_major,
        syntax,
        variable_schema_json: schemaEditor,
        variants: nextVariants,
        change_note: changeNote,
        expected_row_version: activeTemplate.row_version,
      },
    });
    setBusy(false);
    if (!res.ok) {
      setErrorBanner({ title: res.problem.title || "Save draft failed", detail: `Code: ${res.problem.code}` });
      return;
    }
    setLocalVersionsByTemplate((prev) => {
      const list = prev[activeTemplate.id] ?? versions;
      return {
        ...prev,
        [activeTemplate.id]: list.map((v) =>
          v.id === selectedVersion.id
            ? { ...v, syntax, change_note: changeNote, variable_schema_json: schemaEditor, variants: nextVariants }
            : v,
        ),
      };
    });
    setStatusBanner({
      title: "Draft saved",
      detail: `Saved draft v${selectedVersion.version} (${syntax}) without altering active release pointer.`,
    });
  };

  const handlePublishAndActivate = async () => {
    if (!activeTemplate || !selectedVersion || isImmutablePublished || !schemaValid || busy) return;
    setBusy(true);
    setErrorBanner(null);
    const pubPair = createCommandPair("ntf-publish-ver");
    const pubRes = await platform.callApi<{ version?: TemplateVersion }>({
      method: "POST",
      path: `/v1/notifications/templates/${encodeURIComponent(activeTemplate.id)}/versions/${encodeURIComponent(selectedVersion.id)}/publish`,
      commandId: pubPair.commandId,
      idempotencyKey: pubPair.idempotencyKey,
      body: { template_id: activeTemplate.id, version_id: selectedVersion.id },
    });
    if (!pubRes.ok) {
      setBusy(false);
      setErrorBanner({ title: pubRes.problem.title || "Publish failed", detail: `Code: ${pubRes.problem.code}` });
      return;
    }

    const relPair = createCommandPair("ntf-release");
    const currentRelease = releases.find((r) => r.schema_major === selectedVersion.schema_major);
    const nextOrdinal = (currentRelease?.ordinal ?? 0) + 1;
    const relRes = await platform.callApi<{ release?: TemplateRelease }>({
      method: "POST",
      path: `/v1/notifications/templates/${encodeURIComponent(activeTemplate.id)}/releases`,
      commandId: relPair.commandId,
      idempotencyKey: relPair.idempotencyKey,
      body: {
        template_id: activeTemplate.id,
        schema_major: selectedVersion.schema_major,
        template_version_id: selectedVersion.id,
      },
    });
    setBusy(false);
    if (!relRes.ok) {
      setErrorBanner({ title: relRes.problem.title || "Activate release failed", detail: `Code: ${relRes.problem.code}` });
      return;
    }
    const nextRelease: TemplateRelease = relRes.data.release ?? {
      id: `rel-${activeTemplate.id}-ord${nextOrdinal}`,
      template_id: activeTemplate.id,
      schema_major: selectedVersion.schema_major,
      template_version_id: selectedVersion.id,
      ordinal: nextOrdinal,
      ...(currentRelease ? { previous_ordinal: currentRelease.ordinal } : {}),
      status: "ACTIVE",
    };
    setLocalVersionsByTemplate((prev) => {
      const list = prev[activeTemplate.id] ?? versions;
      return {
        ...prev,
        [activeTemplate.id]: list.map((v) =>
          v.id === selectedVersion.id ? { ...v, status: "PUBLISHED", release_ordinal: nextRelease.ordinal } : v,
        ),
      };
    });
    setLocalReleasesByTemplate((prev) => ({
      ...prev,
      [activeTemplate.id]: [
        ...(prev[activeTemplate.id] ?? releases).filter((r) => r.schema_major !== selectedVersion.schema_major),
        nextRelease,
      ],
    }));
    setStatusBanner({
      title: "Published & Activated Release",
      detail: `Version v${selectedVersion.version} is now immutable and active at release ordinal ${nextRelease.ordinal}.`,
    });
  };

  const handleRollbackToVersion = async (targetVersion: TemplateVersion) => {
    if (!activeTemplate || targetVersion.status !== "PUBLISHED" || busy) return;
    setBusy(true);
    setErrorBanner(null);
    const pair = createCommandPair("ntf-rollback");
    const currentRelease = releases.find((r) => r.schema_major === targetVersion.schema_major);
    const nextOrdinal = (currentRelease?.ordinal ?? 0) + 1;
    const res = await platform.callApi<{ release?: TemplateRelease }>({
      method: "POST",
      path: `/v1/notifications/templates/${encodeURIComponent(activeTemplate.id)}/releases/rollback`,
      commandId: pair.commandId,
      idempotencyKey: pair.idempotencyKey,
      body: {
        template_id: activeTemplate.id,
        schema_major: targetVersion.schema_major,
        template_version_id: targetVersion.id,
        reason: `Rollback to immutable v${targetVersion.version}`,
      },
    });
    setBusy(false);
    if (!res.ok) {
      setErrorBanner({ title: res.problem.title || "Rollback failed", detail: `Code: ${res.problem.code}` });
      return;
    }
    const updatedRelease: TemplateRelease = res.data.release ?? {
      id: `rel-${activeTemplate.id}-ord${nextOrdinal}`,
      template_id: activeTemplate.id,
      schema_major: targetVersion.schema_major,
      template_version_id: targetVersion.id,
      ordinal: nextOrdinal,
      ...(currentRelease ? { previous_ordinal: currentRelease.ordinal } : {}),
      status: "ACTIVE",
    };
    setLocalReleasesByTemplate((prev) => ({
      ...prev,
      [activeTemplate.id]: [
        ...(prev[activeTemplate.id] ?? releases).filter((r) => r.schema_major !== targetVersion.schema_major),
        updatedRelease,
      ],
    }));
    setStatusBanner({
      title: "Monotonic release rollback completed",
      detail: `Created new release ordinal ${updatedRelease.ordinal} pointing to immutable v${targetVersion.version} (version content unchanged).`,
    });
  };

  const handleBatchAction = async (action: "SET_STATUS" | "TAGS" | "DELETE") => {
    if (selectedBatchIds.length === 0 || busy) return;
    setBusy(true);
    setErrorBanner(null);
    setBatchResults(null);
    const pair = createCommandPair("ntf-batch");
    const tags = batchTagInput.split(",").map((t) => t.trim()).filter(Boolean);
    const body =
      action === "SET_STATUS"
        ? { action, ids: selectedBatchIds, status: batchStatusValue }
        : action === "TAGS"
          ? { action, ids: selectedBatchIds, tag_mode: batchTagMode, tags }
          : { action, ids: selectedBatchIds, reason: "Batch soft delete from Templates workspace" };

    const res = await platform.callApi<BatchTemplateActionResponse>({
      method: "POST",
      path: "/v1/notifications/templates/batch-actions",
      commandId: pair.commandId,
      idempotencyKey: pair.idempotencyKey,
      body,
    });
    setBusy(false);
    if (!res.ok) {
      setErrorBanner({ title: res.problem.title || "Batch action failed", detail: `Code: ${res.problem.code}` });
      return;
    }
    const results =
      res.data.results && res.data.results.length > 0
        ? res.data.results
        : selectedBatchIds.map((id) => ({ id, ok: true }));
    setBatchResults(results);
    setTemplatePatches((prev) => {
      const next = { ...prev };
      for (const item of results) {
        if (!item.ok) continue;
        const current = allTemplates.find((t) => t.id === item.id);
        if (!current) continue;
        if (action === "SET_STATUS") {
          next[item.id] = { ...(next[item.id] ?? {}), status: batchStatusValue };
        } else if (action === "DELETE") {
          next[item.id] = { ...(next[item.id] ?? {}), is_deleted: true };
        } else if (action === "TAGS") {
          const curTags = new Set(current.tags ?? []);
          if (batchTagMode === "replace") {
            next[item.id] = { ...(next[item.id] ?? {}), tags };
          } else if (batchTagMode === "add") {
            tags.forEach((t) => curTags.add(t));
            next[item.id] = { ...(next[item.id] ?? {}), tags: Array.from(curTags) };
          } else {
            tags.forEach((t) => curTags.delete(t));
            next[item.id] = { ...(next[item.id] ?? {}), tags: Array.from(curTags) };
          }
        }
      }
      return next;
    });
    setStatusBanner({
      title: `Batch ${action} completed`,
      detail: `${results.filter((r) => r.ok).length}/${results.length} template(s) succeeded.`,
    });
  };

  const handleRestoreTemplate = async (tmpl: TemplateSummary) => {
    if (busy) return;
    setBusy(true);
    setErrorBanner(null);
    const pair = createCommandPair("ntf-restore");
    const res = await platform.callApi({
      method: "POST",
      path: `/v1/notifications/templates/${encodeURIComponent(tmpl.id)}/restore`,
      commandId: pair.commandId,
      idempotencyKey: pair.idempotencyKey,
      body: { expected_version: tmpl.row_version, reason: "Restored from soft-delete view" },
    });
    setBusy(false);
    if (!res.ok) {
      setErrorBanner({ title: res.problem.title || "Restore failed", detail: `Code: ${res.problem.code}` });
      return;
    }
    setTemplatePatches((prev) => ({
      ...prev,
      [tmpl.id]: { ...(prev[tmpl.id] ?? {}), is_deleted: false },
    }));
    setStatusBanner({ title: "Template restored", detail: `Restored ${tmpl.template_key}.` });
  };

  const toggleBatchId = (id: string) => {
    setSelectedBatchIds((prev) => (prev.includes(id) ? prev.filter((item) => item !== id) : [...prev, id]));
  };

  const diffLeft = versions.find((v) => v.id === diffCompareVersionId) ?? versions[0];
  const diffRows = useMemo(() => {
    if (!diffLeft || !selectedVersion || diffLeft.id === selectedVersion.id) return [];
    return computeSideBySideDiff(formatVersionSnapshotForDiff(diffLeft), formatVersionSnapshotForDiff(selectedVersion));
  }, [diffLeft, selectedVersion]);

  if (templatesQuery.isLoading) {
    return <LoadingPanel label="Loading notification templates..." />;
  }

  return (
    <Stack spacing={2}>
      {statusBanner && <StatusPanel title={statusBanner.title} detail={statusBanner.detail} />}
      {errorBanner && <ErrorPanel title={errorBanner.title} detail={errorBanner.detail} />}

      {/* Template Catalog + Filters + Batch Actions */}
      <Paper variant="outlined" sx={{ p: 2 }}>
        <Stack spacing={1.5}>
          <Stack direction={{ xs: "column", md: "row" }} justifyContent="space-between" alignItems={{ xs: "flex-start", md: "center" }} spacing={1}>
            <Box>
              <Typography variant="h6">Templates Catalog</Typography>
              <Typography variant="caption" color="text.secondary">
                One row per template_key with channel chips (D2), 24h usage, tags, and soft-delete restore (D3).
              </Typography>
            </Box>
            <Stack direction="row" spacing={1} alignItems="center" flexWrap="wrap">
              <TextField size="small" label="New template_key" placeholder="loan.reminder" value={newTemplateKey} onChange={(e) => setNewTemplateKey(e.target.value)} sx={{ width: 170 }} />
              <TextField size="small" label="Name" placeholder="Loan Reminder" value={newTemplateName} onChange={(e) => setNewTemplateName(e.target.value)} sx={{ width: 170 }} />
              <TextField size="small" label="Tags (comma)" value={newTemplateTags} onChange={(e) => setNewTemplateTags(e.target.value)} sx={{ width: 140 }} />
              <Button variant="outlined" size="small" disabled={busy || !newTemplateKey.trim() || !newTemplateName.trim()} onClick={() => void handleCreateTemplate()}>
                Create Template
              </Button>
            </Stack>
          </Stack>

          {/* Filter Bar */}
          <Stack direction={{ xs: "column", md: "row" }} spacing={1.25} alignItems="center">
            <TextField
              size="small"
              label="Search templates"
              placeholder="template_key, name, or tag..."
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              sx={{ minWidth: 220 }}
            />
            <FormControl size="small" sx={{ minWidth: 130 }}>
              <InputLabel id="tmpl-filter-channel">Channel</InputLabel>
              <Select labelId="tmpl-filter-channel" label="Channel" value={filterChannel} onChange={(e) => setFilterChannel(e.target.value)}>
                <MenuItem value="ALL">All Channels</MenuItem>
                {ALL_CHANNELS.map((ch) => <MenuItem key={ch} value={ch}>{ch}</MenuItem>)}
              </Select>
            </FormControl>
            <FormControl size="small" sx={{ minWidth: 130 }}>
              <InputLabel id="tmpl-filter-status">Status</InputLabel>
              <Select labelId="tmpl-filter-status" label="Status" value={filterStatus} onChange={(e) => setFilterStatus(e.target.value)}>
                <MenuItem value="ALL">All Statuses</MenuItem>
                <MenuItem value="ACTIVE">ACTIVE</MenuItem>
                <MenuItem value="DRAFT">DRAFT</MenuItem>
                <MenuItem value="ARCHIVED">ARCHIVED</MenuItem>
              </Select>
            </FormControl>
            <FormControl size="small" sx={{ minWidth: 140 }}>
              <InputLabel id="tmpl-filter-tag">Tag</InputLabel>
              <Select labelId="tmpl-filter-tag" label="Tag" value={filterTag} onChange={(e) => setFilterTag(e.target.value)}>
                <MenuItem value="ALL">All Tags</MenuItem>
                {availableTags.map((tag) => <MenuItem key={tag} value={tag}>{tag}</MenuItem>)}
              </Select>
            </FormControl>
            <Button size="small" variant={showDeleted ? "contained" : "outlined"} onClick={() => setShowDeleted((v) => !v)}>
              {showDeleted ? "Hide Soft-Deleted" : "Show Soft-Deleted"}
            </Button>
          </Stack>

          {/* Batch Operations Bar */}
          {selectedBatchIds.length > 0 && (
            <Paper variant="outlined" sx={{ p: 1.5, bgcolor: "#f6faf7", borderColor: "#b9dfca" }}>
              <Stack direction={{ xs: "column", lg: "row" }} spacing={1} alignItems={{ xs: "flex-start", lg: "center" }} justifyContent="space-between">
                <Typography variant="subtitle2">{selectedBatchIds.length} template(s) selected</Typography>
                <Stack direction="row" spacing={1} flexWrap="wrap" alignItems="center">
                  <FormControl size="small" sx={{ minWidth: 115 }}>
                    <InputLabel id="batch-status-label">Status</InputLabel>
                    <Select labelId="batch-status-label" label="Status" value={batchStatusValue} onChange={(e) => setBatchStatusValue(e.target.value as TemplateLifecycleStatus)}>
                      <MenuItem value="ACTIVE">ACTIVE</MenuItem>
                      <MenuItem value="DRAFT">DRAFT</MenuItem>
                      <MenuItem value="ARCHIVED">ARCHIVED</MenuItem>
                    </Select>
                  </FormControl>
                  <Button size="small" variant="outlined" disabled={busy} onClick={() => void handleBatchAction("SET_STATUS")}>
                    Batch Set Status
                  </Button>
                  <FormControl size="small" sx={{ minWidth: 105 }}>
                    <InputLabel id="batch-tag-mode">Tag Mode</InputLabel>
                    <Select labelId="batch-tag-mode" label="Tag Mode" value={batchTagMode} onChange={(e) => setBatchTagMode(e.target.value as "add" | "remove" | "replace")}>
                      <MenuItem value="add">add</MenuItem>
                      <MenuItem value="remove">remove</MenuItem>
                      <MenuItem value="replace">replace</MenuItem>
                    </Select>
                  </FormControl>
                  <TextField size="small" label="Batch tags" value={batchTagInput} onChange={(e) => setBatchTagInput(e.target.value)} sx={{ width: 130 }} />
                  <Button size="small" variant="outlined" disabled={busy} onClick={() => void handleBatchAction("TAGS")}>
                    Batch Apply Tags
                  </Button>
                  <Button size="small" variant="outlined" color="error" disabled={busy} onClick={() => void handleBatchAction("DELETE")}>
                    Batch Soft Delete
                  </Button>
                </Stack>
              </Stack>
            </Paper>
          )}

          {batchResults && (
            <Paper variant="outlined" sx={{ p: 1.25 }}>
              <Typography variant="caption" fontWeight={700} display="block" sx={{ mb: 0.5 }}>Batch Per-Item Results</Typography>
              <Stack direction="row" spacing={1} flexWrap="wrap">
                {batchResults.map((r) => (
                  <Chip
                    key={r.id}
                    size="small"
                    color={r.ok ? "success" : "error"}
                    variant="outlined"
                    label={`${r.id}: ${r.ok ? "OK" : r.error ?? "FAILED"}`}
                  />
                ))}
              </Stack>
            </Paper>
          )}

          {/* Templates Table */}
          <Box sx={{ overflowX: "auto" }}>
            <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 13 }}>
              <thead>
                <tr style={{ borderBottom: "1px solid #e2e7e3", textAlign: "left" }}>
                  <th style={{ padding: "8px 6px", width: 32 }}>Sel</th>
                  <th style={{ padding: "8px 6px" }}>Template Key & Name</th>
                  <th style={{ padding: "8px 6px" }}>Channels</th>
                  <th style={{ padding: "8px 6px" }}>Tags</th>
                  <th style={{ padding: "8px 6px" }}>Status / Release</th>
                  <th style={{ padding: "8px 6px" }}>Usage (24h)</th>
                  <th style={{ padding: "8px 6px" }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {filteredTemplates.map((tmpl) => (
                  <tr
                    key={tmpl.id}
                    style={{
                      borderBottom: "1px solid #eff3f0",
                      backgroundColor: tmpl.id === activeTemplate?.id ? "#f2f8f4" : "transparent",
                    }}
                  >
                    <td style={{ padding: "8px 6px" }}>
                      <input
                        type="checkbox"
                        aria-label={`Select ${tmpl.template_key}`}
                        checked={selectedBatchIds.includes(tmpl.id)}
                        onChange={() => toggleBatchId(tmpl.id)}
                      />
                    </td>
                    <td style={{ padding: "8px 6px" }}>
                      <Button size="small" variant="text" onClick={() => setSelectedTemplateId(tmpl.id)} sx={{ p: 0, textAlign: "left" }}>
                        {tmpl.template_key}
                      </Button>
                      <Typography variant="caption" color="text.secondary" display="block">{tmpl.name}</Typography>
                    </td>
                    <td style={{ padding: "8px 6px" }}>
                      <Stack direction="row" spacing={0.5} flexWrap="wrap">
                        {(tmpl.channels ?? []).map((ch) => (
                          <Chip key={ch} size="small" label={ch} variant="outlined" />
                        ))}
                      </Stack>
                    </td>
                    <td style={{ padding: "8px 6px" }}>
                      <Stack direction="row" spacing={0.5} flexWrap="wrap">
                        {(tmpl.tags ?? []).map((tag) => (
                          <Chip key={tag} size="small" label={`#${tag}`} />
                        ))}
                      </Stack>
                    </td>
                    <td style={{ padding: "8px 6px" }}>
                      <Stack direction="row" spacing={0.5} alignItems="center">
                        <Chip size="small" color={tmpl.is_deleted ? "error" : tmpl.status === "ACTIVE" ? "success" : "default"} label={tmpl.is_deleted ? "DELETED" : tmpl.status} />
                        {tmpl.active_release && (
                          <Chip size="small" variant="outlined" label={`@${tmpl.active_release.schema_major} ord ${tmpl.active_release.ordinal}`} />
                        )}
                      </Stack>
                    </td>
                    <td style={{ padding: "8px 6px" }}>{(tmpl.usage_24h ?? 0).toLocaleString()}</td>
                    <td style={{ padding: "8px 6px" }}>
                      <Stack direction="row" spacing={0.5}>
                        {tmpl.is_deleted ? (
                          <Button size="small" variant="outlined" onClick={() => void handleRestoreTemplate(tmpl)} disabled={busy}>
                            Restore
                          </Button>
                        ) : (
                          <Button
                            size="small"
                            variant="outlined"
                            onClick={() => onNavigateToSend?.(tmpl.template_key, tmpl.active_release?.schema_major ?? 1, tmpl.channels[0] ?? "EMAIL")}
                          >
                            Test Send
                          </Button>
                        )}
                      </Stack>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Box>
        </Stack>
      </Paper>

      {/* Authoring Toolbar */}
      <Paper variant="outlined" sx={{ p: 2 }}>
        <Stack spacing={1.5}>
          <Stack direction={{ xs: "column", lg: "row" }} justifyContent="space-between" alignItems={{ xs: "flex-start", lg: "center" }} spacing={1}>
            <Stack direction="row" spacing={1} alignItems="center" flexWrap="wrap">
              <Typography variant="h6">{activeTemplate?.template_key ?? "Template"} Editor</Typography>
              {releases.map((rel) => (
                <Chip key={rel.id} size="small" color="success" variant="outlined" label={`Active @${rel.schema_major} -> ${rel.template_version_id} (ordinal ${rel.ordinal})`} />
              ))}
            </Stack>

            <Stack direction="row" spacing={1} flexWrap="wrap" alignItems="center">
              <FormControl size="small" sx={{ minWidth: 145 }}>
                <InputLabel id="tmpl-syntax-label">Syntax (D1)</InputLabel>
                <Select labelId="tmpl-syntax-label" label="Syntax (D1)" value={syntax} disabled={isImmutablePublished} onChange={(e) => setSyntax(e.target.value as TemplateSyntax)}>
                  <MenuItem value="handlebars/v1">handlebars/v1</MenuItem>
                  <MenuItem value="gotmpl/v1">gotmpl/v1</MenuItem>
                </Select>
              </FormControl>
              {(["split", "edit", "preview"] as const).map((mode) => (
                <Button key={mode} size="small" variant={editorMode === mode ? "contained" : "outlined"} onClick={() => setEditorMode(mode)}>
                  {mode.toUpperCase()}
                </Button>
              ))}
              <Button size="small" variant={showVersionHistory ? "contained" : "outlined"} onClick={() => setShowVersionHistory((v) => !v)}>
                Version History ({versions.length})
              </Button>
            </Stack>
          </Stack>

          <Stack direction={{ xs: "column", md: "row" }} spacing={1} justifyContent="space-between" alignItems={{ xs: "stretch", md: "center" }}>
            <TextField
              size="small"
              label="Change note"
              placeholder="Describe template changes for version history..."
              value={changeNote}
              onChange={(e) => setChangeNote(e.target.value)}
              disabled={isImmutablePublished}
              sx={{ flex: 1 }}
            />
            <Stack direction="row" spacing={1} flexWrap="wrap">
              <Button size="small" variant="outlined" disabled={busy} onClick={() => void handleServerValidate()}>
                Validate on Server
              </Button>
              <Button size="small" variant="outlined" disabled={busy || isImmutablePublished || !schemaValid} onClick={() => void handleSaveDraft()}>
                Save Draft
              </Button>
              <Button size="small" variant="contained" disabled={busy || isImmutablePublished || !schemaValid || clientDiagnosticReport.errorCount > 0} onClick={() => void handlePublishAndActivate()}>
                Publish & Activate
              </Button>
              <Button
                size="small"
                variant="outlined"
                onClick={() => onNavigateToSend?.(activeTemplate?.template_key ?? "case.submitted", selectedVersion?.schema_major ?? 1, selectedChannel)}
              >
                Open in Send Notification
              </Button>
            </Stack>
          </Stack>
        </Stack>
      </Paper>

      {/* Main Split Area: Editor + Preview + Version History Sidebar */}
      <Stack direction={{ xs: "column", xl: "row" }} spacing={2} alignItems="flex-start">
        {editorMode !== "preview" && (
          <Paper variant="outlined" sx={{ p: 2, flex: 1.15, minWidth: 0, width: "100%" }}>
            <Stack spacing={2}>
              {/* Channel tabs */}
              <Stack direction="row" spacing={0.75} flexWrap="wrap" role="tablist" aria-label="Variant channels">
                {ALL_CHANNELS.map((ch) => (
                  <Button
                    key={ch}
                    size="small"
                    role="tab"
                    aria-selected={selectedChannel === ch}
                    variant={selectedChannel === ch ? "contained" : "outlined"}
                    onClick={() => setSelectedChannel(ch)}
                  >
                    {ch}
                  </Button>
                ))}
              </Stack>

              {/* Variable Table + Advanced JSON Schema toggle */}
              <Stack direction="row" justifyContent="space-between" alignItems="center">
                <Typography variant="subtitle1" fontWeight={700}>Variable Schema</Typography>
                <Stack direction="row" spacing={1}>
                  <Button size="small" variant={schemaMode === "table" ? "contained" : "outlined"} onClick={() => setSchemaMode("table")}>
                    Table View
                  </Button>
                  <Button size="small" variant={schemaMode === "json" ? "contained" : "outlined"} onClick={() => setSchemaMode("json")}>
                    JSON Schema
                  </Button>
                  {schemaMode === "table" && !isImmutablePublished && (
                    <Button size="small" variant="outlined" onClick={handleAddVariable}>+ Add Variable</Button>
                  )}
                </Stack>
              </Stack>

              {schemaMode === "table" ? (
                <Box sx={{ overflowX: "auto" }}>
                  <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 12.5 }}>
                    <thead>
                      <tr style={{ borderBottom: "1px solid #e2e7e3", textAlign: "left" }}>
                        <th style={{ padding: "6px 4px" }}>Key</th>
                        <th style={{ padding: "6px 4px" }}>Label (title)</th>
                        <th style={{ padding: "6px 4px" }}>Type</th>
                        <th style={{ padding: "6px 4px" }}>Sample (example)</th>
                        <th style={{ padding: "6px 4px" }}>Req</th>
                        <th style={{ padding: "6px 4px" }}>Insert</th>
                      </tr>
                    </thead>
                    <tbody>
                      {variableFields.map((field, idx) => (
                        <tr key={`${field.key}-${idx}`} style={{ borderBottom: "1px solid #f0f3f1" }}>
                          <td style={{ padding: "4px" }}>
                            <TextField size="small" value={field.key} disabled={isImmutablePublished} onChange={(e) => handleUpdateVariable(idx, { key: e.target.value })} />
                          </td>
                          <td style={{ padding: "4px" }}>
                            <TextField size="small" value={field.label} disabled={isImmutablePublished} onChange={(e) => handleUpdateVariable(idx, { label: e.target.value })} />
                          </td>
                          <td style={{ padding: "4px" }}>
                            <Select size="small" value={field.type} disabled={isImmutablePublished} onChange={(e) => handleUpdateVariable(idx, { type: e.target.value as VariableFieldType })}>
                              <MenuItem value="string">string</MenuItem>
                              <MenuItem value="number">number (decimal)</MenuItem>
                              <MenuItem value="currency">currency (money)</MenuItem>
                              <MenuItem value="date">date</MenuItem>
                              <MenuItem value="html_fragment">html_fragment</MenuItem>
                            </Select>
                          </td>
                          <td style={{ padding: "4px" }}>
                            <TextField size="small" value={field.sampleValue} disabled={isImmutablePublished} onChange={(e) => handleUpdateVariable(idx, { sampleValue: e.target.value })} />
                          </td>
                          <td style={{ padding: "4px", textAlign: "center" }}>
                            <input
                              type="checkbox"
                              aria-label={`Required ${field.key}`}
                              checked={field.required}
                              disabled={isImmutablePublished}
                              onChange={(e) => handleUpdateVariable(idx, { required: e.target.checked })}
                            />
                          </td>
                          <td style={{ padding: "4px" }}>
                            <Stack direction="row" spacing={0.5}>
                              <Button size="small" variant="text" disabled={isImmutablePublished} onClick={() => handleInsertVariableToken(field.key, selectedChannel === "SMS" ? "text" : "html")}>
                                Insert
                              </Button>
                              {!isImmutablePublished && (
                                <Button size="small" color="error" variant="text" onClick={() => handleRemoveVariable(idx)}>
                                  Del
                                </Button>
                              )}
                            </Stack>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </Box>
              ) : (
                <TextField
                  label="Advanced JSON Schema (2020-12 subset)"
                  multiline
                  minRows={6}
                  fullWidth
                  value={schemaEditor}
                  onChange={(e) => handleSchemaJsonChange(e.target.value)}
                  disabled={isImmutablePublished}
                  error={!schemaValid}
                  sx={{ "& textarea": { fontFamily: "Consolas, monospace", fontSize: 12 } }}
                />
              )}

              {/* Variable Autocomplete Quick-Insert Chips */}
              {variableFields.length > 0 && !isImmutablePublished && (
                <Stack direction="row" spacing={0.75} flexWrap="wrap" alignItems="center">
                  <Typography variant="caption" color="text.secondary">Autocomplete insert:</Typography>
                  {variableFields.map((v) => (
                    <Chip
                      key={v.key}
                      size="small"
                      variant="outlined"
                      label={`{{${v.key}}}`}
                      onClick={() => handleInsertVariableToken(v.key, selectedChannel === "SMS" ? "text" : "html")}
                    />
                  ))}
                </Stack>
              )}

              <Divider />

              {/* Variant Content Editors */}
              {selectedChannel === "ZNS" ? (
                <Stack spacing={1.5}>
                  <TextField size="small" label="Provider Template ID (Zalo ZNS)" value={providerTemplateId} onChange={(e) => setProviderTemplateId(e.target.value)} disabled={isImmutablePublished} />
                  <TextField
                    label="ZNS Parameter Mapping JSON"
                    multiline
                    minRows={4}
                    fullWidth
                    value={paramMappingEditor}
                    onChange={(e) => setParamMappingEditor(e.target.value)}
                    disabled={isImmutablePublished}
                    sx={{ "& textarea": { fontFamily: "Consolas, monospace", fontSize: 12 } }}
                  />
                </Stack>
              ) : (
                <Stack spacing={1.5}>
                  {selectedChannel !== "SMS" && (
                    <TextField
                      size="small"
                      label={`Subject / Title (${syntax})`}
                      fullWidth
                      inputRef={subjectInputRef}
                      value={subjectEditor}
                      onChange={(e) => setSubjectEditor(e.target.value)}
                      disabled={isImmutablePublished}
                    />
                  )}
                  {(selectedChannel === "EMAIL" || selectedChannel === "IN_APP" || selectedChannel === "PUSH") && (
                    <TextField
                      label={`HTML / Handlebars Body (${syntax})`}
                      multiline
                      minRows={6}
                      fullWidth
                      inputRef={htmlInputRef}
                      value={htmlEditor}
                      onChange={(e) => setHtmlEditor(e.target.value)}
                      disabled={isImmutablePublished}
                      sx={{ "& textarea": { fontFamily: "Consolas, monospace", fontSize: 12 } }}
                    />
                  )}
                  <TextField
                    label={selectedChannel === "SMS" ? `SMS Body (${syntax})` : "Plain Text Fallback"}
                    multiline
                    minRows={3}
                    fullWidth
                    inputRef={textInputRef}
                    value={textEditor}
                    onChange={(e) => setTextEditor(e.target.value)}
                    disabled={isImmutablePublished}
                    sx={{ "& textarea": { fontFamily: "Consolas, monospace", fontSize: 12 } }}
                  />
                </Stack>
              )}

              {/* Handlebars Linter + Server Validation Diagnostics Pane */}
              <Paper variant="outlined" sx={{ p: 1.5, bgcolor: allDiagnostics.length > 0 ? "#fffaf6" : "#f7fbf8" }}>
                <Stack spacing={1}>
                  <Stack direction="row" justifyContent="space-between" alignItems="center">
                    <Typography variant="subtitle2">
                      Diagnostics ({clientDiagnosticReport.errorCount} errors, {clientDiagnosticReport.warningCount} warnings{serverDiagnostics.length > 0 ? `, ${serverDiagnostics.length} server` : ""})
                    </Typography>
                    <Chip size="small" color={allDiagnostics.length === 0 ? "success" : "warning"} label={allDiagnostics.length === 0 ? "Clean" : "Issues Detected"} />
                  </Stack>
                  {allDiagnostics.length === 0 ? (
                    <Typography variant="caption" color="text.secondary">
                      No Handlebars syntax or HTML balance issues detected.
                    </Typography>
                  ) : (
                    <Stack spacing={0.75}>
                      {allDiagnostics.map((diag) => (
                        <Paper key={diag.id} variant="outlined" sx={{ p: 1 }}>
                          <Stack direction={{ xs: "column", sm: "row" }} justifyContent="space-between" alignItems={{ xs: "flex-start", sm: "center" }} spacing={1}>
                            <Box>
                              <Stack direction="row" spacing={0.75} alignItems="center">
                                <Chip size="small" color={diag.severity === "error" ? "error" : "warning"} label={diag.code} />
                                <Typography variant="caption" color="text.secondary">
                                  {diag.source} L{diag.line}:C{diag.column} ({diag.origin ?? "client"})
                                </Typography>
                              </Stack>
                              <Typography variant="body2" sx={{ mt: 0.25 }}>{diag.message}</Typography>
                            </Box>
                            <Stack direction="row" spacing={0.5}>
                              <Button size="small" variant="text" onClick={() => handleJumpToDiagnostic(diag)}>
                                Jump to line
                              </Button>
                              {diag.quickFix && !isImmutablePublished && (
                                <Button size="small" variant="outlined" onClick={() => handleApplyQuickFix(diag)}>
                                  Quick Fix: {diag.quickFix.label}
                                </Button>
                              )}
                            </Stack>
                          </Stack>
                        </Paper>
                      ))}
                    </Stack>
                  )}
                </Stack>
              </Paper>
            </Stack>
          </Paper>
        )}

        {/* Preview Pane */}
        {editorMode !== "edit" && (
          <Paper variant="outlined" sx={{ p: 2, flex: 1, minWidth: 0, width: "100%" }}>
            <Stack spacing={1.5}>
              <Stack direction={{ xs: "column", sm: "row" }} justifyContent="space-between" alignItems={{ xs: "flex-start", sm: "center" }} spacing={1}>
                <Typography variant="h6">Sandboxed Preview</Typography>
                <Stack direction="row" spacing={0.75}>
                  {(["DESKTOP", "MOBILE", "SOURCE"] as const).map((vp) => (
                    <Button key={vp} size="small" variant={previewViewport === vp ? "contained" : "outlined"} onClick={() => setPreviewViewport(vp)}>
                      {vp === "MOBILE" ? "Mobile (375px)" : vp}
                    </Button>
                  ))}
                </Stack>
              </Stack>

              <Stack direction={{ xs: "column", sm: "row" }} spacing={1} alignItems="center">
                <FormControl size="small" sx={{ minWidth: 180 }}>
                  <InputLabel id="preview-mode-label">Preview Mode</InputLabel>
                  <Select labelId="preview-mode-label" label="Preview Mode" value={previewMode} onChange={(e) => setPreviewMode(e.target.value as PreviewMode)}>
                    <MenuItem value="DRAFT">DRAFT</MenuItem>
                    <MenuItem value="PUBLISHED_VERSION">PUBLISHED_VERSION</MenuItem>
                    <MenuItem value="DELIVERY_REPLAY">DELIVERY_REPLAY</MenuItem>
                  </Select>
                </FormControl>
                {previewMode === "DELIVERY_REPLAY" && (
                  <TextField size="small" label="Delivery ID" value={previewDeliveryId} onChange={(e) => setPreviewDeliveryId(e.target.value)} />
                )}
                <Button size="small" variant="outlined" disabled={previewLoading} onClick={() => void runPreview()}>
                  {previewLoading ? "Rendering..." : "Refresh Preview"}
                </Button>
                {replayDeliveryId && onClearReplay && (
                  <Button size="small" variant="text" onClick={onClearReplay}>Exit Replay</Button>
                )}
              </Stack>

              {previewMode === "DELIVERY_REPLAY" && (
                <Paper variant="outlined" sx={{ p: 1, bgcolor: "#fff9ec", borderColor: "#f0d399" }}>
                  <Typography variant="caption">
                    DELIVERY_PAYLOAD_INSPECTED audit event is recorded when decrypting retained payloads; expired or secret fields fall back to sample data.
                  </Typography>
                </Paper>
              )}

              <Stack direction="row" spacing={1} flexWrap="wrap">
                <Chip size="small" variant="outlined" label={`SMS Encoding: ${smsMetrics.encoding}`} />
                <Chip size="small" variant="outlined" label={`Characters: ${smsMetrics.charCount}`} />
                <Chip size="small" variant="outlined" label={`Segments: ${smsMetrics.segments}`} />
              </Stack>

              <Typography variant="caption" color="text.secondary">
                Rendered Subject: <strong>{effectiveSubject || "(none)"}</strong>
              </Typography>

              {previewViewport === "SOURCE" ? (
                <Paper variant="outlined" sx={{ p: 1.5, bgcolor: "#f8faf8", overflowX: "auto" }}>
                  <pre style={{ margin: 0, fontSize: 12, fontFamily: "Consolas, monospace", whiteSpace: "pre-wrap" }}>
                    {selectedChannel === "SMS" ? effectiveSmsText : effectiveHtml}
                  </pre>
                </Paper>
              ) : (
                <Box
                  sx={{
                    width: previewViewport === "MOBILE" ? 375 : "100%",
                    maxWidth: "100%",
                    mx: previewViewport === "MOBILE" ? "auto" : 0,
                    border: "1px solid #d7e0d9",
                    borderRadius: 1.5,
                    overflow: "hidden",
                    bgcolor: "#ffffff",
                  }}
                >
                  <iframe
                    title="Notification HTML Preview"
                    sandbox=""
                    srcDoc={selectedChannel === "SMS" ? `<pre style="font-family:sans-serif;padding:12px;white-space:pre-wrap">${effectiveSmsText}</pre>` : effectiveHtml}
                    style={{ width: "100%", height: 320, border: 0, display: "block" }}
                  />
                </Box>
              )}

              <TextField
                label="Sample Data JSON"
                multiline
                minRows={3}
                fullWidth
                value={sampleDataJson}
                onChange={(e) => setSampleDataJson(e.target.value)}
                sx={{ "& textarea": { fontFamily: "Consolas, monospace", fontSize: 12 } }}
              />
            </Stack>
          </Paper>
        )}

        {/* Version History Sidebar */}
        {showVersionHistory && (
          <Paper variant="outlined" sx={{ p: 2, width: { xs: "100%", xl: 310 }, flexShrink: 0 }}>
            <Stack spacing={1.5}>
              <Typography variant="subtitle1" fontWeight={700}>Version History (D2)</Typography>
              <Typography variant="caption" color="text.secondary">
                Published versions are immutable. Rollback creates a new release ordinal+1 pointing to the chosen version.
              </Typography>
              {versions.map((ver) => (
                <Paper
                  key={ver.id}
                  variant="outlined"
                  sx={{ p: 1.25, borderColor: ver.id === selectedVersion?.id ? "#228255" : "#e2e7e3" }}
                >
                  <Stack spacing={0.5}>
                    <Stack direction="row" justifyContent="space-between" alignItems="center">
                      <Typography variant="subtitle2">v{ver.version} (@{ver.schema_major})</Typography>
                      <Chip size="small" color={ver.status === "PUBLISHED" ? "success" : "default"} label={ver.status} />
                    </Stack>
                    <Typography variant="caption" color="text.secondary">
                      Release ordinal: {ver.release_ordinal ?? "-"} • Author: {ver.created_by ?? "system"}
                    </Typography>
                    {ver.change_note && <Typography variant="caption">{ver.change_note}</Typography>}
                    <Stack direction="row" spacing={0.75} sx={{ pt: 0.5 }}>
                      <Button size="small" variant="text" onClick={() => setSelectedVersionId(ver.id)}>Inspect</Button>
                      <Button size="small" variant="text" onClick={() => setDiffCompareVersionId(ver.id)}>Diff</Button>
                      {ver.status === "PUBLISHED" && (
                        <Button size="small" variant="outlined" disabled={busy} onClick={() => void handleRollbackToVersion(ver)}>
                          Rollback
                        </Button>
                      )}
                    </Stack>
                  </Stack>
                </Paper>
              ))}

              {diffRows.length > 0 && (
                <Box>
                  <Typography variant="caption" fontWeight={700} display="block" sx={{ mb: 0.5 }}>
                    Diff: v{diffLeft?.version} vs v{selectedVersion?.version}
                  </Typography>
                  <Box sx={{ maxHeight: 180, overflowY: "auto", border: "1px solid #e2e7e3", borderRadius: 1, p: 0.75, fontFamily: "Consolas, monospace", fontSize: 11 }}>
                    {diffRows.filter((r) => r.status !== "same").slice(0, 20).map((row, idx) => (
                      <div key={idx} style={{ backgroundColor: row.status === "added" ? "#e9f7ef" : row.status === "removed" ? "#fdecea" : "#fff8e1" }}>
                        {row.leftText && <div>- {row.leftText}</div>}
                        {row.rightText && <div>+ {row.rightText}</div>}
                      </div>
                    ))}
                  </Box>
                </Box>
              )}
            </Stack>
          </Paper>
        )}
      </Stack>
    </Stack>
  );
}
