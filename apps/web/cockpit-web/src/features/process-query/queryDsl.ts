export type ValueKind = "string" | "integer" | "timestamp";
export type QueryScope = "process" | "event";

export const comparisonOperators = [
  "COMPARISON_OPERATOR_EQUAL",
  "COMPARISON_OPERATOR_NOT_EQUAL",
  "COMPARISON_OPERATOR_GREATER_THAN",
  "COMPARISON_OPERATOR_GREATER_THAN_OR_EQUAL",
  "COMPARISON_OPERATOR_LESS_THAN",
  "COMPARISON_OPERATOR_LESS_THAN_OR_EQUAL",
  "COMPARISON_OPERATOR_IN",
  "COMPARISON_OPERATOR_NOT_IN",
  "COMPARISON_OPERATOR_CONTAINS",
  "COMPARISON_OPERATOR_STARTS_WITH",
  "COMPARISON_OPERATOR_ENDS_WITH",
  "COMPARISON_OPERATOR_BETWEEN",
  "COMPARISON_OPERATOR_NOT_BETWEEN",
  "COMPARISON_OPERATOR_IS_NULL",
  "COMPARISON_OPERATOR_IS_NOT_NULL",
] as const;

export type ComparisonOperator = typeof comparisonOperators[number];
export type TextTransform =
  | "TEXT_TRANSFORM_NONE"
  | "TEXT_TRANSFORM_TRIM"
  | "TEXT_TRANSFORM_LOWER"
  | "TEXT_TRANSFORM_UPPER"
  | "TEXT_TRANSFORM_TRIM_LOWER"
  | "TEXT_TRANSFORM_TRIM_UPPER";
export type NumericTransform = "NUMERIC_TRANSFORM_NONE" | "NUMERIC_TRANSFORM_ABS";
export type DateTransform =
  | "DATE_TRANSFORM_NONE"
  | "DATE_TRANSFORM_UTC_DAY"
  | "DATE_TRANSFORM_UTC_WEEK"
  | "DATE_TRANSFORM_UTC_MONTH"
  | "DATE_TRANSFORM_UTC_YEAR";

export const textTransforms: Array<{ value: TextTransform; label: string }> = [
  { value: "TEXT_TRANSFORM_NONE", label: "Original" },
  { value: "TEXT_TRANSFORM_TRIM", label: "Trim" },
  { value: "TEXT_TRANSFORM_LOWER", label: "Lowercase" },
  { value: "TEXT_TRANSFORM_UPPER", label: "Uppercase" },
  { value: "TEXT_TRANSFORM_TRIM_LOWER", label: "Trim + lowercase" },
  { value: "TEXT_TRANSFORM_TRIM_UPPER", label: "Trim + uppercase" },
];
export const numericTransforms: Array<{ value: NumericTransform; label: string }> = [
  { value: "NUMERIC_TRANSFORM_NONE", label: "Original" },
  { value: "NUMERIC_TRANSFORM_ABS", label: "Absolute value" },
];
export const dateTransforms: Array<{ value: DateTransform; label: string }> = [
  { value: "DATE_TRANSFORM_NONE", label: "Exact timestamp" },
  { value: "DATE_TRANSFORM_UTC_DAY", label: "UTC day" },
  { value: "DATE_TRANSFORM_UTC_WEEK", label: "UTC week" },
  { value: "DATE_TRANSFORM_UTC_MONTH", label: "UTC month" },
  { value: "DATE_TRANSFORM_UTC_YEAR", label: "UTC year" },
];

export interface FieldDefinition {
  value: string;
  label: string;
  kind: ValueKind;
  sortable?: boolean;
}

