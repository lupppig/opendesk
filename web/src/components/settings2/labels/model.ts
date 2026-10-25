// Labels (the API's tags) for the settings screens: label rows and groups
// from the query cache, plus optimistic writes for labels, groups, merge and
// reorder. Realtime keeps qk.tags and qk.tagGroups fresh.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo } from "react";
import { toast } from "@/components/ui/toast";
import { api, rows } from "@/lib/api/endpoints";
import type { Tag as ApiTag, TagGroup as ApiTagGroup } from "@/lib/api/types";
import { q, qk } from "@/lib/queries";
import { isTone, toneFor, type Tone } from "@/lib/tones";

export type LabelScope = "tickets" | "contacts" | "companies";
export const SCOPES: LabelScope[] = ["tickets", "contacts", "companies"];

export type Label = {
  id: string;
  name: string;
  color: Tone;
  description: string;
  group_id: string | null;
  position: number;
  archived: boolean;
  applies_to: LabelScope[];
  usage: { tickets: number; contacts: number; companies: number } | null;
};

export type LabelGroup = { id: string; name: string; color: Tone | null; exclusive: boolean; position: number; tag_count: number };

/** Which parts of the label model the page shows. Every part is supported now; kept so rows can be reused read-only. */
export type LabelCaps = { colors: boolean; describe: boolean; scope: boolean; archive: boolean; usage: boolean; groups: boolean; merge: boolean; reorder: boolean };
const CAPS: LabelCaps = { colors: true, describe: true, scope: true, archive: true, usage: true, groups: true, merge: true, reorder: true };

export type LabelInput = { name: string; color?: Tone; description?: string; group_id?: string | null; applies_to?: LabelScope[]; archived?: boolean };

function normalize(t: ApiTag): Label {
  return {
    id: t.id,
    name: t.name,
    color: isTone(t.color) ? t.color : toneFor(t.name),
    description: t.description ?? "",
    group_id: t.group_id ?? null,
    position: t.position ?? 0,
    archived: !!t.archived,
    applies_to: t.applies_to?.length ? t.applies_to : SCOPES,
    usage: t.usage ?? null,
  };
}

function normalizeGroup(g: ApiTagGroup): LabelGroup {
  return { id: g.id, name: g.name, color: g.color && isTone(g.color) ? g.color : null, exclusive: g.exclusive, position: g.position, tag_count: g.tag_count };
}

export function useLabels() {
  const tags = useQuery(q.tags());
  const tagGroups = useQuery(q.tagGroups());
  return useMemo(() => {
    const labels = (tags.data ?? []).map(normalize).sort((a, b) => a.position - b.position || a.name.localeCompare(b.name));
    const groups = (tagGroups.data ?? []).map(normalizeGroup).sort((a, b) => a.position - b.position);
    return { labels, groups, caps: CAPS, isLoading: tags.isLoading || tagGroups.isLoading };
  }, [tags.data, tags.isLoading, tagGroups.data, tagGroups.isLoading]);
}

export function useLabelWrites(_caps: LabelCaps = CAPS) {
  const qc = useQueryClient();
  const key = qk.tags;
  const patchList = (fn: (rows: ApiTag[]) => ApiTag[]) => {
    const prev = qc.getQueryData<ApiTag[]>(key);
    if (prev) qc.setQueryData(key, fn(prev));
    return prev;
  };
  const settle = () => {
    void qc.invalidateQueries({ queryKey: key });
    void qc.invalidateQueries({ queryKey: qk.tagGroups });
  };

  const create = useMutation({
    mutationFn: (v: LabelInput) => api.tags.create(v),
    onSuccess: (t) => {
      qc.setQueryData<ApiTag[]>(key, (old) => (old ? [...old.filter((x) => x.id !== t.id), t] : old));
    },
    onSettled: settle,
  });

  const update = useMutation({
    mutationFn: ({ id, ...v }: Partial<LabelInput> & { id: string }) => api.tags.update(id, v),
    onMutate: async ({ id, ...v }) => {
      await qc.cancelQueries({ queryKey: key });
      return { prev: patchList((list) => list.map((t) => (t.id === id ? { ...t, ...v } : t))) };
    },
    onError: (e, _v, ctx) => {
      if (ctx?.prev) qc.setQueryData(key, ctx.prev);
      toast.error(e, "Couldn't save the label");
    },
    onSettled: settle,
  });

  const remove = useMutation({
    mutationFn: (id: string) => api.tags.remove(id),
    onMutate: async (id) => {
      await qc.cancelQueries({ queryKey: key });
      return { prev: patchList((list) => list.filter((t) => t.id !== id)) };
    },
    onError: (e, _v, ctx) => {
      if (ctx?.prev) qc.setQueryData(key, ctx.prev);
      toast.error(e, "Couldn't delete the label");
    },
    onSettled: settle,
  });

  return { create, update, remove };
}

/** Group, merge and reorder writes, optimistic where the result is known. */
export function useLabelStructureWrites() {
  const qc = useQueryClient();
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: qk.tags });
    void qc.invalidateQueries({ queryKey: qk.tagGroups });
  };
  return {
    reorder: async (ids: string[]) => {
      const prev = qc.getQueryData<ApiTag[]>(qk.tags);
      if (prev) {
        const pos = new Map(ids.map((id, i) => [id, i]));
        qc.setQueryData<ApiTag[]>(qk.tags, prev.map((t) => (pos.has(t.id) ? { ...t, position: pos.get(t.id)! } : t)));
      }
      try {
        const res = rows(await api.tags.reorder(ids));
        if (res.length && prev) {
          const byId = new Map(res.map((t) => [t.id, t]));
          qc.setQueryData<ApiTag[]>(qk.tags, (old) => old?.map((t) => byId.get(t.id) ?? t));
        }
      } catch (e) {
        if (prev) qc.setQueryData(qk.tags, prev);
        toast.error(e, "Couldn't reorder the labels");
      } finally {
        refresh();
      }
    },
    merge: async (id: string, into: string) => {
      await api.tags.merge(id, into);
      qc.setQueryData<ApiTag[]>(qk.tags, (old) => old?.filter((t) => t.id !== id));
      refresh();
      void qc.invalidateQueries({ queryKey: qk.of("tickets") });
    },
    saveGroup: async (id: string | null, v: { name: string; color: Tone | null; exclusive: boolean }) => {
      const g = id ? await api.tagGroups.update(id, v) : await api.tagGroups.create(v);
      qc.setQueryData<ApiTagGroup[]>(qk.tagGroups, (old) => (old ? [...old.filter((x) => x.id !== g.id), g] : old));
      refresh();
      return g;
    },
    removeGroup: async (id: string) => {
      const prev = qc.getQueryData<ApiTagGroup[]>(qk.tagGroups);
      qc.setQueryData<ApiTagGroup[]>(qk.tagGroups, (old) => old?.filter((g) => g.id !== id));
      qc.setQueryData<ApiTag[]>(qk.tags, (old) => old?.map((t) => (t.group_id === id ? { ...t, group_id: null } : t)));
      try {
        await api.tagGroups.remove(id);
      } catch (e) {
        if (prev) qc.setQueryData(qk.tagGroups, prev);
        throw e;
      } finally {
        refresh();
      }
    },
  };
}

export const scopeOf = (l: Label, s: LabelScope) => l.applies_to.includes(s);
export const totalUsage = (l: Label) => (l.usage ? l.usage.tickets + l.usage.contacts + l.usage.companies : 0);
