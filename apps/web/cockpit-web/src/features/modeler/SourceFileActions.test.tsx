// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SourceFileActions } from "./SourceFileActions";

afterEach(()=>{cleanup();vi.restoreAllMocks();vi.unstubAllGlobals()});

describe("Modeler source file actions",()=>{
  it("imports an XML file into the current local draft",async()=>{
    const onImport=vi.fn();render(<SourceFileActions source="<definitions/>" format="bpmn" fileName="generic" onImport={onImport}/>);
    const input=document.querySelector('input[type="file"]')!;const file=new File(["<definitions id=\"uploaded\"/>"] ,"upload.bpmn",{type:"application/xml"});fireEvent.change(input,{target:{files:[file]}});
    await waitFor(()=>expect(onImport).toHaveBeenCalledWith("<definitions id=\"uploaded\"/>"));
  });

  it("downloads the exact current draft source with the format extension",()=>{
    vi.stubGlobal("URL",{...URL,createObjectURL:vi.fn(()=>"blob:dmn"),revokeObjectURL:vi.fn()});
    const click=vi.spyOn(HTMLAnchorElement.prototype,"click").mockImplementation(function(this:HTMLAnchorElement){expect(this.download).toBe("generic.decision.dmn");expect(this.href).toBe("blob:dmn")});
    render(<SourceFileActions source={'<definitions id="live"/>'} format="dmn" fileName="generic.decision" onImport={()=>{}}/>);
    fireEvent.click(screen.getByRole("button",{name:"Export DMN"}));expect(click).toHaveBeenCalledOnce();
  });

  it("rejects an unsupported file extension without changing the draft",async()=>{
    const onImport=vi.fn();render(<SourceFileActions source="original" format="dmn" fileName="generic" onImport={onImport}/>);
    fireEvent.change(document.querySelector('input[type="file"]')!,{target:{files:[new File(["<definitions/>"] ,"wrong.txt")]}});
    expect((await screen.findByRole("alert")).textContent).toContain("Choose a .dmn or .xml file");expect(onImport).not.toHaveBeenCalled();
  });
});