export const processFields: FieldDefinition[] = [
  { value: "PROCESS_FIELD_INSTANCE_ID", label: "Instance ID", kind: "string", sortable: true },
  { value: "PROCESS_FIELD_WORKFLOW_TYPE", label: "Workflow type", kind: "string", sortable: true },
  { value: "PROCESS_FIELD_WORKFLOW_VERSION", label: "Workflow version", kind: "string", sortable: true },
  { value: "PROCESS_FIELD_STATUS", label: "Status", kind: "string", sortable: true },
  { value: "PROCESS_FIELD_ACTIVE_NODE_ID", label: "Active node", kind: "string", sortable: true },
  { value: "PROCESS_FIELD_LAST_EVENT_SEQUENCE", label: "Last event sequence", kind: "integer", sortable: true },
  { value: "PROCESS_FIELD_STARTED_AT", label: "Started at", kind: "timestamp", sortable: true },
  { value: "PROCESS_FIELD_UPDATED_AT", label: "Updated at", kind: "timestamp", sortable: true },
  { value: "PROCESS_FIELD_COMPLETED_AT", label: "Completed at", kind: "timestamp", sortable: true },
  { value: "PROCESS_FIELD_DURATION_MS", label: "Duration (ms)", kind: "integer", sortable: true },
  { value: "PROCESS_FIELD_CONFIG_VERSION", label: "Config version", kind: "string", sortable: true },
  { value: "PROCESS_FIELD_POLICY_VERSION", label: "Policy version", kind: "string", sortable: true },
  { value: "PROCESS_FIELD_VERSION", label: "Projection version", kind: "integer", sortable: true },
  { value: "PROCESS_FIELD_INITIATOR_ACTOR_ID", label: "Initiator actor", kind: "string", sortable: true },
];

export const eventFields: FieldDefinition[] = [
  { value: "EVENT_FIELD_EVENT_TYPE", label: "Event type", kind: "string" },
  { value: "EVENT_FIELD_NODE_ID", label: "Node ID", kind: "string" },
  { value: "EVENT_FIELD_ACTOR_ID", label: "Actor ID", kind: "string" },
  { value: "EVENT_FIELD_SEQUENCE", label: "Event sequence", kind: "integer" },
  { value: "EVENT_FIELD_OCCURRED_AT", label: "Occurred at", kind: "timestamp" },
  { value: "EVENT_FIELD_CONFIG_VERSION", label: "Event config version", kind: "string" },
  { value: "EVENT_FIELD_POLICY_VERSION", label: "Event policy version", kind: "string" },
];

export interface PredicateNode {
  id: string;
  type: "predicate";
  scope: QueryScope;
  field: string;
  operator: ComparisonOperator;
  rawValue: string;
  rangeEnd: string;
  textTransform: TextTransform;
  numericTransform: NumericTransform;
  dateTransform: DateTransform;
  coalesceValue: string;
}

export interface GroupNode {
  id: string;
  type: "group";
  scope: QueryScope;
  operator: "LOGICAL_OPERATOR_AND" | "LOGICAL_OPERATOR_OR";
  children: QueryNode[];
}

export interface NegatedNode {
  id: string;
  type: "negated";
  scope: QueryScope;
  child: QueryNode;
}

export interface RelationNode {
  id: string;
  type: "relation";
  scope: "process";
  quantifier: "RELATION_QUANTIFIER_EXISTS" | "RELATION_QUANTIFIER_NOT_EXISTS";
  child: QueryNode;
}

export type QueryNode = PredicateNode | GroupNode | NegatedNode | RelationNode;

export interface QueryLimits {
  maxDepth: number;
  maxPredicates: number;
  maxRelations: number;
  maxInValues: number;
  maxStringBytes: number;
  maxSortFields: number;
}

// These are defensive browser limits. The tenant ProjectionPolicy remains the
// authority and may reject a stricter budget at execution time.
export const browserQueryLimits: QueryLimits = {
  maxDepth: 8,
  maxPredicates: 20,
  maxRelations: 4,
  maxInValues: 20,
  maxStringBytes: 512,
  maxSortFields: 4,
};

export function newPredicate(scope: QueryScope): PredicateNode {
  const field = (scope === "process" ? processFields : eventFields)[0]!;
  return {
    id: crypto.randomUUID(),
    type: "predicate",
    scope,
    field: field.value,
    operator: "COMPARISON_OPERATOR_EQUAL",
    rawValue: "",
    rangeEnd: "",
    textTransform: "TEXT_TRANSFORM_NONE",
    numericTransform: "NUMERIC_TRANSFORM_NONE",
    dateTransform: "DATE_TRANSFORM_NONE",
    coalesceValue: "",
  };
}

export function newGroup(scope: QueryScope): GroupNode {
  return {
    id: crypto.randomUUID(),
    type: "group",
    scope,
    operator: "LOGICAL_OPERATOR_AND",
    children: [newPredicate(scope), newPredicate(scope)],
  };
}

