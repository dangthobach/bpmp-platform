export type NotificationSection =
  | "templates"
  | "variable-mapping"
  | "applications"
  | "providers"
  | "delivery-monitoring"
  | "metrics"
  | "audit-logs"
  | "circuit-breaker"
  | "send-notification";

export type NotificationChannel = "EMAIL" | "SMS" | "PUSH" | "IN_APP" | "ZNS" | "WEBHOOK";
export type NotificationLane = "TRANSACTIONAL" | "NORMAL" | "BULK";
export type SendPriorityLabel = "NORMAL" | "HIGH" | "URGENT";
export type SendPriorityValue = 1 | 2 | 3;
export type PreviewMode = "DRAFT" | "PUBLISHED_VERSION" | "DELIVERY_REPLAY";
export type TemplateSyntax = "handlebars/v1" | "gotmpl/v1";
export type TemplateVersionStatus = "DRAFT" | "IN_REVIEW" | "PUBLISHED" | "DEPRECATED" | "RETIRED";
export type TemplateLifecycleStatus = "ACTIVE" | "DRAFT" | "ARCHIVED";
export type VariantKind = "INLINE" | "PROVIDER_TEMPLATE";
export type DriverKind = "smtp" | "http" | "mock" | "inapp";
export type AuthMode = "WORKLOAD_MTLS" | "OIDC_CLIENT" | "API_KEY";
export type ApplicationStatus = "ACTIVE" | "SUSPENDED";
export type BreakerState = "CLOSED" | "OPEN" | "HALF_OPEN";
export type BreakerOverrideMode = "FORCE_OPEN" | "FORCE_CLOSE" | "DRAINING";
export type ProviderHealthStatus = "HEALTHY" | "DEGRADED" | "DOWN";

export type DeliveryStatus =
  | "ACCEPTED"
  | "SCHEDULED"
  | "QUEUED"
  | "SENDING"
  | "SENT"
  | "DELIVERED"
  | "BOUNCED"
  | "FAILED"
  | "DEFERRED"
  | "EXPIRED"
  | "SUPPRESSED"
  | "REJECTED";

export type GroupedDeliveryStatus = "DELIVERED" | "FAILED" | "BOUNCED" | "RETRYING" | "QUEUED";

export function toGroupedDeliveryStatus(status: DeliveryStatus, attemptCount: number): GroupedDeliveryStatus {
  if (status === "DELIVERED" || status === "SENT") return "DELIVERED";
  if (status === "BOUNCED") return "BOUNCED";
  if (status === "FAILED" || status === "EXPIRED" || status === "REJECTED" || status === "SUPPRESSED") return "FAILED";
  if (status === "DEFERRED" || (status === "QUEUED" && attemptCount > 0)) return "RETRYING";
  return "QUEUED";
}

export function mapPriorityLabelToValue(priority: SendPriorityLabel): SendPriorityValue {
  if (priority === "URGENT") return 3;
  if (priority === "HIGH") return 2;
  return 1;
}

export type VariableFieldType = "string" | "number" | "currency" | "date" | "html_fragment";

export interface TemplateVariableField {
  key: string;
  label: string;
  sampleValue: string;
  required: boolean;
  type: VariableFieldType;
  pii?: boolean;
  secret?: boolean;
}

export interface TemplateVariant {
  id: string;
  channel: NotificationChannel;
  locale: string;
  variant_kind: VariantKind;
  subject_tmpl?: string;
  html_tmpl?: string;
  text_tmpl?: string;
  provider_template_id?: string;
  param_mapping_json?: string;
}

export interface TemplateVersion {
  id: string;
  template_id: string;
  version: number;
  schema_major: number;
  syntax: TemplateSyntax;
  layout_version_id?: string;
  variable_schema_json: string;
  content_hash: string;
  status: TemplateVersionStatus;
  change_note?: string;
  created_by?: string;
  release_ordinal?: number;
  published_at_epoch_ms?: number;
  variants: TemplateVariant[];
}

export interface TemplateRelease {
  id: string;
  template_id: string;
  schema_major: number;
  template_version_id: string;
  ordinal: number;
  previous_ordinal?: number;
  activated_at_epoch_ms?: number;
  status: string;
}

export interface TemplateSummary {
  id: string;
  template_key: string;
  application_id: string;
  category_id: string;
  name: string;
  description?: string;
  default_locale: string;
  status: TemplateLifecycleStatus;
  channels: NotificationChannel[];
  tags: string[];
  usage_24h: number;
  active_release?: {
    schema_major: number;
    template_version_id: string;
    version: number;
    ordinal: number;
  };
  is_deleted?: boolean;
  row_version: number;
  updated_at_epoch_ms?: number;
}

export interface BatchTemplateActionResultItem {
  id: string;
  ok: boolean;
  error?: string;
}

export interface BatchTemplateActionResponse {
  results: BatchTemplateActionResultItem[];
}

export interface RenderPreviewResponse {
  channel?: NotificationChannel;
  locale?: string;
  subject?: string;
  html?: string;
  text?: string;
  sms_encoding?: "GSM-7" | "UCS-2" | string;
  sms_char_count?: number;
  sms_segments?: number;
  html_bytes?: number;
  rendered_with_sample_data?: boolean;
  warnings?: string[];
}

export type VariableMappingSeverity = "OK" | "WARN" | "CRITICAL";
export type VariableMappingReason =
  | "OK"
  | "MISSING_IN_CONTEXT"
  | "TYPE_MISMATCH"
  | "UNDECLARED_IN_TEMPLATE"
  | "UNUSED_EXPORT"
  | "UNBOUND_PROCESS";

export interface VariableMappingLink {
  id: string;
  process_key: string;
  process_name?: string;
  trigger_kind: string;
  rule_id?: string;
  template_id?: string;
  template_key: string;
  schema_major: number;
  variable_key: string;
  source_path?: string;
  expected_type?: string;
  actual_type?: string;
  severity: VariableMappingSeverity;
  reason: VariableMappingReason;
}

export interface ProcessContextExportField {
  key: string;
  label: string;
  source_path: string;
  type: VariableFieldType;
  guaranteed: boolean;
}

export interface ProcessMappingNode {
  rule_id: string;
  process_key: string;
  process_name: string;
  trigger_kind: string;
  linked_template_keys: string[];
  exported_variables: ProcessContextExportField[];
  expected_version: number;
}

export interface VariableMappingResponse {
  links: VariableMappingLink[];
  processes: ProcessMappingNode[];
}

