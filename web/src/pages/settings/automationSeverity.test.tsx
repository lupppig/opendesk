import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, type ComponentProps } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ConfirmProvider } from "@/components/ui/confirm";
import { TooltipProvider } from "@/components/ui/tooltip";
import { useSession } from "@/lib/session";
import { qk } from "@/lib/queries";
import { installMock } from "@/mock";
import { db } from "@/mock/db";
import { setWorkspaceId } from "@/lib/api/client";
import { i18n } from "@/i18n";
import AutomationEdit from "@/pages/settings/AutomationEdit";

vi.mock("@/components/settings2/UnsavedGuard", () => ({ UnsavedGuard: () => null }));
vi.mock("@tanstack/react-router", async (load) => ({
 ...await load<typeof import("@tanstack/react-router")>(),
 Link: ({to,children,...props}:ComponentProps<"a"> & {to:string})=><a href={to} {...props}>{children}</a>,
 useNavigate:()=>vi.fn(),useParams:()=>({automationId:"new"}),
}));
vi.stubGlobal("ResizeObserver", class { observe(){} unobserve(){} disconnect(){} });
Object.defineProperty(HTMLElement.prototype,"scrollIntoView",{configurable:true,value(){}});
let root:Root;let el:HTMLDivElement;let qc:QueryClient;
const ws=()=>db.workspaces[0];
beforeEach(async()=>{
 installMock();await i18n.changeLanguage("en");setWorkspaceId(ws().workspace.id);
 useSession.setState({status:"authed",workspaceId:ws().workspace.id,memberships:[{workspace:ws().workspace,role:"owner"}]});
 qc=new QueryClient({defaultOptions:{queries:{retry:false,staleTime:Infinity},mutations:{retry:false}}});
 qc.setQueryData(qk.automations,[]);qc.setQueryData(qk.teams,[]);qc.setQueryData(qk.brands,[]);qc.setQueryData(qk.phoneConnections,[]);qc.setQueryData(qk.customFields,[]);
 el=document.createElement("div");document.body.append(el);root=createRoot(el);
});
afterEach(async()=>{await act(async()=>root.unmount());el.remove();qc.clear();setWorkspaceId(null);useSession.setState({memberships:[],workspaceId:null,user:null});});
async function fields(severity:boolean){
 qc.setQueryData(qk.workspace,{...ws().workspace,settings:{...ws().workspace.settings,severity_enabled:severity}});
 await act(async()=>root.render(<QueryClientProvider client={qc}><TooltipProvider><ConfirmProvider><AutomationEdit/></ConfirmProvider></TooltipProvider></QueryClientProvider>));
 const picker=[...el.querySelectorAll("button")].find(b=>b.textContent?.trim()==="Subject");expect(picker).toBeDefined();
 await act(async()=>picker?.click());
 return [...document.querySelectorAll("[cmdk-item]")].map(e=>e.textContent);
}
it("keeps severity out of shared automation conditions when disabled, with time and call fields still excluded",async()=>{
 const values=await fields(false);expect(values).toContain("Subject");expect(values).not.toContain("Severity");expect(values).not.toContain("Business hours");expect(values).not.toContain("Call outcome");
});
it("offers severity in the same shared picker when enabled",async()=>{
 const values=await fields(true);expect(values).toContain("Severity");expect(values).not.toContain("Business hours");
});
