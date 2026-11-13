import { ArrowDown, ArrowUp } from "lucide-react";
import { Button } from "../../components/Button";

interface JSONSchemaField {
  type?: string;
  enum?: unknown[];
}

interface JSONSchemaDocument {
  properties?: Record<string, JSONSchemaField>;
}

interface UIField {
  widget?: "default" | "textarea";
  width?: "full" | "half";
  placeholder?: string;
  help_text?: string;
}

interface UISchemaDocument {
  order?: string[];
  fields?: Record<string, UIField>;
}

export function FormUISchemaEditor({
  schemaJSON,
  uiSchemaJSON,
  onChange,
}: {
  schemaJSON: string;
  uiSchemaJSON: string;
  onChange: (value: string) => void;
}) {
  let schema: JSONSchemaDocument;
  let uiSchema: UISchemaDocument;
  try {
    schema = JSON.parse(schemaJSON) as JSONSchemaDocument;
    uiSchema = JSON.parse(uiSchemaJSON || "{}") as UISchemaDocument;
  } catch {
    return <p className="field__hint" role="status">Enter valid JSON Schema and UI schema JSON to configure fields visually.</p>;
  }
  if (!schema.properties || !uiSchema || typeof uiSchema !== "object" || Array.isArray(uiSchema)) {
    return <p className="field__hint" role="status">Visual profile editor requires a JSON Schema properties object and a UI schema object.</p>;
  }

  const fieldNames = Object.keys(schema.properties);
  const order = [
    ...new Set([
      ...(uiSchema.order ?? []).filter((name) => fieldNames.includes(name)),
      ...fieldNames,
    ]),
  ];

  function commit(nextOrder: string[], fields: Record<string, UIField>) {
    onChange(JSON.stringify({ order: nextOrder, fields }, null, 2));
  }

  function updateField(name: string, update: Partial<UIField>) {
    const fields = { ...(uiSchema.fields ?? {}) };
    const current = { ...(fields[name] ?? {}), ...update };
    for (const key of Object.keys(current) as Array<keyof UIField>) {
      if (current[key] === "" || current[key] === "default" || current[key] === "half") delete current[key];
    }
    if (Object.keys(current).length) fields[name] = current;
    else delete fields[name];
    commit(order, fields);
  }

  function move(name: string, direction: -1 | 1) {
    const index = order.indexOf(name);
    const target = index + direction;
    if (target < 0 || target >= order.length) return;
    const nextOrder = [...order];
    const currentName = nextOrder[index];
    const targetName = nextOrder[target];
    if (!currentName || !targetName) return;
    nextOrder[index] = targetName;
    nextOrder[target] = currentName;
    commit(nextOrder, uiSchema.fields ?? {});
  }

  return (
    <section className="form-ui-profile" aria-label="Visual form presentation settings">
      <header className="portal-section-heading">
        <div><h3>Field presentation</h3><p>These settings affect presentation only. The published JSON Schema remains authoritative for submitted values.</p></div>
      </header>
      <div className="form-ui-profile__list">
        {order.map((name, index) => {
          const field = schema.properties?.[name];
          if (!field) return null;
          const config = uiSchema.fields?.[name] ?? {};
          const canUseTextarea = field.type === "string" && !field.enum?.length;
          return (
            <article className="form-ui-profile__field" key={name}>
              <div className="form-ui-profile__title">
                <strong>{name}</strong><span>{field.type ?? "unspecified"}{field.enum?.length ? " · enum" : ""}</span>
                <div className="form-ui-profile__order">
                  <Button type="button" aria-label={`Move ${name} up`} disabled={index === 0} onClick={() => move(name, -1)}><ArrowUp size={14} /> Up</Button>
                  <Button type="button" aria-label={`Move ${name} down`} disabled={index === order.length - 1} onClick={() => move(name, 1)}><ArrowDown size={14} /> Down</Button>
                </div>
              </div>
              <div className="form-ui-profile__controls">
                <label>Widget<select value={config.widget ?? "default"} onChange={(event) => updateField(name, { widget: event.target.value as NonNullable<UIField["widget"]> })}>
                  <option value="default">Default</option>
                  {canUseTextarea ? <option value="textarea">Textarea</option> : null}
                </select></label>
                <label>Width<select value={config.width ?? "half"} onChange={(event) => updateField(name, { width: event.target.value as NonNullable<UIField["width"]> })}><option value="half">Half</option><option value="full">Full</option></select></label>
                <label>Placeholder<input maxLength={256} value={config.placeholder ?? ""} onChange={(event) => updateField(name, { placeholder: event.target.value })} /></label>
                <label>Help text<input maxLength={512} value={config.help_text ?? ""} onChange={(event) => updateField(name, { help_text: event.target.value })} /></label>
              </div>
            </article>
          );
        })}
      </div>
    </section>
  );
}