export interface ProducerApplication {
  id: string;
  application_key: string;
  name: string;
  description?: string;
  owner_team: string;
  default_sender_identity: string;
  callback_url?: string;
  auth_mode: AuthMode;
  principal_ref: string;
  allowed_namespaces: string[];
  allowed_channels: NotificationChannel[];
  default_lane: NotificationLane;
  quota: {
    per_minute: number;
    per_day: number;
  };
  usage_today: number;
  status: ApplicationStatus;
  active_key_prefix?: string;
  row_version?: number;
}

export interface ProviderPresetOption {
  id: string;
  label: string;
  vendor: string;
  channel: NotificationChannel;
  driver_kind: DriverKind;
  default_endpoint: string;
  note?: string;
  backlog?: boolean;
}

export const PROVIDER_PRESETS: ProviderPresetOption[] = [
  { id: "aws-ses-smtp", label: "AWS SES", vendor: "AWS_SES", channel: "EMAIL", driver_kind: "smtp", default_endpoint: "email-smtp.ap-southeast-1.amazonaws.com:587" },
  { id: "sendgrid-http", label: "SendGrid", vendor: "SENDGRID", channel: "EMAIL", driver_kind: "http", default_endpoint: "https://api.sendgrid.com/v3/mail/send" },
  { id: "smtp-internal", label: "SMTP Internal", vendor: "SMTP_INTERNAL", channel: "EMAIL", driver_kind: "smtp", default_endpoint: "smtp.internal.bpmp.local:587" },
  { id: "firebase-fcm-v1", label: "FCM v1", vendor: "FIREBASE_FCM", channel: "PUSH", driver_kind: "http", default_endpoint: "https://fcm.googleapis.com/v1/projects/bpmp/messages:send" },
  { id: "vnpt-sms-http", label: "VNPT SMS", vendor: "VNPT_SMS", channel: "SMS", driver_kind: "http", default_endpoint: "https://api.vnpt-sms.vn/v2/send" },
  { id: "viettel-brandname", label: "Viettel Brandname", vendor: "VIETTEL_BRANDNAME", channel: "SMS", driver_kind: "http", default_endpoint: "https://ams.viettel.vn/api/v1/brandname" },
  { id: "slack-webhook", label: "Slack Webhook", vendor: "SLACK_WEBHOOK", channel: "WEBHOOK", driver_kind: "http", default_endpoint: "https://hooks.slack.com/services/T000/B000/XXXX" },
  { id: "zalo-zns-mock", label: "Zalo ZNS Mock", vendor: "ZALO_ZNS", channel: "ZNS", driver_kind: "mock", default_endpoint: "mock://zalo-zns.local/v1/template-send" },
  { id: "cockpit-inapp-sse", label: "In-App Inbox", vendor: "IN_APP", channel: "IN_APP", driver_kind: "inapp", default_endpoint: "internal://notification-inapp-inbox" },
];

export interface ProviderProfile {
  provider_id: string;
  provider_key: string;
  name: string;
  channel: NotificationChannel;
  driver_kind: DriverKind;
  preset?: string;
  endpoint?: string;
  sender_identity?: string;
  routing_tier: number;
  routing_weight: number;
  timeout_ms?: number;
  max_retries?: number;
  operational_state: "ACTIVE" | "DISABLED" | "DEGRADED" | "DRAINING" | "OVERRIDDEN" | string;
  breaker_state: BreakerState;
  error_rate_bps: number;
  slow_call_rate_bps?: number;
  failure_threshold_bps?: number;
  slow_call_threshold_ms?: number;
  slow_call_rate_threshold_bps?: number;
  rolling_window_ms?: number;
  window_requests: number;
  window_failures: number;
  buffered_calls?: number;
  failed_calls?: number;
  slow_calls?: number;
  cooldown_remaining_ms?: number;
  fallback_provider?: string;
  avg_latency_ms?: number;
  success_rate_bps?: number;
  sent_24h?: number;
  active_override?: BreakerOverrideMode | undefined;
  override_expires_at_epoch_ms?: number | undefined;
  environment?: "development" | "staging" | "production" | string;
  row_version?: number;
}

export function deriveProviderHealthStatus(provider: ProviderProfile): ProviderHealthStatus {
  if (provider.breaker_state === "OPEN" || provider.operational_state === "DISABLED") return "DOWN";
  if (provider.breaker_state === "HALF_OPEN" || provider.operational_state === "DEGRADED" || provider.operational_state === "DRAINING") {
    return "DEGRADED";
  }
  return "HEALTHY";
}

export interface BreakerTransitionRecord {
  id: string;
  from_state: BreakerState;
  to_state: BreakerState;
  reason: string;
  occurred_at: string;
  actor_ref?: string;
}

export interface DeliveryTraceStep {
  step: string;
  status: "OK" | "WARN" | "ERROR";
  time_ms: number;
  detail: string;
}

export interface DeliveryRecord {
  id: string;
  message_id?: string;
  request_id: string;
  template_key?: string;
  template_version_id: string;
  channel: NotificationChannel;
  lane: NotificationLane;
  status: DeliveryStatus;
  recipient_subject_ref?: string;
  masked_address: string;
  selected_provider_id?: string;
  correlation_id?: string;
  attempt_count: number;
  max_attempts?: number;
  latency_ms?: number;
  error_message?: string;
  has_secret_variables?: boolean;
  payload_expired?: boolean;
  resend_of_delivery_id?: string;
  trace?: DeliveryTraceStep[];
  created_at: string;
  created_at_epoch_ms?: number;
}

export interface DeliveryQueryPage {
  deliveries?: DeliveryRecord[];
  next_before_created_at?: string;
  next_before_created_at_epoch_ms?: number;
  next_before_id?: string;
}

export interface MetricSlice {
  dimension: "CHANNEL" | "LANE" | "PROVIDER";
  key: string;
  accepted: number;
  sent: number;
  delivered: number;
  failed: number;
  deferred: number;
  p50_latency_ms: number;
  p95_latency_ms: number;
  p99_latency_ms: number;
}

export type AuditResourceType = "TEMPLATE" | "PROVIDER" | "APPLICATION" | "CIRCUIT_BREAKER" | "DISPATCH" | "TRIGGER_RULE";
export type AuditSeverity = "INFO" | "WARN" | "CRITICAL";

