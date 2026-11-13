import { useQueries, useQuery } from "@tanstack/react-query";
import { ArrowLeft, ArrowRight, Download, Eye, GitCompareArrows, History, RotateCcw } from "lucide-react";
import { useMemo, useState } from "react";
import { Button } from "../../components/Button";
import { EmptyState, ErrorState, LoadingRows } from "../../components/Feedback";

export interface VersionHistoryItem {
  id: string;
  version_num: number;
  source_revision: number;
  created_by: string;
  created_at_epoch_ms: number;
  version_tag: string;
  summary: string;
}

interface VersionSource extends VersionHistoryItem { source: string }
interface DiffLine { kind: "same" | "add" | "remove"; text: string }

function compareSourceLines(base: string, target: string): DiffLine[] | null {
  if (new TextEncoder().encode(base).length + new TextEncoder().encode(target).length > 262_144) return null;
  const left = base.split(/\r?\n/); const right = target.split(/\r?\n/);
  if (left.length > 3000 || right.length > 3000 || left.length * right.length > 1_500_000) return null;
  const width = right.length + 1; const table = new Uint32Array((left.length + 1) * width);
  for (let i=left.length-1;i>=0;i--) for(let j=right.length-1;j>=0;j--) {
    table[i*width+j]=left[i]===right[j]?(table[(i+1)*width+j+1]??0)+1:Math.max(table[(i+1)*width+j]??0,table[i*width+j+1]??0);
  }
  const result:DiffLine[]=[];let i=0,j=0;
  while(i<left.length||j<right.length){
    if(i<left.length&&j<right.length&&left[i]===right[j]){result.push({kind:"same",text:left[i]??""});i++;j++;}
    else if(i<left.length&&(j>=right.length||(table[(i+1)*width+j]??0)>=(table[i*width+j+1]??0))){result.push({kind:"remove",text:left[i++]??""});}
    else result.push({kind:"add",text:right[j++]??""});
  }
  return result;
}

interface VersionHistoryPanelProps {
  tenantId: string;
  modelId: string;
  modelKey: string;
  format: "bpmn" | "dmn";
  listVersions: (pageToken: string) => Promise<{ versions: VersionHistoryItem[]; next_page_token: string }>;
  getVersion: (versionId: string) => Promise<VersionSource>;
  hasUnsavedEditorChanges: boolean;
  onLoadIntoEditor: (source: string, versionId: string) => void;
}

