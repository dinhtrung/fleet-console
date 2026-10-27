/** Mock sign-in.
 *
 *  Any seeded account signs in with any non-empty password. The four demo accounts are listed so each
 *  permission state is reachable directly — that is the point of the demo, and the screen says so.
 *  The requested location is not lost: the address bar already points at it, so signing in reveals
 *  exactly the page that was asked for. */

import { useState } from "react";
import { ShieldCheck } from "lucide-react";
import { useAuth } from "../auth/session";
import { TextInput, Button } from "../components/ui";
import { DEMO_IDENTITIES } from "../mock/accounts";
import { cn } from "../lib/cn";

export function SignInScreen({ expired = false }: { expired?: boolean }) {
  const { signIn } = useAuth();
  const [email, setEmail] = useState(DEMO_IDENTITIES[0]!.email);
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (nextEmail = email) => {
    setBusy(true);
    setError(null);
    try {
      await signIn(nextEmail, password || "demo");
    } catch (e) {
      setError(e instanceof Error ? e.message : "Sign-in failed.");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="min-h-svh bg-ground">
      <div className="mx-auto grid min-h-svh max-w-5xl grid-cols-1 lg:grid-cols-[1fr_20rem]">
        <div className="flex flex-col justify-center px-5 py-10">
          <h1 className="text-[2.25rem] font-semibold leading-[1.05] tracking-[-0.03em] text-ink">
            fleet-console
          </h1>
          <p className="mt-3 max-w-[58ch] text-body text-ink-muted">
            A synthetic demo board: users, roles, organizations, active sessions and the audit record —
            operable end to end on authored data, with no server and no account of your own. Every write
            you make lands in the audit record, and every list works empty as well as full.
          </p>
          <dl className="mt-6 grid max-w-[46ch] grid-cols-[9rem_1fr] gap-y-1.5 text-[0.6875rem]">
            <dt className="label text-ink-muted">Data source</dt>
            <dd className="text-ink">In-browser mock layer, 160 ms latency, opt-in failures</dd>
            <dt className="label text-ink-muted">Written to</dt>
            <dd className="text-ink">This browser only — nothing leaves the machine</dd>
            <dt className="label text-ink-muted">License</dt>
            <dd className="text-ink">MIT</dd>
          </dl>
        </div>

        <div className="border-t border-rule bg-panel px-5 py-8 lg:border-l lg:border-t-0">
          <div className="flex items-center gap-2">
            <ShieldCheck size={16} aria-hidden="true" className="text-ink-muted" />
            <h2 className="text-lead font-semibold text-ink">Sign in</h2>
          </div>

          {expired ? (
            <p
              role="status"
              className="mt-3 border border-attention px-2 py-1.5 text-[0.6875rem] text-ink"
            >
              Your session ended after {Math.round(30)} minutes of inactivity. Sign in again to return to
              the page you were on.
            </p>
          ) : null}

          <form
            className="mt-4 flex flex-col gap-3"
            onSubmit={(e) => {
              e.preventDefault();
              void submit();
            }}
          >
            <TextInput
              label="Work email"
              type="email"
              autoComplete="username"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              error={error ?? undefined}
            />
            <TextInput
              label="Password"
              type="password"
              autoComplete="current-password"
              value={password}
              placeholder="any password works on the demo board"
              onChange={(e) => setPassword(e.target.value)}
              hint="Mock authentication: any non-empty password is accepted."
            />
            <Button type="submit" variant="primary" busy={busy}>
              Sign in
            </Button>
          </form>

          <p className="label mt-6 text-ink-muted">Demo accounts</p>
          <ul className="mt-2 flex flex-col divide-y divide-[var(--rule)] border border-rule">
            {DEMO_IDENTITIES.map((account) => (
              <li key={account.id}>
                <button
                  type="button"
                  onClick={() => {
                    setEmail(account.email);
                    setPassword("demo");
                    void submit(account.email);
                  }}
                  className={cn(
                    "flex w-full flex-col items-start gap-0.5 px-2 py-2 text-left hover:bg-hover",
                    email === account.email && "bg-selected",
                  )}
                >
                  <span className="text-body font-semibold text-ink">{account.name}</span>
                  <span className="font-mono text-[0.625rem] text-ink-muted">{account.email}</span>
                  <span className="text-[0.6875rem] text-ink-muted">{account.note}</span>
                </button>
              </li>
            ))}
          </ul>
        </div>
      </div>
    </div>
  );
}
