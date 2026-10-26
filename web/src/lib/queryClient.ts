import {
  QueryClient,
  type DefaultError,
  type DefaultedQueryObserverOptions,
  type MutationOptions,
  type QueryFunction,
  type QueryKey,
  type QueryObserverOptions,
} from "@tanstack/react-query";
import { inWorkspace, isApiError, scopeWorkspace } from "./api/client";
import { keyWorkspace } from "./queries";

/**
 * A query runs in the workspace its key names, and a mutation in the workspace in
 * scope when its component rendered (the page keys itself by the focus workspace, so
 * that is the page's own). Requests and the keys the callbacks build then target it,
 * even when the focus moved to another workspace in the meantime: one workspace's
 * answer is never stored under another's key (contracts/api.md "Several workspaces").
 */
export class WorkspaceQueryClient extends QueryClient {
  override defaultQueryOptions<
    TQueryFnData = unknown,
    TError = DefaultError,
    TData = TQueryFnData,
    TQueryData = TQueryFnData,
    TQueryKey extends QueryKey = QueryKey,
    TPageParam = never,
  >(
    options:
      | QueryObserverOptions<TQueryFnData, TError, TData, TQueryData, TQueryKey, TPageParam>
      | DefaultedQueryObserverOptions<TQueryFnData, TError, TData, TQueryData, TQueryKey>,
  ): DefaultedQueryObserverOptions<TQueryFnData, TError, TData, TQueryData, TQueryKey> {
    const defaulted = super.defaultQueryOptions(options);
    const wid = keyWorkspace(defaulted.queryKey);
    const fn = defaulted.queryFn as (QueryFunction & Scoped) | undefined;
    if (wid && typeof fn === "function" && fn.__ws !== wid) {
      const scoped: QueryFunction & Scoped = (ctx) => inWorkspace(wid, () => fn(ctx));
      scoped.__ws = wid;
      defaulted.queryFn = scoped as unknown as typeof defaulted.queryFn;
    }
    return defaulted;
  }

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  override defaultMutationOptions<T extends MutationOptions<any, any, any, any>>(options?: T): T {
    const defaulted = super.defaultMutationOptions(options) as T & Scoped;
    if (!options || (options as Scoped).__ws || defaulted.__ws) return defaulted;
    const wid = scopeWorkspace();
    if (!wid) return defaulted;
    const wrap = <F extends ((...a: never[]) => unknown) | undefined>(f: F): F =>
      (f ? (...a: Parameters<NonNullable<F>>) => inWorkspace(wid, () => (f as (...x: unknown[]) => unknown)(...a)) : f) as F;
    return {
      ...defaulted,
      mutationFn: wrap(defaulted.mutationFn),
      onMutate: wrap(defaulted.onMutate),
      onSuccess: wrap(defaulted.onSuccess),
      onError: wrap(defaulted.onError),
      onSettled: wrap(defaulted.onSettled),
      __ws: wid,
    };
  }
}

type Scoped = { __ws?: string };

export const queryClient = new WorkspaceQueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      gcTime: 10 * 60_000,
      refetchOnWindowFocus: false,
      retry: (count, e) => {
        if (isApiError(e) && e.status >= 400 && e.status < 500 && e.status !== 429) return false;
        return count < 2;
      },
    },
    mutations: { retry: false },
  },
});