function downloadSource(filename: string, source: string) {
  const url = URL.createObjectURL(new Blob([source], { type: "application/xml;charset=utf-8" }));
  const link = document.createElement("a");
  link.href = url;
  link.download = filename.replace(/[\\/<>:"|?*\u0000-\u001f]/g, "_");
  link.click();
  window.setTimeout(() => URL.revokeObjectURL(url), 0);
}

export function VersionHistoryPanel({ tenantId, modelId, modelKey, format, listVersions, getVersion, hasUnsavedEditorChanges, onLoadIntoEditor }: VersionHistoryPanelProps) {
  const [pageTokens, setPageTokens] = useState([""]);
  const [selectedVersionId, setSelectedVersionId] = useState("");
  const [loadedVersionId, setLoadedVersionId] = useState("");
  const [compareIds, setCompareIds] = useState<string[]>([]);
  const pageToken = pageTokens.at(-1) ?? "";
  const page = useQuery({
    queryKey: ["authoring-version-history", format, tenantId, modelId, pageToken],
    queryFn: () => listVersions(pageToken),
    enabled: Boolean(tenantId && modelId),
  });
  const detail = useQuery({
    queryKey: ["authoring-version", format, tenantId, modelId, selectedVersionId],
    queryFn: () => getVersion(selectedVersionId),
    enabled: Boolean(tenantId && modelId && selectedVersionId),
  });
  const compareQueries=useQueries({queries:compareIds.map(versionId=>({queryKey:["authoring-version",format,tenantId,modelId,versionId],queryFn:()=>getVersion(versionId),enabled:Boolean(tenantId&&modelId&&compareIds.length===2)}))});
  const baseCompareVersion=compareQueries[0]?.data; const targetCompareVersion=compareQueries[1]?.data;
  const compareDiff=useMemo(()=>baseCompareVersion&&targetCompareVersion?compareSourceLines(baseCompareVersion.source,targetCompareVersion.source):null,[baseCompareVersion?.source,targetCompareVersion?.source]);
  const compareStats=compareDiff?.reduce((stats,line)=>{stats[line.kind]++;return stats;},{same:0,add:0,remove:0})??null;
  const version = detail.data;
  return <section className="table-surface modeler-version-history" aria-label={`${format.toUpperCase()} version history`}>
    <header className="portal-section-heading"><div><h3><History size={16} /> Version history</h3><p>Immutable checked-in {format.toUpperCase()} source snapshots</p></div></header>
    {page.isPending ? <LoadingRows /> : null}
    {page.isError ? <ErrorState message={page.error.message} retry={() => void page.refetch()} /> : null}
    {page.isSuccess && page.data.versions.length === 0 ? <EmptyState title="No checked-in versions" detail="Validate and check in a saved draft to create the first immutable version." /> : null}
    {page.data?.versions.length ? <div className="table-scroll"><table><thead><tr><th>Version</th><th>Source revision</th><th>Tag</th><th>Checked in</th><th>Summary</th><th>Actions</th></tr></thead><tbody>
      {page.data.versions.map(item => <tr key={item.id}><td>v{item.version_num}</td><td>{item.source_revision}</td><td><code>{item.version_tag}</code></td><td>{item.created_at_epoch_ms ? new Date(item.created_at_epoch_ms).toLocaleString() : "—"}</td><td>{item.summary}</td><td><div className="modeler-version-preview__actions"><Button icon={Eye} onClick={() => { setSelectedVersionId(item.id); setLoadedVersionId(""); }}>{selectedVersionId === item.id ? "Selected" : "View source"}</Button><Button icon={GitCompareArrows} disabled={!compareIds.includes(item.id)&&compareIds.length===2} onClick={()=>setCompareIds(ids=>ids.includes(item.id)?ids.filter(id=>id!==item.id):[...ids,item.id])}>{compareIds.includes(item.id)?`Compare ${compareIds.indexOf(item.id)===0?"base":"target"}`:"Compare"}</Button></div></td></tr>)}
    </tbody></table></div> : null}
    {compareIds.length===2?<section className="modeler-version-compare" aria-label="Compare immutable versions"><header><div><h4>Version comparison</h4><p>Base v{compareQueries[0]?.data?.version_num??"…"} → Target v{compareQueries[1]?.data?.version_num??"…"}</p></div><Button onClick={()=>setCompareIds([])}>Clear comparison</Button></header>
      {compareQueries.some(query=>query.isPending)?<LoadingRows/>:null}{compareQueries.some(query=>query.isError)?<ErrorState message="Unable to load both immutable versions." retry={()=>compareQueries.forEach(query=>void query.refetch())}/>:null}
      {compareQueries.length===2&&compareQueries.every(query=>query.data)?compareDiff?<><div className="modeler-version-compare__stats"><span>+{compareStats?.add} added</span><span>−{compareStats?.remove} removed</span><span>{compareStats?.same} unchanged</span></div><div className="modeler-version-compare__diff" role="list" aria-label="Line diff">{compareDiff.map((line,index)=><div role="listitem" className={`modeler-version-compare__line is-${line.kind}`} key={`${line.kind}-${index}`}><span>{line.kind==="add"?"+":line.kind==="remove"?"−":" "}</span><code>{line.text||" "}</code></div>)}</div></>:<div className="modeler-version-compare__fallback"><p>These source files are too large or complex for an inline line diff. Review the immutable sources side by side.</p><div><textarea aria-label="Base version source" readOnly spellCheck={false} value={compareQueries[0]!.data!.source}/><textarea aria-label="Target version source" readOnly spellCheck={false} value={compareQueries[1]!.data!.source}/></div></div>:null}
    </section>:null}
    {page.data ? <footer className="pagination"><span>History page {pageTokens.length}</span><div>
      <Button icon={ArrowLeft} disabled={pageTokens.length === 1 || page.isFetching} onClick={() => setPageTokens(tokens => tokens.slice(0, -1))}>Previous</Button>
      <Button icon={ArrowRight} disabled={!page.data.next_page_token || page.isFetching} onClick={() => setPageTokens(tokens => [...tokens, page.data.next_page_token])}>Next</Button>
    </div></footer> : null}
    {selectedVersionId ? <section className="modeler-version-preview" aria-label="Selected immutable version">
      {detail.isPending ? <LoadingRows /> : null}
      {detail.isError ? <ErrorState message={detail.error.message} retry={() => void detail.refetch()} /> : null}
      {version ? <><header><div><strong>v{version.version_num} · {version.version_tag}</strong><span>Revision {version.source_revision} · {version.created_at_epoch_ms ? new Date(version.created_at_epoch_ms).toLocaleString() : "—"}</span></div><div className="modeler-version-preview__actions"><Button icon={RotateCcw} onClick={() => { if (hasUnsavedEditorChanges && !window.confirm("Replace the current unsaved editor content with this version? The saved draft is not changed until you choose Save draft.")) return; onLoadIntoEditor(version.source, version.id); setLoadedVersionId(version.id); }}>Load as draft</Button><Button icon={Download} onClick={() => downloadSource(`${modelKey}-v${version.version_num}.${format}`, version.source)}>Download source</Button></div></header><p>{version.summary}</p>{loadedVersionId===version.id?<p className="success-message" role="status">Version source loaded into the editor only. Save draft to create a new revision; the immutable version remains unchanged.</p>:null}<textarea aria-label="Immutable version source" readOnly spellCheck={false} value={version.source} /></> : null}
    </section> : null}
  </section>;
}
