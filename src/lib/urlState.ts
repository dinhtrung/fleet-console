/** List state that lives in the URL.
 *
 *  A filtered, sorted, paged view has to be shareable and survive a reload: the operator pastes the
 *  address into a handover message and the recipient sees the same board. State is read from the
 *  query string once, then written back with replaceState (so back/forward still walks the app, not
 *  every keystroke), and a popstate listener keeps it honest. */

import { useCallback, useEffect, useRef, useState } from "react";

export type ListStateValue = string | number | undefined;

function readUrl(): Record<string, string> {
  if (typeof window === "undefined") return {};
  const out: Record<string, string> = {};
  new URLSearchParams(window.location.search).forEach((value, key) => {
    out[key] = value;
  });
  return out;
}

export function useListState<T extends Record<string, ListStateValue>>(
  defaults: T,
): [T, (patch: Partial<T>, options?: { replace?: boolean }) => void] {
  const defaultsRef = useRef(defaults);
  const [state, setState] = useState<T>(() => {
    const url = readUrl();
    const initial = { ...defaults } as Record<string, ListStateValue>;
    for (const key of Object.keys(defaults)) {
      const raw = url[key];
      if (raw === undefined || raw === "") continue;
      const example = defaults[key];
      initial[key] = typeof example === "number" ? Number(raw) : raw;
    }
    initial.page = typeof initial.page === "number" && initial.page > 0 ? initial.page : 1;
    return initial as T;
  });

  const write = useCallback((next: Record<string, ListStateValue>) => {
    if (typeof window === "undefined") return;
    const params = new URLSearchParams(window.location.search);
    for (const [key, value] of Object.entries(next)) {
      const isDefault = defaultsRef.current[key] === value;
      if (value === undefined || value === "" || isDefault) params.delete(key);
      else params.set(key, String(value));
    }
    const query = params.toString();
    const url = `${window.location.pathname}${query ? `?${query}` : ""}${window.location.hash}`;
    window.history.replaceState(null, "", url);
  }, []);

  const update = useCallback(
    (patch: Partial<T>) => {
      setState((current) => {
        const next = { ...current, ...patch } as T;
        // Any change other than paging resets to the first page: page 7 of a narrower filter is
        // usually an empty board, which reads as "there is nothing here".
        if (!("page" in patch) && "page" in next) {
          (next as Record<string, ListStateValue>).page = 1;
        }
        write(next as Record<string, ListStateValue>);
        return next;
      });
    },
    [write],
  );

  useEffect(() => {
    const onPop = () => {
      const url = readUrl();
      setState((current) => {
        const next = { ...current } as Record<string, ListStateValue>;
        for (const key of Object.keys(defaultsRef.current)) {
          const raw = url[key];
          const example = (defaultsRef.current as Record<string, ListStateValue>)[key];
          next[key] = raw === undefined || raw === "" ? example : typeof example === "number" ? Number(raw) : raw;
        }
        return next as T;
      });
    };
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  return [state, update];
}