export function allowedOperators(kind: ValueKind): ComparisonOperator[] {
  if (kind === "string") {
    return [
      "COMPARISON_OPERATOR_EQUAL", "COMPARISON_OPERATOR_NOT_EQUAL",
      "COMPARISON_OPERATOR_IN", "COMPARISON_OPERATOR_NOT_IN",
      "COMPARISON_OPERATOR_CONTAINS", "COMPARISON_OPERATOR_STARTS_WITH",
      "COMPARISON_OPERATOR_ENDS_WITH",
      "COMPARISON_OPERATOR_IS_NULL", "COMPARISON_OPERATOR_IS_NOT_NULL",
    ];
  }
  return [
    "COMPARISON_OPERATOR_EQUAL", "COMPARISON_OPERATOR_NOT_EQUAL",
    "COMPARISON_OPERATOR_GREATER_THAN", "COMPARISON_OPERATOR_GREATER_THAN_OR_EQUAL",
    "COMPARISON_OPERATOR_LESS_THAN", "COMPARISON_OPERATOR_LESS_THAN_OR_EQUAL",
    "COMPARISON_OPERATOR_IN", "COMPARISON_OPERATOR_NOT_IN",
    "COMPARISON_OPERATOR_BETWEEN", "COMPARISON_OPERATOR_NOT_BETWEEN",
    "COMPARISON_OPERATOR_IS_NULL", "COMPARISON_OPERATOR_IS_NOT_NULL",
  ];
}

export function fieldDefinition(scope: QueryScope, field: string): FieldDefinition | undefined {
  return (scope === "process" ? processFields : eventFields).find((item) => item.value === field);
}

export function updateNode(root: QueryNode, id: string, update: (node: QueryNode) => QueryNode): QueryNode {
  if (root.id === id) return update(root);
  if (root.type === "group") {
    return { ...root, children: root.children.map((child) => updateNode(child, id, update)) };
  }
  if (root.type === "negated" || root.type === "relation") {
    return { ...root, child: updateNode(root.child, id, update) };
  }
  return root;
}

export function removeNode(root: QueryNode, id: string): QueryNode {
  if (root.type === "group") {
    return {
      ...root,
      children: root.children
        .filter((child) => child.id !== id)
        .map((child) => removeNode(child, id)),
    };
  }
  if (root.type === "negated" || root.type === "relation") {
    return { ...root, child: removeNode(root.child, id) };
  }
  return root;
}

export type WireValue = { string_value: string } | { integer_value: number } | { timestamp_epoch_ms: number };
type WireExpression = Record<string, unknown>;

export interface ValidationResult {
  expression?: WireExpression;
  errors: Record<string, string>;
  summary: { depth: number; predicates: number; relations: number };
}

