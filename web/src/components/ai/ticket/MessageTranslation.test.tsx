import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { expect, it, vi } from 'vitest';
import type { Message, Ticket } from '@/lib/api/types';
import { q } from '@/lib/queries';
import { i18n } from '@/i18n';
import { useMessageTranslation } from './MessageAi';

vi.mock('@/lib/ai',()=>({useAI:()=>({enabled:true})}));
vi.mock('@/lib/session',async(importOriginal)=>({...await importOriginal<typeof import('@/lib/session')>(),useRole:()=> 'agent'}));

it.each([null,'und','zxx','en','fr'])('offers translation unless the source language %s is known to match the reader',async language=>{
 vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT',true);
 await i18n.changeLanguage('en');
 const qc=new QueryClient({defaultOptions:{queries:{retry:false}}});
 qc.setQueryData<Ticket>(q.ticket("t").queryKey, {
    id: "t",
    number: 12679,
    subject: "Bestelling #194799",
    status: "open",
    priority: "high",
    severity: null,
    channel: "email",
    mailbox_id: "mb1",
    contact: null,
    assignee: null,
    team_id: "team1",
    brand_id: "b1",
    widget_id: null,
    origin_mailbox_id: "mb1",
    received_at: "support@margin.test",
    page_url: "",
    routed: null,
    required_skills: [],
    tags: [],
    fields: {},
    sla: null,
    first_responded_at: null,
    solved_at: null,
    snoozed_until: null,
    last_message_at: "2026-10-28T09:00:00Z",
    last_customer_message_at: null,
    waiting_since: null,
    unanswered_count: 0,
    last_message_preview: "",
    message_count: 1,
    spam: false,
    csat: null,
    custom: { language },
    created_at: "2026-10-28T09:00:00Z",
    updated_at: "2026-10-28T09:00:00Z",
  });
 const m={id:'m',ticket_id:'t',kind:'reply',direction:'inbound',body_html:'<p>Hello?</p>',body_text:'Hello?'} as Message;
 const host=document.createElement('div');document.body.append(host);const root=createRoot(host);
 function Probe(){const tr=useMessageTranslation(m);return <span data-offered={tr.offered} />}
 try{
  await act(async()=>root.render(<QueryClientProvider client={qc}><Probe /></QueryClientProvider>));
  expect(host.querySelector('span')?.getAttribute('data-offered')).toBe(String(language!=='en'));
 }finally{await act(async()=>root.unmount());host.remove();qc.clear();vi.unstubAllGlobals()}
});
