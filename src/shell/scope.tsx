/** The board's scope: one organization, or the whole board.
 *
 *  Scope is deliberately global rather than per-screen: on a handover board the question "whose
 *  board am I reading" is answered once and applies everywhere, and every count, list and record
 *  re-scopes with it. */

import { createContext, useCallback, useContext, useMemo, useState } from "react";
import type { ReactNode } from "react";

const SCOPE_KEY = "fleet-console.scope";

interface ScopeValue {
  orgId: string | null;
  setOrgId: (orgId: string | null) => void;
  /** Bumped whenever the scope changes, so lists can run the handover sweep once per change. */
  sweepKey: number;
}

const ScopeContext = createContext<ScopeValue | null>(null);

export function ScopeProvider({ children }: { children: ReactNode }) {
  const [orgId, setOrgIdState] = useState<string | null>(() => {
    try {
      return localStorage.getItem(SCOPE_KEY) || null;
    } catch {
      return null;
    }
  });
  const [sweepKey, setSweepKey] = useState(0);

  const setOrgId = useCallback((next: string | null) => {
    setOrgIdState(next);
    setSweepKey((k) => k + 1);
    try {
      if (next) localStorage.setItem(SCOPE_KEY, next);
      else localStorage.removeItem(SCOPE_KEY);
    } catch {
      // storage unavailable: scope still applies for this session
    }
  }, []);

  const value = useMemo(() => ({ orgId, setOrgId, sweepKey }), [orgId, setOrgId, sweepKey]);
  return <ScopeContext.Provider value={value}>{children}</ScopeContext.Provider>;
}

export function useScope(): ScopeValue {
  const ctx = useContext(ScopeContext);
  if (!ctx) throw new Error("useScope must be used inside ScopeProvider");
  return ctx;
}