export interface AuditEvidenceRecord {
  id: string;
  occurred_at: string;
  actor_ref?: string;
  actor_display: string;
  actor_role: string;
  client_ip: string;
  severity: AuditSeverity;
  resource_type: AuditResourceType;
  action: string;
  target_ref: string;
  summary?: string;
  command_id: string;
  before_digest?: string;
  after_digest: string;
  details?: Record<string, unknown>;
}

export interface DryRunPlan {
  template_version_id: string;
  release_ordinal: number;
  variant: {
    channel: NotificationChannel;
    locale: string;
    variant_kind: VariantKind;
  };
  schema_errors: string[];
  quota: {
    remaining: number;
    limit: number;
  };
  routing: {
    candidates: Array<{ provider: string; breaker_state: BreakerState }>;
    selected: string;
    failover_reason?: string;
  };
  warnings: string[];
}

export interface CommandPair {
  commandId: string;
  idempotencyKey: string;
}

export function createCommandPair(prefix: string): CommandPair {
  const token = crypto.randomUUID().replaceAll("-", "");
  return {
    commandId: `${prefix}-cmd-${token}`,
    idempotencyKey: `${prefix}-key-${token}`,
  };
}

export function schemaJsonToVariables(schemaJson: string): TemplateVariableField[] {
  try {
    const parsed = JSON.parse(schemaJson || "{}") as {
      required?: unknown;
      properties?: Record<string, Record<string, unknown>>;
    };
    const requiredSet = new Set(Array.isArray(parsed.required) ? parsed.required.map(String) : []);
    const props = parsed.properties ?? {};
    return Object.entries(props).map(([key, def]) => {
      const rawFormat = typeof def.format === "string" ? def.format : "";
      const rawType = typeof def.type === "string" ? def.type : "string";
      let type: VariableFieldType = "string";
      if (rawFormat === "money" || rawType === "money") type = "currency";
      else if (rawFormat === "decimal" || rawType === "number" || rawType === "integer") type = "number";
      else if (rawFormat === "date" || rawFormat === "date-time") type = "date";
      else if (rawFormat === "html_fragment") type = "html_fragment";
      return {
        key,
        label: typeof def.title === "string" ? def.title : key,
        sampleValue: def.example !== undefined ? String(def.example) : "",
        required: requiredSet.has(key),
        type,
        pii: Boolean(def.pii),
        secret: Boolean(def.secret),
      };
    });
  } catch {
    return [];
  }
}

export function variablesToSchemaJson(variables: TemplateVariableField[]): string {
  const required: string[] = [];
  const properties: Record<string, Record<string, unknown>> = {};
  for (const field of variables) {
    const cleanKey = field.key.trim();
    if (!cleanKey) continue;
    if (field.required) required.push(cleanKey);
    const prop: Record<string, unknown> = {
      type: field.type === "number" ? "string" : "string",
      title: field.label || cleanKey,
      example: field.sampleValue,
    };
    if (field.type === "number") {
      prop.type = "string";
      prop.format = "decimal";
    } else if (field.type === "currency") {
      prop.type = "string";
      prop.format = "money";
    } else if (field.type === "date") {
      prop.type = "string";
      prop.format = "date";
    } else if (field.type === "html_fragment") {
      prop.type = "string";
      prop.format = "html_fragment";
    }
    if (field.pii) prop.pii = true;
    if (field.secret) prop.secret = true;
    properties[cleanKey] = prop;
  }
  return JSON.stringify(
    {
      type: "object",
      required,
      properties,
    },
    null,
    2,
  );
}

const gsm7BasicChars = new Set(
  "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞ !\"#¤%&'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà",
);
const gsm7ExtendedChars = new Set("^{}\\[~]|€\f");

export interface SmsMetrics {
  encoding: "GSM-7" | "UCS-2";
  charCount: number;
  segments: number;
}

export function computeSmsMetrics(text: string): SmsMetrics {
  if (text.length === 0) {
    return { encoding: "GSM-7", charCount: 0, segments: 0 };
  }
  let septets = 0;
  let isGsm7 = true;
  for (const ch of text) {
    if (gsm7BasicChars.has(ch)) {
      septets += 1;
    } else if (gsm7ExtendedChars.has(ch)) {
      septets += 2;
    } else {
      isGsm7 = false;
      break;
    }
  }
  if (isGsm7) {
    const segments = septets <= 160 ? 1 : Math.ceil(septets / 153);
    return { encoding: "GSM-7", charCount: septets, segments };
  }
  const codeUnits = Array.from(text).length;
  const segments = codeUnits <= 70 ? 1 : Math.ceil(codeUnits / 67);
  return { encoding: "UCS-2", charCount: codeUnits, segments };
}

export interface DiffRow {
  leftLineNumber: number | null;
  rightLineNumber: number | null;
  leftText: string;
  rightText: string;
  status: "same" | "added" | "removed" | "changed";
}

export function computeSideBySideDiff(leftSource: string, rightSource: string): DiffRow[] {
  const leftLines = leftSource.split(/\r?\n/);
  const rightLines = rightSource.split(/\r?\n/);
  const maxLen = Math.max(leftLines.length, rightLines.length);
  const rows: DiffRow[] = [];
  for (let i = 0; i < maxLen; i += 1) {
    const left = leftLines[i];
    const right = rightLines[i];
    if (left !== undefined && right !== undefined) {
      rows.push({
        leftLineNumber: i + 1,
        rightLineNumber: i + 1,
        leftText: left,
        rightText: right,
        status: left === right ? "same" : "changed",
      });
    } else if (left !== undefined) {
      rows.push({
        leftLineNumber: i + 1,
        rightLineNumber: null,
        leftText: left,
        rightText: "",
        status: "removed",
      });
    } else if (right !== undefined) {
      rows.push({
        leftLineNumber: null,
        rightLineNumber: i + 1,
        leftText: "",
        rightText: right,
        status: "added",
      });
    }
  }
  return rows;
}

