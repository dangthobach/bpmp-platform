import { FileDown, FileUp } from "lucide-react";
import { useRef, useState } from "react";
import { Button } from "../../components/Button";

const MAX_SOURCE_BYTES = 1_048_576;

export function SourceFileActions({
  source,
  format,
  fileName,
  onImport,
}: {
  source: string;
  format: "bpmn" | "dmn";
  fileName: string;
  onImport: (source: string) => void;
}) {
  const input = useRef<HTMLInputElement>(null);
  const [error, setError] = useState("");

  async function readFile(file?: File) {
    if (!file) return;
    setError("");
    if (!new RegExp(`\\.(?:${format}|xml)$`, "i").test(file.name)) {
      setError(`Choose a .${format} or .xml file.`);
      return;
    }
    if (file.size === 0 || file.size > MAX_SOURCE_BYTES) {
      setError("Choose a non-empty XML file no larger than 1 MiB.");
      return;
    }
    try {
      const text = await file.text();
      if (new TextEncoder().encode(text).length > MAX_SOURCE_BYTES || !text.trimStart().startsWith("<")) {
        setError("The selected file is not a supported XML source within the 1 MiB limit.");
        return;
      }
      onImport(text);
    } catch {
      setError("The selected file could not be read.");
    } finally {
      if (input.current) input.current.value = "";
    }
  }

  function download() {
    setError("");
    const blob = new Blob([source], { type: "application/xml;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `${fileName}.${format}`;
    anchor.style.display = "none";
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    window.setTimeout(() => URL.revokeObjectURL(url), 0);
  }

  return <div className="modeler-source-file-actions">
    <input ref={input} type="file" accept={`.${format},.xml,application/xml,text/xml`} hidden onChange={(event) => void readFile(event.currentTarget.files?.[0])} />
    <Button icon={FileUp} onClick={() => input.current?.click()}>Import {format.toUpperCase()}</Button>
    <Button icon={FileDown} disabled={!source} onClick={download}>Export {format.toUpperCase()}</Button>
    {error ? <span className="form-error" role="alert">{error}</span> : null}
  </div>;
}