export function compileQuery(root: QueryNode, limits: QueryLimits = browserQueryLimits): ValidationResult {
  const errors: Record<string, string> = {};
  const summary = { depth: 0, predicates: 0, relations: 0 };

  function compile(node: QueryNode, depth: number, expectedScope: QueryScope): WireExpression | undefined {
    summary.depth = Math.max(summary.depth, depth);
    if (depth > limits.maxDepth) errors[node.id] = `Maximum nesting depth is ${limits.maxDepth}.`;
    if (node.scope !== expectedScope) errors[node.id] = "Expression scope is invalid.";
    if (node.type === "predicate") {
      summary.predicates++;
      if (summary.predicates > limits.maxPredicates) errors[node.id] = `Maximum ${limits.maxPredicates} predicates.`;
      const definition = fieldDefinition(node.scope, node.field);
      if (!definition) {
        errors[node.id] = "Choose a supported field.";
        return undefined;
      }
      if (!allowedOperators(definition.kind).includes(node.operator)) {
        errors[node.id] = "Operator is incompatible with this field.";
        return undefined;
      }
	  if (definition.kind !== "string" && node.textTransform !== "TEXT_TRANSFORM_NONE") {
		errors[node.id] = "Text transforms require a string field.";
		return undefined;
	  }
	  if (definition.kind !== "integer" && node.numericTransform !== "NUMERIC_TRANSFORM_NONE") {
		errors[node.id] = "Numeric transforms require a numeric field.";
		return undefined;
	  }
	  if (definition.kind !== "timestamp" && node.dateTransform !== "DATE_TRANSFORM_NONE") {
		errors[node.id] = "Date transforms require a date field.";
		return undefined;
	  }
      const noValue = node.operator === "COMPARISON_OPERATOR_IS_NULL" || node.operator === "COMPARISON_OPERATOR_IS_NOT_NULL";
      const range = node.operator === "COMPARISON_OPERATOR_BETWEEN" || node.operator === "COMPARISON_OPERATOR_NOT_BETWEEN";
      const rawValues = noValue
        ? []
        : range
          ? [node.rawValue.trim(), node.rangeEnd.trim()]
          : node.operator.endsWith("_IN")
            ? node.rawValue.split(",").map((value) => value.trim())
            : [node.rawValue.trim()];
      if (rawValues.some((value) => value.length === 0)) {
        errors[node.id] = node.operator.endsWith("_IN") ? "Provide a comma-separated list without empty items." : "A value is required.";
        return undefined;
      }
      const uniqueValues = [...new Set(rawValues)];
      if (uniqueValues.length > limits.maxInValues) {
        errors[node.id] = `Maximum ${limits.maxInValues} values for IN.`;
        return undefined;
      }
      const values: WireValue[] = [];
      for (const value of uniqueValues) {
        const parsed = compileFieldValue(definition, value, limits);
        if (parsed.error) {
          errors[node.id] = parsed.error;
          return undefined;
        }
        values.push(parsed.value!);
      }
	  if (range && compareWireValues(values[0]!, values[1]!) > 0) {
		errors[node.id] = "Range start must be less than or equal to range end.";
		return undefined;
	  }
      let coalesceValue: WireValue | undefined;
      if (node.coalesceValue.trim()) {
        const parsed = compileFieldValue(definition, node.coalesceValue.trim(), limits);
        if (parsed.error) {
          errors[node.id] = `Null fallback: ${parsed.error}`;
          return undefined;
        }
        coalesceValue = parsed.value;
      }
      return {
        predicate: {
          field: node.field,
          operator: node.operator,
          values,
          text_transform: node.textTransform,
          numeric_transform: node.numericTransform,
          date_transform: node.dateTransform,
          ...(coalesceValue ? { coalesce_value: coalesceValue } : {}),
        },
      };
    }
    if (node.type === "group") {
      if (node.children.length < 2) errors[node.id] = "A group needs at least two expressions.";
      const expressions = node.children.map((child) => compile(child, depth + 1, expectedScope));
      return expressions.some((value) => !value)
        ? undefined
        : { group: { operator: node.operator, expressions } };
    }
    if (node.type === "negated") {
      const child = compile(node.child, depth + 1, expectedScope);
      return child ? { negated: child } : undefined;
    }
    summary.relations++;
    if (expectedScope !== "process") errors[node.id] = "Event relations cannot be nested inside an event relation.";
    if (summary.relations > limits.maxRelations) errors[node.id] = `Maximum ${limits.maxRelations} event relations.`;
    const child = compile(node.child, depth + 1, "event");
    return child ? { event_relation: { quantifier: node.quantifier, expression: child } } : undefined;
  }

  const expression = compile(root, 1, "process");
  return { ...(expression ? { expression } : {}), errors, summary };
}

function compareWireValues(left: WireValue, right: WireValue): number {
  const leftValue = "integer_value" in left ? left.integer_value
    : "timestamp_epoch_ms" in left ? left.timestamp_epoch_ms : left.string_value;
  const rightValue = "integer_value" in right ? right.integer_value
    : "timestamp_epoch_ms" in right ? right.timestamp_epoch_ms : right.string_value;
  return leftValue < rightValue ? -1 : leftValue > rightValue ? 1 : 0;
}

export function compileFieldValue(
  definition: FieldDefinition,
  value: string,
  limits: QueryLimits = browserQueryLimits,
): { value?: WireValue; error?: string } {
  if (definition.kind === "string") {
    if (!value || new TextEncoder().encode(value).length > limits.maxStringBytes) {
      return { error: `String values must contain 1-${limits.maxStringBytes} UTF-8 bytes.` };
    }
    return { value: { string_value: value } };
  }
  if (definition.kind === "integer") {
    const parsed = Number(value);
    return Number.isSafeInteger(parsed)
      ? { value: { integer_value: parsed } }
      : { error: "Enter a safe whole number." };
  }
  const parsed = Date.parse(value);
  return Number.isSafeInteger(parsed) && parsed > 0
    ? { value: { timestamp_epoch_ms: parsed } }
    : { error: "Enter a valid date and time after 1970-01-01." };
}

export function operatorLabel(value: string): string {
  return value
    .replace("COMPARISON_OPERATOR_", "")
    .replace("LOGICAL_OPERATOR_", "")
    .replaceAll("_", " ")
    .toLowerCase();
}