export function formatVersionSnapshotForDiff(version: TemplateVersion): string {
  const header = [
    `version: v${version.version} (schema_major: @${version.schema_major}, status: ${version.status})`,
    `syntax: ${version.syntax}`,
    `release_ordinal: ${version.release_ordinal ?? "-"}`,
    `change_note: ${version.change_note ?? ""}`,
    `content_hash: ${version.content_hash}`,
    "--- variable_schema ---",
    version.variable_schema_json.trim(),
    "--- variants ---",
  ];
  const variantBlocks = version.variants.map((variant) => {
    if (variant.variant_kind === "PROVIDER_TEMPLATE") {
      return `[${variant.channel} / ${variant.locale}] PROVIDER_TEMPLATE id=${variant.provider_template_id ?? ""}\n${variant.param_mapping_json ?? "{}"}`;
    }
    return [
      `[${variant.channel} / ${variant.locale}] INLINE`,
      variant.subject_tmpl ? `subject: ${variant.subject_tmpl}` : "",
      variant.html_tmpl ? `html: ${variant.html_tmpl}` : "",
      variant.text_tmpl ? `text: ${variant.text_tmpl}` : "",
    ]
      .filter(Boolean)
      .join("\n");
  });
  return [...header, ...variantBlocks].join("\n");
}

