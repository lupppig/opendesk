// Every admin API route (contracts/admin.md "Endpoints").

import { authRequest, http, setSession } from "./client";
import type {
  AdminAuditLog,
  AdminAuthResult,
  AdminCollabInput,
  AdminHealth,
  AdminJob,
  AdminJobStats,
  AdminLimits,
  AdminMe,
  AdminRole,
  AdminSummary,
  AdminUpdates,
  AdminUser,
  AdminUserRow,
  AdminWorkspace,
  AdminWorkspaceRow,
  AdminTestWorkspace,
  ChangeTestWorkspaceInput,
  CreateTestWorkspaceInput,
  InstanceAdmin,
  Page,
  SampleRequest,
  TestWorkspaceResult,
} from "./types";

const id = encodeURIComponent;

export type ListParams = { q?: string; filter?: string; cursor?: string | null; limit?: number };
export type JobParams = { state?: string; kind?: string; queue?: string; cursor?: string | null; limit?: number };
export type AuditParams = { action?: string; target_id?: string; actor_id?: string; cursor?: string | null; limit?: number };

export const api = {
  auth: {
    login: (b: { email: string; password: string; turnstile_token?: string }) => authRequest<{ mfa_token: string }>("/v1/admin/auth/login", b),
    mfa: async (b: { mfa_token: string; code: string }) => {
      const r = await authRequest<AdminAuthResult>("/v1/admin/auth/mfa", b);
      setSession(r);
      return r;
    },
    logout: async () => {
      try {
        await authRequest<void>("/v1/admin/auth/logout");
      } finally {
        setSession(null);
      }
    },
  },
  me: () => http.get<AdminMe>("/v1/admin/me"),
  summary: () => http.get<AdminSummary>("/v1/admin/summary"),
  health: () => http.get<AdminHealth>("/v1/admin/health"),
  checkUpdates: () => http.post<AdminUpdates>("/v1/admin/updates/check"),

  users: {
    list: (p: ListParams) => http.get<Page<AdminUserRow>>("/v1/admin/users", p),
    get: (uid: string) => http.get<AdminUser>(`/v1/admin/users/${id(uid)}`),
    revokeSessions: (uid: string, sessionId?: string) => http.post<void>(`/v1/admin/users/${id(uid)}/sessions/revoke`, sessionId ? { session_id: sessionId } : {}),
    resendVerification: (uid: string) => http.post<void>(`/v1/admin/users/${id(uid)}/verification-email`),
    suspend: (uid: string, reason: string) => http.post<AdminUser>(`/v1/admin/users/${id(uid)}/suspend`, { reason }),
    unsuspend: (uid: string) => http.post<AdminUser>(`/v1/admin/users/${id(uid)}/unsuspend`),
    disableMfa: (uid: string, code: string) => http.post<AdminUser>(`/v1/admin/users/${id(uid)}/mfa/disable`, { code }),
    remove: (uid: string, code: string, confirmEmail: string) => http.del(`/v1/admin/users/${id(uid)}`, { code, confirm_email: confirmEmail }),
  },

  workspaces: {
    list: (p: ListParams) => http.get<Page<AdminWorkspaceRow>>("/v1/admin/workspaces", p),
    get: (wid: string) => http.get<AdminWorkspace>(`/v1/admin/workspaces/${id(wid)}`),
    suspend: (wid: string, reason: string) => http.post<AdminWorkspace>(`/v1/admin/workspaces/${id(wid)}/suspend`, { reason }),
    unsuspend: (wid: string) => http.post<AdminWorkspace>(`/v1/admin/workspaces/${id(wid)}/unsuspend`),
    setLimits: (wid: string, limits: AdminLimits) => http.put<AdminWorkspace>(`/v1/admin/workspaces/${id(wid)}/limits`, limits),
    setCollab: (wid: string, body: AdminCollabInput) => http.put<AdminWorkspace>(`/v1/admin/workspaces/${id(wid)}/collab`, body),
    grantAi: (wid: string, amountUsd: number, note: string) => http.post<AdminWorkspace>(`/v1/admin/workspaces/${id(wid)}/ai-grants`, { amount_usd: amountUsd, note }),
    remove: (wid: string, code: string, confirmName: string) => http.del(`/v1/admin/workspaces/${id(wid)}`, { code, confirm_name: confirmName }),
  },

  testWorkspaces: {
    list: (p: { cursor?: string | null; limit?: number }) => http.get<Page<AdminTestWorkspace>>("/v1/admin/test-workspaces", p),
    create: (b: CreateTestWorkspaceInput) => http.post<TestWorkspaceResult>("/v1/admin/test-workspaces", b),
    change: (wid: string, b: ChangeTestWorkspaceInput) => http.patch<TestWorkspaceResult>(`/v1/admin/test-workspaces/${id(wid)}`, b),
    addSample: (wid: string, b: SampleRequest) => http.post<AdminTestWorkspace>(`/v1/admin/workspaces/${id(wid)}/sample-data`, b),
  },

  jobs: {
    list: (p: JobParams) => http.get<Page<AdminJob>>("/v1/admin/jobs", p),
    stats: () => http.get<AdminJobStats>("/v1/admin/jobs/stats"),
    retry: (jid: number) => http.post<AdminJob>(`/v1/admin/jobs/${jid}/retry`),
    cancel: (jid: number) => http.post<AdminJob>(`/v1/admin/jobs/${jid}/cancel`),
  },

  admins: {
    list: () => http.get<InstanceAdmin[]>("/v1/admin/admins"),
    grant: (b: { email: string; role: AdminRole; code: string }) => http.post<InstanceAdmin>("/v1/admin/admins", b),
    setRole: (uid: string, role: AdminRole, code: string) => http.patch<InstanceAdmin>(`/v1/admin/admins/${id(uid)}`, { role, code }),
    revoke: (uid: string, code: string) => http.del(`/v1/admin/admins/${id(uid)}`, { code }),
  },

  audit: (p: AuditParams) => http.get<Page<AdminAuditLog>>("/v1/admin/audit", p),
};
