/** The shell: a dark rail, a ruled top bar, and the board.
 *
 *  The rail is the one place the palette commits to a surface. Navigation, controls and layout stay
 *  the standard web ones — the world lends type, palette, density and the magnet, nothing else.
 *  Below the large breakpoint the rail becomes an overlay drawer, and closing it returns focus to
 *  the control that opened it. */

import { useCallback, useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { Link, useRouterState } from "@tanstack/react-router";
import { Menu as MenuIcon, X } from "lucide-react";
import { useAuth } from "../auth/session";
import { useAccess } from "../access/access";
import { cn } from "../lib/cn";
import { initials } from "../lib/format";
import { IconButton } from "../components/ui";
import { AppearanceControl } from "./AppearanceControl";
import { BoardControls } from "./BoardControls";
import { ScopeSwitcher } from "./ScopeSwitcher";

interface NavItem {
  to: string;
  label: string;
  permission: string;
}

const NAV: { group: string; items: NavItem[] }[] = [
  {
    group: "Board",
    items: [{ to: "/", label: "Overview", permission: "dashboard.read" }],
  },
  {
    group: "People",
    items: [
      { to: "/users", label: "Users", permission: "users.read" },
      { to: "/roles", label: "Roles", permission: "roles.read" },
    ],
  },
  {
    group: "Access",
    items: [
      { to: "/organizations", label: "Organizations", permission: "orgs.read" },
      { to: "/api-keys", label: "API keys", permission: "apikeys.read" },
    ],
  },
  {
    group: "Security",
    items: [
      { to: "/sessions", label: "Sessions", permission: "sessions.read" },
      { to: "/audit", label: "Audit record", permission: "audit.read" },
    ],
  },
  {
    group: "Workspace",
    items: [
      { to: "/feature-flags", label: "Feature flags", permission: "flags.read" },
      { to: "/settings", label: "Settings", permission: "settings.write" },
    ],
  },
];

function RailNav({ onNavigate }: { onNavigate?: () => void }) {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const { can } = useAccess();

  return (
    <nav aria-label="Board sections" className="flex flex-col gap-5">
      {NAV.map((section) => {
        const visible = section.items.filter((item) => can(item.permission));
        if (visible.length === 0) return null;
        return (
          <div key={section.group}>
            <p className="label mb-1.5 px-3 text-rail-ink/60">{section.group}</p>
            <ul>
              {visible.map((item) => {
                const active = item.to === "/" ? pathname === "/" : pathname.startsWith(item.to);
                return (
                  <li key={item.to}>
                    <Link
                      to={item.to}
                      onClick={onNavigate}
                      aria-current={active ? "page" : undefined}
                      className={cn(
                        "flex min-h-9 items-center gap-2 py-1.5 pr-3 pl-3 text-body transition-colors",
                        active
                          ? "bg-rail-ink/14 font-semibold text-rail-ink"
                          : "text-rail-ink/75 hover:bg-rail-ink/8 hover:text-rail-ink",
                      )}
                    >
                      {/* The active destination carries a filled slot marker, the way a token sits in a
                          board slot — never a coloured edge on the row. */}
                      <span
                        aria-hidden="true"
                        className={cn(
                          "h-3.5 w-1 shrink-0 rounded-[1px]",
                          active ? "bg-rail-ink" : "bg-transparent",
                        )}
                      />
                      {item.label}
                    </Link>
                  </li>
                );
              })}
            </ul>
          </div>
        );
      })}
    </nav>
  );
}

function IdentityBlock() {
  const { identity, signOut } = useAuth();
  if (!identity) return null;
  return (
    <div className="border-t border-rail-ink/20 px-3 py-3">
      <div className="flex items-center gap-2">
        <span
          aria-hidden="true"
          className="flex size-7 shrink-0 items-center justify-center rounded-chip bg-rail-ink font-mono text-[0.6875rem] font-semibold text-rail"
        >
          {initials(identity.name)}
        </span>
        <span className="min-w-0">
          <span className="block truncate text-[0.6875rem] font-semibold text-rail-ink">{identity.name}</span>
          <span className="block truncate font-mono text-[0.625rem] text-rail-ink/65">{identity.email}</span>
        </span>
      </div>
      <div className="mt-2.5 flex items-center justify-between gap-2">
        <AppearanceControl compact />
        <button
          type="button"
          onClick={() => signOut("user")}
          className="relative text-[0.6875rem] font-semibold text-rail-ink/80 underline decoration-rail-ink/40 hover:text-rail-ink after:absolute after:-inset-3 after:content-['']"
        >
          Sign out
        </button>
      </div>
    </div>
  );
}

function RailBody({ onNavigate }: { onNavigate?: () => void }) {
  return (
    <div className="flex h-full flex-col bg-rail text-rail-ink">
      <div className="flex items-center gap-2 border-b border-rail-ink/20 px-3 py-2.5">
        <span className="text-lead font-semibold tracking-[-0.02em]">fleet-console</span>
        <span className="ml-auto flex items-center gap-1">
          <BoardControls />
          {onNavigate ? (
            <IconButton label="Close navigation" onClick={onNavigate} className="text-rail-ink hover:bg-rail-ink/15 lg:hidden">
              <X size={14} />
            </IconButton>
          ) : null}
        </span>
      </div>
      <div className="border-b border-rail-ink/20 px-3 py-2">
        <ScopeSwitcher />
      </div>
      <div className="flex-1 overflow-y-auto py-3">
        <RailNav onNavigate={onNavigate} />
      </div>
      <IdentityBlock />
    </div>
  );
}

export function AppShell({ children }: { children: ReactNode }) {
  const [drawerOpen, setDrawerOpen] = useState(false);
  const drawerTrigger = useRef<HTMLButtonElement>(null);

  const closeDrawer = useCallback(() => {
    setDrawerOpen(false);
    drawerTrigger.current?.focus();
  }, []);

  // The drawer closes on navigation, on Escape, and on a backdrop click; closing it always returns
  // focus to the control that opened it.
  useEffect(() => {
    if (!drawerOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") closeDrawer();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [drawerOpen, closeDrawer]);

  return (
    <div className="min-h-svh lg:grid lg:grid-cols-[15rem_1fr]">
      <a
        href="#main"
        className="sr-only focus:not-sr-only focus:absolute focus:top-2 focus:left-2 focus:z-50 focus:border focus:border-rule-strong focus:bg-panel focus:px-3 focus:py-1.5 focus:text-body focus:text-ink"
      >
        Skip to the board
      </a>

      <aside className="hidden lg:block lg:h-svh lg:sticky lg:top-0">
        <RailBody />
      </aside>

      {drawerOpen ? (
        <div className="fixed inset-0 z-50 lg:hidden">
          <div
            className="absolute inset-0 bg-[color-mix(in_srgb,var(--palette-ink)_55%,transparent)]"
            onClick={closeDrawer}
            aria-hidden="true"
          />
          <div className="relative h-full w-[15rem] shadow-overlay" data-rail-surface role="dialog" aria-modal="true" aria-label="Board navigation">
            <RailBody onNavigate={closeDrawer} />
          </div>
        </div>
      ) : null}

      <div className="flex min-w-0 flex-col">
        <div className="flex items-center gap-2 border-b border-rule bg-ground px-3 py-2 lg:hidden">
          <IconButton ref={drawerTrigger} label="Open navigation" onClick={() => setDrawerOpen(true)}>
            <MenuIcon size={16} />
          </IconButton>
          <span className="text-body font-semibold text-ink">fleet-console</span>
          <span className="ml-auto">
            <ScopeSwitcher variant="page" />
          </span>
        </div>
        <main id="main" tabIndex={-1} className="min-w-0 flex-1 focus:outline-none">
          {children}
        </main>
        <footer className="border-t border-rule px-3 py-2 text-[0.6875rem] text-ink-muted sm:px-5">
          <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <span>Synthetic demo board · every name, organization and event is authored · not a real service</span>
            <span aria-hidden="true">·</span>
            <a href="/LICENSE.txt" className="underline decoration-rule-strong hover:text-ink">
              MIT licensed
            </a>
          </span>
        </footer>
      </div>
    </div>
  );
}