export function renderFallbackTemplate(templateSource: string, sampleDataJson: string): string {
  let parsedData: Record<string, unknown> = {};
  try {
    const candidate = JSON.parse(sampleDataJson) as unknown;
    if (candidate !== null && typeof candidate === "object" && !Array.isArray(candidate)) {
      parsedData = candidate as Record<string, unknown>;
    }
  } catch {
    // Keep empty object when sample JSON is malformed.
  }

  const lookup = (pathStr: string): string => {
    const clean = pathStr.replace(/^\./, "");
    const segments = clean.split(".");
    if (segments[0] === "data" && segments.length > 1) {
      let cursor: unknown = parsedData;
      for (const key of segments.slice(1)) {
        if (cursor !== null && typeof cursor === "object" && key in (cursor as Record<string, unknown>)) {
          cursor = (cursor as Record<string, unknown>)[key];
        } else {
          cursor = undefined;
          break;
        }
      }
      return cursor === undefined ? `<missing ${pathStr}>` : String(cursor);
    }
    if (clean === "recipient.displayName") return "Nguyen Van A";
    if (clean === "brand.supportHotline") return "1900-6868";
    if (clean in parsedData) return String(parsedData[clean]);
    return `[${clean}]`;
  };

  let output = templateSource;
  output = output.replace(
    /\{\{\s*#if\s+([a-zA-Z0-9_.-]+)\s*\}\}([\s\S]*?)(?:\{\{\s*else\s*\}\}([\s\S]*?))?\{\{\s*\/if\s*\}\}/g,
    (_match, key: string, truthy: string, falsy = "") => {
      const val = parsedData[key];
      return val !== undefined && String(val).trim() !== "" && String(val).toLowerCase() !== "false" ? truthy : falsy;
    },
  );
  output = output.replace(/\{\{\{?\s*([.a-zA-Z0-9_-]+)\s*\}\}\}?/g, (_match, pathStr: string) => lookup(pathStr));
  return output;
}

export const defaultTemplatesSeed: TemplateSummary[] = [
  {
    id: "tmpl-case-submitted",
    template_key: "case.submitted",
    application_id: "app-case-service",
    category_id: "cat-case-updates",
    name: "Case Submitted Confirmation",
    description: "Sent immediately when a customer case is accepted into processing.",
    default_locale: "vi-VN",
    status: "ACTIVE",
    channels: ["EMAIL", "SMS", "IN_APP", "ZNS"],
    tags: ["workflow", "customer-facing", "sla"],
    usage_24h: 1842,
    active_release: {
      schema_major: 1,
      template_version_id: "ver-case-v1",
      version: 1,
      ordinal: 3,
    },
    row_version: 3,
    updated_at_epoch_ms: 1764154800000,
  },
  {
    id: "tmpl-auth-otp",
    template_key: "auth.otp_challenge",
    application_id: "app-authz-cp",
    category_id: "cat-security-otp",
    name: "Security OTP Verification",
    description: "Transactional OTP verification challenge for high-risk actions.",
    default_locale: "vi-VN",
    status: "ACTIVE",
    channels: ["SMS", "EMAIL"],
    tags: ["security", "otp"],
    usage_24h: 6410,
    active_release: {
      schema_major: 1,
      template_version_id: "ver-otp-v1",
      version: 1,
      ordinal: 1,
    },
    row_version: 2,
    updated_at_epoch_ms: 1764151200000,
  },
  {
    id: "tmpl-loan-approved",
    template_key: "loan.approved",
    application_id: "app-case-service",
    category_id: "cat-case-updates",
    name: "Loan Disbursement Approval",
    description: "Customer notification for approved loan disbursement.",
    default_locale: "vi-VN",
    status: "DRAFT",
    channels: ["EMAIL", "PUSH"],
    tags: ["lending", "workflow"],
    usage_24h: 0,
    row_version: 1,
    updated_at_epoch_ms: 1764140000000,
  },
];

export const defaultTemplateVersionsSeed: Record<string, TemplateVersion[]> = {
  "tmpl-case-submitted": [
    {
      id: "ver-case-v1",
      template_id: "tmpl-case-submitted",
      version: 1,
      schema_major: 1,
      syntax: "handlebars/v1",
      layout_version_id: "layout-standard-v1",
      variable_schema_json: JSON.stringify(
        {
          type: "object",
          required: ["caseTrackingCode", "customerName"],
          properties: {
            caseTrackingCode: { type: "string", title: "Case Tracking Code", example: "HS-2026-00918" },
            customerName: { type: "string", title: "Customer Name", pii: true, example: "Nguyen Van A" },
          },
        },
        null,
        2,
      ),
      content_hash: "sha256:4a8d9102e9f810c21b9d73",
      status: "PUBLISHED",
      change_note: "Initial production release for case submission confirmation.",
      created_by: "officer.linh",
      release_ordinal: 3,
      published_at_epoch_ms: 1764100000000,
      variants: [
        {
          id: "var-case-v1-email",
          channel: "EMAIL",
          locale: "vi-VN",
          variant_kind: "INLINE",
          subject_tmpl: "[BPMP] Xac nhan tiep nhan ho so {{caseTrackingCode}}",
          html_tmpl:
            "<main style=\"font-family:Segoe UI,Arial,sans-serif;padding:16px\"><h2>Xin chao {{customerName}}</h2><p>Ho so <strong>{{caseTrackingCode}}</strong> da duoc tiep nhan thanh cong.</p></main>",
          text_tmpl: "Xin chao {{customerName}}, ho so {{caseTrackingCode}} da duoc tiep nhan.",
        },
        {
          id: "var-case-v1-sms",
          channel: "SMS",
          locale: "vi-VN",
          variant_kind: "INLINE",
          text_tmpl: "BPMP: Ho so {{caseTrackingCode}} cua {{customerName}} da duoc tiep nhan.",
        },
        {
          id: "var-case-v1-inapp",
          channel: "IN_APP",
          locale: "vi-VN",
          variant_kind: "INLINE",
          subject_tmpl: "Ho so {{caseTrackingCode}} da tiep nhan",
          html_tmpl: "<p>Ho so <strong>{{caseTrackingCode}}</strong> dang duoc xu ly.</p>",
        },
        {
          id: "var-case-v1-zns",
          channel: "ZNS",
          locale: "vi-VN",
          variant_kind: "PROVIDER_TEMPLATE",
          provider_template_id: "zns_mock_case_submitted_001",
          param_mapping_json: JSON.stringify(
            { tracking_code: "{{caseTrackingCode}}", customer_name: "{{customerName}}" },
            null,
            2,
          ),
        },
      ],
    },
    {
      id: "ver-case-v2-draft",
      template_id: "tmpl-case-submitted",
      version: 2,
      schema_major: 1,
      syntax: "handlebars/v1",
      layout_version_id: "layout-standard-v1",
      variable_schema_json: JSON.stringify(
        {
          type: "object",
          required: ["caseTrackingCode", "customerName"],
          properties: {
            caseTrackingCode: { type: "string", title: "Case Tracking Code", example: "HS-2026-00918" },
            customerName: { type: "string", title: "Customer Name", pii: true, example: "Nguyen Van A" },
            branchName: { type: "string", title: "Branch Name", example: "Chi nhanh Hoan Kiem" },
            approvedAmount: { type: "string", format: "money", title: "Approved Amount", example: "250000000 VND" },
          },
        },
        null,
        2,
      ),
      content_hash: "sha256:9c12fe7845bc20a18873ef",
      status: "DRAFT",
      change_note: "Add optional branchName conditional block and approvedAmount.",
      created_by: "author.an",
      variants: [
        {
          id: "var-case-v2-email",
          channel: "EMAIL",
          locale: "vi-VN",
          variant_kind: "INLINE",
          subject_tmpl: "[BPMP] Ho so {{caseTrackingCode}} da tiep nhan{{#if branchName}} tai {{branchName}}{{/if}}",
          html_tmpl:
            "<main style=\"font-family:Segoe UI,Arial,sans-serif;padding:16px;border:1px solid #d7e8de;border-radius:8px\"><h2>Xin chao {{customerName}}</h2><p>Ho so <strong>{{caseTrackingCode}}</strong> da duoc tiep nhan{{#if branchName}} tai <em>{{branchName}}</em>{{/if}}.</p><p>Tong dai ho tro: {{brand.supportHotline}}</p></main>",
          text_tmpl:
            "Xin chao {{customerName}}, ho so {{caseTrackingCode}} da tiep nhan tai {{branchName}}.",
        },
        {
          id: "var-case-v2-sms",
          channel: "SMS",
          locale: "vi-VN",
          variant_kind: "INLINE",
          text_tmpl: "BPMP: Ho so {{caseTrackingCode}} tai {{branchName}} da duoc tiep nhan.",
        },
        {
          id: "var-case-v2-inapp",
          channel: "IN_APP",
          locale: "vi-VN",
          variant_kind: "INLINE",
          subject_tmpl: "Tiep nhan ho so {{caseTrackingCode}}",
          html_tmpl: "<p>Ho so <strong>{{caseTrackingCode}}</strong> tai {{branchName}} dang xu ly.</p>",
        },
        {
          id: "var-case-v2-zns",
          channel: "ZNS",
          locale: "vi-VN",
          variant_kind: "PROVIDER_TEMPLATE",
          provider_template_id: "zns_mock_case_submitted_002",
          param_mapping_json: JSON.stringify(
            {
              tracking_code: "{{caseTrackingCode}}",
              customer_name: "{{customerName}}",
              branch_name: "{{branchName}}",
            },
            null,
            2,
          ),
        },
      ],
    },
  ],
  "tmpl-auth-otp": [
    {
      id: "ver-otp-v1",
      template_id: "tmpl-auth-otp",
      version: 1,
      schema_major: 1,
      syntax: "handlebars/v1",
      variable_schema_json: JSON.stringify(
        {
          type: "object",
          required: ["otpCode", "expiresInMinutes"],
          properties: {
            otpCode: { type: "string", title: "OTP Code", secret: true, example: "482910" },
            expiresInMinutes: { type: "string", format: "decimal", title: "Expiry Minutes", example: "5" },
          },
        },
        null,
        2,
      ),
      content_hash: "sha256:77c12a0f9821",
      status: "PUBLISHED",
      change_note: "Initial OTP template with secret masking.",
      created_by: "sec.tuan",
      release_ordinal: 1,
      published_at_epoch_ms: 1764090000000,
      variants: [
        {
          id: "var-otp-v1-sms",
          channel: "SMS",
          locale: "vi-VN",
          variant_kind: "INLINE",
          text_tmpl: "BPMP OTP: {{otpCode}}. Co hieu luc trong {{expiresInMinutes}} phut.",
        },
        {
          id: "var-otp-v1-email",
          channel: "EMAIL",
          locale: "vi-VN",
          variant_kind: "INLINE",
          subject_tmpl: "[BPMP] Ma xac thuc OTP",
          html_tmpl: "<p>Ma OTP cua ban la <strong>{{otpCode}}</strong> (het han sau {{expiresInMinutes}} phut).</p>",
          text_tmpl: "Ma OTP: {{otpCode}}",
        },
      ],
    },
  ],
};

export const defaultReleasesSeed: Record<string, TemplateRelease[]> = {
  "tmpl-case-submitted": [
    {
      id: "rel-case-m1-ord3",
      template_id: "tmpl-case-submitted",
      schema_major: 1,
      template_version_id: "ver-case-v1",
      ordinal: 3,
      previous_ordinal: 2,
      activated_at_epoch_ms: 1764110000000,
      status: "ACTIVE",
    },
  ],
  "tmpl-auth-otp": [
    {
      id: "rel-otp-m1-ord1",
      template_id: "tmpl-auth-otp",
      schema_major: 1,
      template_version_id: "ver-otp-v1",
      ordinal: 1,
      activated_at_epoch_ms: 1764090000000,
      status: "ACTIVE",
    },
  ],
};

export const defaultVariableMappingSeed: VariableMappingResponse = {
  processes: [
    {
      rule_id: "rule-case-intake",
      process_key: "credit_case_intake",
      process_name: "Credit Case Intake Workflow",
      trigger_kind: "STEP_COMPLETED",
      linked_template_keys: ["case.submitted"],
      expected_version: 2,
      exported_variables: [
        { key: "caseTrackingCode", label: "Case Tracking Code", source_path: "vars.caseTrackingCode", type: "string", guaranteed: true },
        { key: "customerName", label: "Customer Name", source_path: "vars.customerName", type: "string", guaranteed: true },
        { key: "approvedAmount", label: "Approved Amount", source_path: "vars.approvedAmount", type: "number", guaranteed: true },
        { key: "internalAuditFlag", label: "Internal Audit Flag", source_path: "vars.internalAuditFlag", type: "string", guaranteed: false },
      ],
    },
    {
      rule_id: "rule-loan-disbursement",
      process_key: "loan_disbursement_v2",
      process_name: "Loan Disbursement Workflow",
      trigger_kind: "PROCESS_COMPLETED",
      linked_template_keys: [],
      expected_version: 1,
      exported_variables: [
        { key: "loanRef", label: "Loan Reference", source_path: "vars.loanRef", type: "string", guaranteed: true },
      ],
    },
  ],
  links: [
    {
      id: "link-1",
      process_key: "credit_case_intake",
      process_name: "Credit Case Intake Workflow",
      trigger_kind: "STEP_COMPLETED",
      rule_id: "rule-case-intake",
      template_id: "tmpl-case-submitted",
      template_key: "case.submitted",
      schema_major: 1,
      variable_key: "caseTrackingCode",
      source_path: "vars.caseTrackingCode",
      expected_type: "string",
      actual_type: "string",
      severity: "OK",
      reason: "OK",
    },
    {
      id: "link-2",
      process_key: "credit_case_intake",
      process_name: "Credit Case Intake Workflow",
      trigger_kind: "STEP_COMPLETED",
      rule_id: "rule-case-intake",
      template_id: "tmpl-case-submitted",
      template_key: "case.submitted",
      schema_major: 1,
      variable_key: "branchName",
      source_path: "vars.branchName",
      expected_type: "string",
      severity: "CRITICAL",
      reason: "MISSING_IN_CONTEXT",
    },
    {
      id: "link-3",
      process_key: "credit_case_intake",
      process_name: "Credit Case Intake Workflow",
      trigger_kind: "STEP_COMPLETED",
      rule_id: "rule-case-intake",
      template_id: "tmpl-case-submitted",
      template_key: "case.submitted",
      schema_major: 1,
      variable_key: "approvedAmount",
      source_path: "vars.approvedAmount",
      expected_type: "money",
      actual_type: "decimal",
      severity: "WARN",
      reason: "TYPE_MISMATCH",
    },
    {
      id: "link-4",
      process_key: "credit_case_intake",
      process_name: "Credit Case Intake Workflow",
      trigger_kind: "STEP_COMPLETED",
      rule_id: "rule-case-intake",
      template_id: "tmpl-case-submitted",
      template_key: "case.submitted",
      schema_major: 1,
      variable_key: "slaDeadline",
      source_path: "vars.slaDeadline",
      severity: "WARN",
      reason: "UNDECLARED_IN_TEMPLATE",
    },
    {
      id: "link-5",
      process_key: "credit_case_intake",
      process_name: "Credit Case Intake Workflow",
      trigger_kind: "STEP_COMPLETED",
      rule_id: "rule-case-intake",
      template_id: "tmpl-case-submitted",
      template_key: "case.submitted",
      schema_major: 1,
      variable_key: "internalAuditFlag",
      source_path: "vars.internalAuditFlag",
      severity: "OK",
      reason: "UNUSED_EXPORT",
    },
    {
      id: "link-6",
      process_key: "loan_disbursement_v2",
      process_name: "Loan Disbursement Workflow",
      trigger_kind: "PROCESS_COMPLETED",
      rule_id: "rule-loan-disbursement",
      template_key: "",
      schema_major: 1,
      variable_key: "loanRef",
      source_path: "vars.loanRef",
      severity: "WARN",
      reason: "UNBOUND_PROCESS",
    },
  ],
};

export const defaultApplicationsSeed: ProducerApplication[] = [
  {
    id: "app-case-service",
    application_key: "case-service",
    name: "Customer Case Service",
    description: "Primary ODS workflow & case lifecycle producer.",
    owner_team: "Core Lending Platform",
    default_sender_identity: "no-reply@bpmp.vn",
    callback_url: "https://case-service.internal/hooks/notification-status",
    auth_mode: "WORKLOAD_MTLS",
    principal_ref: "spiffe://bpmp.internal/ns/ods/sa/case-service",
    allowed_namespaces: ["case.*", "portal.*"],
    allowed_channels: ["EMAIL", "SMS", "IN_APP", "ZNS"],
    default_lane: "NORMAL",
    quota: { per_minute: 7200, per_day: 250000 },
    usage_today: 41280,
    status: "ACTIVE",
    row_version: 4,
  },
  {
    id: "app-legacy-crm",
    application_key: "legacy-crm-bridge",
    name: "Legacy CRM Adapter",
    description: "Bridge adapter for legacy CRM notifications via hashed API keys.",
    owner_team: "Customer Engagement Ops",
    default_sender_identity: "care@bpmp.vn",
    callback_url: "https://crm-bridge.internal/v1/delivery-events",
    auth_mode: "API_KEY",
    principal_ref: "client:legacy-crm",
    allowed_namespaces: ["crm.*"],
    allowed_channels: ["EMAIL", "SMS"],
    default_lane: "BULK",
    quota: { per_minute: 1500, per_day: 50000 },
    usage_today: 12450,
    status: "ACTIVE",
    active_key_prefix: "ntf_live_9f21a8",
    row_version: 2,
  },
];

export const defaultProvidersSeed: ProviderProfile[] = [
  {
    provider_id: "prv-smtp-ses",
    provider_key: "ses-smtp-primary",
    name: "Amazon SES SMTP Relay",
    channel: "EMAIL",
    driver_kind: "smtp",
    preset: "aws-ses-smtp",
    endpoint: "email-smtp.ap-southeast-1.amazonaws.com:587",
    sender_identity: "no-reply@bpmp.vn",
    routing_tier: 1,
    routing_weight: 100,
    timeout_ms: 5000,
    max_retries: 3,
    operational_state: "ACTIVE",
    breaker_state: "CLOSED",
    error_rate_bps: 42,
    slow_call_rate_bps: 120,
    failure_threshold_bps: 500,
    slow_call_threshold_ms: 2500,
    slow_call_rate_threshold_bps: 1000,
    rolling_window_ms: 60000,
    window_requests: 1200,
    window_failures: 5,
    buffered_calls: 1200,
    failed_calls: 5,
    slow_calls: 14,
    cooldown_remaining_ms: 0,
    fallback_provider: "smtp-internal-backup",
    avg_latency_ms: 420,
    success_rate_bps: 9958,
    sent_24h: 14820,
    environment: "staging",
    row_version: 5,
  },
  {
    provider_id: "prv-http-sms",
    provider_key: "vnpt-sms-brandname",
    name: "VNPT SMS Brandname Gateway",
    channel: "SMS",
    driver_kind: "http",
    preset: "vnpt-sms-http",
    endpoint: "https://api.vnpt-sms.vn/v2/send",
    sender_identity: "BPMP_BANK",
    routing_tier: 1,
    routing_weight: 80,
    timeout_ms: 4000,
    max_retries: 2,
    operational_state: "DEGRADED",
    breaker_state: "HALF_OPEN",
    error_rate_bps: 680,
    slow_call_rate_bps: 840,
    failure_threshold_bps: 500,
    slow_call_threshold_ms: 2000,
    slow_call_rate_threshold_bps: 1000,
    rolling_window_ms: 60000,
    window_requests: 250,
    window_failures: 17,
    buffered_calls: 250,
    failed_calls: 17,
    slow_calls: 21,
    cooldown_remaining_ms: 18000,
    fallback_provider: "viettel-brandname-fallback",
    avg_latency_ms: 1380,
    success_rate_bps: 9320,
    sent_24h: 6240,
    environment: "staging",
    row_version: 3,
  },
  {
    provider_id: "prv-mock-zns",
    provider_key: "zalo-zns-primary",
    name: "Zalo ZNS (mock)",
    channel: "ZNS",
    driver_kind: "mock",
    preset: "zalo-zns-mock",
    endpoint: "mock://zalo-zns.local/v1/template-send",
    sender_identity: "BPMP_OA",
    routing_tier: 1,
    routing_weight: 100,
    timeout_ms: 3000,
    max_retries: 2,
    operational_state: "ACTIVE",
    breaker_state: "CLOSED",
    error_rate_bps: 0,
    slow_call_rate_bps: 0,
    failure_threshold_bps: 500,
    slow_call_threshold_ms: 1500,
    slow_call_rate_threshold_bps: 1000,
    rolling_window_ms: 60000,
    window_requests: 340,
    window_failures: 0,
    buffered_calls: 340,
    failed_calls: 0,
    slow_calls: 0,
    cooldown_remaining_ms: 0,
    avg_latency_ms: 185,
    success_rate_bps: 10000,
    sent_24h: 2150,
    environment: "staging",
    row_version: 2,
  },
  {
    provider_id: "prv-inapp-hub",
    provider_key: "ods-inapp-store",
    name: "ODS Shell In-App Inbox",
    channel: "IN_APP",
    driver_kind: "inapp",
    preset: "cockpit-inapp-sse",
    endpoint: "internal://notification-inapp-inbox",
    sender_identity: "BPMP_SYSTEM",
    routing_tier: 1,
    routing_weight: 100,
    timeout_ms: 1500,
    max_retries: 1,
    operational_state: "ACTIVE",
    breaker_state: "CLOSED",
    error_rate_bps: 0,
    slow_call_rate_bps: 0,
    failure_threshold_bps: 500,
    slow_call_threshold_ms: 500,
    slow_call_rate_threshold_bps: 1000,
    rolling_window_ms: 60000,
    window_requests: 890,
    window_failures: 0,
    buffered_calls: 890,
    failed_calls: 0,
    slow_calls: 0,
    cooldown_remaining_ms: 0,
    avg_latency_ms: 48,
    success_rate_bps: 10000,
    sent_24h: 19400,
    environment: "staging",
    row_version: 2,
  },
];

export const defaultBreakerTransitionsSeed: Record<string, BreakerTransitionRecord[]> = {
  "prv-http-sms": [
    {
      id: "trn-1",
      from_state: "CLOSED",
      to_state: "OPEN",
      reason: "Rolling failure rate exceeded 5.0% threshold (6.8% over 60s window).",
      occurred_at: "2026-11-26T18:10:00Z",
      actor_ref: "system:circuit-breaker",
    },
    {
      id: "trn-2",
      from_state: "OPEN",
      to_state: "HALF_OPEN",
      reason: "Cooldown elapsed; probing upstream carrier health.",
      occurred_at: "2026-11-26T18:15:00Z",
      actor_ref: "system:circuit-breaker",
    },
  ],
};

export const defaultDeliveriesSeed: DeliveryRecord[] = [
  {
    id: "dlv-01936a80-0001",
    message_id: "msg-01936a80-0001",
    request_id: "req-01936a80-9001",
    template_key: "case.submitted@1",
    template_version_id: "ver-case-v1",
    channel: "EMAIL",
    lane: "NORMAL",
    status: "DELIVERED",
    recipient_subject_ref: "subj:customer:8812",
    masked_address: "n***a@example.vn",
    selected_provider_id: "ses-smtp-primary",
    correlation_id: "corr-case-8812",
    attempt_count: 1,
    max_attempts: 3,
    latency_ms: 412,
    created_at: "2026-11-26T18:45:10Z",
    created_at_epoch_ms: 1764182710000,
    trace: [
      { step: "INGEST_VALIDATE", status: "OK", time_ms: 14, detail: "Validated case.submitted@1 schema & quota." },
      { step: "RENDER_TEMPLATE", status: "OK", time_ms: 28, detail: "Rendered ver-case-v1 (ordinal 3) via gotmpl/v1 AST." },
      { step: "ROUTE_PROVIDER", status: "OK", time_ms: 5, detail: "Selected tier-1 ses-smtp-primary (breaker CLOSED)." },
      { step: "PROVIDER_DISPATCH", status: "OK", time_ms: 365, detail: "250 Ok SMTP relay accepted." },
    ],
  },
  {
    id: "dlv-01936a7f-0002",
    message_id: "msg-01936a7f-0002",
    request_id: "req-01936a7f-9002",
    template_key: "case.submitted@1",
    template_version_id: "ver-case-v1",
    channel: "SMS",
    lane: "TRANSACTIONAL",
    status: "FAILED",
    recipient_subject_ref: "subj:customer:9920",
    masked_address: "+8490****321",
    selected_provider_id: "vnpt-sms-brandname",
    correlation_id: "corr-case-9920",
    attempt_count: 3,
    max_attempts: 3,
    latency_ms: 4010,
    error_message: "UPSTREAM_TIMEOUT: Carrier gateway returned HTTP 504 after 3 attempts.",
    created_at: "2026-11-26T18:40:05Z",
    created_at_epoch_ms: 1764182405000,
    trace: [
      { step: "INGEST_VALIDATE", status: "OK", time_ms: 11, detail: "Validated case.submitted@1 schema & quota." },
      { step: "RENDER_TEMPLATE", status: "OK", time_ms: 19, detail: "Rendered SMS GSM-7 (1 segment)." },
      { step: "ROUTE_PROVIDER", status: "WARN", time_ms: 6, detail: "Primary vnpt-sms-brandname HALF_OPEN; attempted probe." },
      { step: "PROVIDER_DISPATCH", status: "ERROR", time_ms: 3974, detail: "HTTP 504 Gateway Timeout on attempt 3/3." },
    ],
  },
];

export const defaultMetricsSeed: MetricSlice[] = [
  { dimension: "CHANNEL", key: "EMAIL", accepted: 14820, sent: 14742, delivered: 14710, failed: 32, deferred: 78, p50_latency_ms: 380, p95_latency_ms: 840, p99_latency_ms: 1420 },
  { dimension: "CHANNEL", key: "SMS", accepted: 6240, sent: 6110, delivered: 6015, failed: 95, deferred: 130, p50_latency_ms: 720, p95_latency_ms: 1650, p99_latency_ms: 2890 },
  { dimension: "CHANNEL", key: "IN_APP", accepted: 19400, sent: 19400, delivered: 19398, failed: 2, deferred: 0, p50_latency_ms: 42, p95_latency_ms: 120, p99_latency_ms: 210 },
  { dimension: "CHANNEL", key: "ZNS", accepted: 2150, sent: 2150, delivered: 2150, failed: 0, deferred: 0, p50_latency_ms: 140, p95_latency_ms: 210, p99_latency_ms: 340 },
  { dimension: "LANE", key: "TRANSACTIONAL", accepted: 8410, sent: 8384, delivered: 8360, failed: 24, deferred: 26, p50_latency_ms: 290, p95_latency_ms: 690, p99_latency_ms: 1180 },
  { dimension: "LANE", key: "NORMAL", accepted: 24100, sent: 23965, delivered: 23890, failed: 75, deferred: 135, p50_latency_ms: 810, p95_latency_ms: 2450, p99_latency_ms: 4120 },
  { dimension: "LANE", key: "BULK", accepted: 10100, sent: 10053, delivered: 10023, failed: 30, deferred: 47, p50_latency_ms: 4200, p95_latency_ms: 11800, p99_latency_ms: 18400 },
  { dimension: "PROVIDER", key: "ses-smtp-primary", accepted: 14820, sent: 14742, delivered: 14710, failed: 32, deferred: 78, p50_latency_ms: 380, p95_latency_ms: 840, p99_latency_ms: 1420 },
  { dimension: "PROVIDER", key: "vnpt-sms-brandname", accepted: 6240, sent: 6110, delivered: 6015, failed: 95, deferred: 130, p50_latency_ms: 720, p95_latency_ms: 1650, p99_latency_ms: 2890 },
  { dimension: "PROVIDER", key: "zalo-zns-primary", accepted: 2150, sent: 2150, delivered: 2150, failed: 0, deferred: 0, p50_latency_ms: 140, p95_latency_ms: 210, p99_latency_ms: 340 },
];

export const defaultAuditSeed: AuditEvidenceRecord[] = [
  {
    id: "aud-01936a82-101",
    occurred_at: "2026-11-26T18:50:00Z",
    actor_ref: "user:officer-linh",
    actor_display: "Nguyen Thi Linh",
    actor_role: "NOTIFICATION_ADMIN",
    client_ip: "10.24.18.14",
    severity: "INFO",
    resource_type: "TEMPLATE",
    action: "TEMPLATE_RELEASE_ACTIVATED",
    target_ref: "template:case.submitted@1 (ordinal 3)",
    summary: "Activated immutable version ver-case-v1 on release ordinal 3.",
    command_id: "ntf-release-cmd-7712a",
    before_digest: "sha256:1109ab",
    after_digest: "sha256:4a8d91",
    details: { schema_major: 1, ordinal: 3, template_version_id: "ver-case-v1" },
  },
  {
    id: "aud-01936a70-100",
    occurred_at: "2026-11-26T18:12:44Z",
    actor_ref: "user:sre-minh",
    actor_display: "Tran Quang Minh",
    actor_role: "PLATFORM_SRE",
    client_ip: "10.24.19.42",
    severity: "WARN",
    resource_type: "CIRCUIT_BREAKER",
    action: "BREAKER_OVERRIDE_SET",
    target_ref: "provider:vnpt-sms-brandname (HALF_OPEN)",
    summary: "Applied temporary FORCE_OPEN override during carrier maintenance.",
    command_id: "ntf-breaker-cmd-5521c",
    before_digest: "state:CLOSED",
    after_digest: "state:FORCE_OPEN;ttl_ms:300000",
    details: { override_mode: "FORCE_OPEN", ttl_ms: 300000, reason: "Carrier gateway maintenance window" },
  },
  {
    id: "aud-01936a60-099",
    occurred_at: "2026-11-26T17:42:10Z",
    actor_ref: "user:sec-tuan",
    actor_display: "Le Anh Tuan",
    actor_role: "SECURITY_OFFICER",
    client_ip: "10.24.12.7",
    severity: "CRITICAL",
    resource_type: "APPLICATION",
    action: "APPLICATION_KEY_ROTATED",
    target_ref: "app:legacy-crm-bridge",
    summary: "Rotated API key with 3600s grace period.",
    command_id: "ntf-rotate-cmd-1091f",
    before_digest: "prefix:ntf_live_11ab02",
    after_digest: "prefix:ntf_live_9f21a8",
    details: { grace_period_seconds: 3600, auth_mode: "API_KEY" },
  },
];
