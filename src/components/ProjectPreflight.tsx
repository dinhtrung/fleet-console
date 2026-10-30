/** Preflight: what the board checks before it dispatches, and the one action that fixes what it can.
 *
 *  Two surfaces share this: the create dialog (against a **draft**, where `rig` is `unknown` because
 *  the rig is created with the project) and the project view (against the registry row). Both render
 *  the same four rows, and neither ever offers a field to type a credential — the runtime row shows
 *  a boolean, a login command the operator runs on the box, and nothing else. */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Copy, Plus, RefreshCw, Terminal } from "lucide-react";
import { useEffect, useState } from "react";
import {
  getPreflight,
  getRuntimes,
  preflightDraft,
  runSetup,
  SETUP_STEPS,
} from "../lib/fleet";
import type {
  CheckState,
  CreateProjectInput,
  PreflightCheck,
  RuntimeInfo,
  SetupStepResult,
} from "../lib/fleet";
import { Badge, Button, LoadingRows, ErrorState } from "./ui";

/** One badge per state. `unknown` is deliberately not grey-but-fine: it is its own colour, because
 *  the design's rule is that an unknown is never allowed to read as ok. */
const STATE_TONE: Record<CheckState, "quiet" | "ink" | "tint"> = {
  ok: "quiet",
  warn: "tint",
  fail: "ink",
  unknown: "tint",
};

const STATE_LABEL: Record<CheckState, string> = {
  ok: "ok",
  warn: "warn",
  fail: "fail",
  unknown: "unknown",
};

const STEP_LABEL: Record<string, string> = {
  mkdir_base: "Make the directory",
  clone: "Clone the repository",
  apply_rig: "Render and sync the rig spec",
  up_rig: "Launch the rig",
};

function CopyCommand({ command }: { command: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(command);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      // The command stays selectable in the DOM; a refused clipboard is not worth a toast.
    }
  };
  return (
    <div className="mt-1 flex items-stretch gap-1.5">
      <code className="min-w-0 flex-1 overflow-x-auto rounded-chip border border-rule-strong bg-ground px-2 py-1 font-mono text-[0.6875rem] text-ink">
        {command}
      </code>
      <Button
        variant="outline"
        size="sm"
        icon={copied ? <Check size={12} aria-hidden="true" /> : <Copy size={12} aria-hidden="true" />}
        onClick={() => void copy()}
      >
        {copied ? "Copied" : "Copy"}
      </Button>
    </div>
  );
}

/** One check: badge, label, the detail verbatim, and either the fix or the step that runs it. */
function CheckRow({
  check,
  busyStep,
  onRunStep,
}: {
  check: PreflightCheck;
  busyStep?: string;
  onRunStep?: (step: string) => void;
}) {
  return (
    <li className="flex flex-col gap-1 border-b border-rule px-3 py-2 last:border-0">
      <div className="flex flex-wrap items-baseline gap-2">
        <Badge tone={STATE_TONE[check.state]} mono>
          {STATE_LABEL[check.state]}
        </Badge>
        <span className="text-body font-medium text-ink">{check.label}</span>
        <span className="ml-auto font-mono text-[0.6875rem] text-ink-muted">{check.id}</span>
      </div>
      {/* The detail is the command's own last line, verbatim. Never a paraphrase: "fatal: could not
          read Username for 'https://github.com'" tells the operator what "auth failed" does not. */}
      <p className="max-w-[80ch] font-mono text-[0.6875rem] break-words text-ink">{check.detail}</p>
      {check.fix.why ? <p className="max-w-[80ch] text-[0.6875rem] text-ink-muted">{check.fix.why}</p> : null}
      {check.fix.command ? <CopyCommand command={check.fix.command} /> : null}
      {check.fix.step && onRunStep ? (
        <div>
          <Button
            variant="outline"
            size="sm"
            busy={busyStep === check.fix.step}
            icon={<Plus size={12} aria-hidden="true" />}
            onClick={() => onRunStep(check.fix.step)}
          >
            Set up this step
          </Button>
        </div>
      ) : null}
    </li>
  );
}

/** The runtime row: state, the login command, and a Re-check. No input, no masked field, no form —
 *  the operator logs in on the box and the app never asks for the credential. */
function RuntimeRow({ runtime, onRecheck, busy }: { runtime: RuntimeInfo; onRecheck: () => void; busy: boolean }) {
  const state: CheckState = runtime.auth_present ? "ok" : runtime.supported ? "fail" : "fail";
  return (
    <div className="border-t border-rule px-3 py-2">
      <div className="flex flex-wrap items-baseline gap-2">
        <Badge tone={STATE_TONE[state]} mono>
          {runtime.auth_present ? "credential present" : "no credential"}
        </Badge>
        <span className="text-body font-medium text-ink">{runtime.id}</span>
        <Badge tone={runtime.supported ? "quiet" : "tint"} mono>
          {runtime.supported ? "installed" : "not installed"}
        </Badge>
        <Button
          variant="quiet"
          size="sm"
          className="ml-auto"
          busy={busy}
          icon={<RefreshCw size={12} aria-hidden="true" />}
          onClick={onRecheck}
        >
          Re-check
        </Button>
      </div>
      {runtime.supported ? (
        <div className="mt-1 flex flex-col gap-1">
          <p className="text-[0.6875rem] text-ink-muted">Log in on this box — the app never asks for the credential:</p>
          <CopyCommand command={runtime.login_hint} />
          <p className="max-w-[80ch] text-[0.6875rem] text-ink-muted">{runtime.login_doc}</p>
        </div>
      ) : null}
    </div>
  );
}

/** The setup results, one line per step: what ran, what it did, and the last line it printed. */
function SetupResults({ steps }: { steps: SetupStepResult[] }) {
  return (
    <ul className="border-t border-rule" aria-live="polite">
      {steps.map((step) => (
        <li key={step.step} className="flex flex-col gap-0.5 border-b border-rule px-3 py-2 last:border-0">
          <div className="flex flex-wrap items-baseline gap-2">
            <Badge tone={step.state === "failed" ? "ink" : "quiet"} mono>
              {step.state}
            </Badge>
            <span className="text-body text-ink">{STEP_LABEL[step.step] ?? step.step}</span>
            <span className="ml-auto font-mono text-[0.6875rem] text-ink-muted">{step.step}</span>
          </div>
          {step.command ? (
            <code className="max-w-[80ch] overflow-x-auto font-mono text-[0.6875rem] text-ink-muted">{step.command}</code>
          ) : null}
          {step.output ? (
            <p className="max-w-[80ch] font-mono text-[0.6875rem] break-words text-ink">{step.output}</p>
          ) : null}
        </li>
      ))}
    </ul>
  );
}

/** The whole panel for a registered project: four checks, the runtime row, and the two actions.
 *  `onRecheck` refetches; the setup run posts the allowlisted list and renders per-step results. */
export function ProjectPreflight({ projectKey }: { projectKey: string }) {
  const queryClient = useQueryClient();
  const report = useQuery({ queryKey: ["preflight", projectKey], queryFn: () => getPreflight(projectKey) });
  const runtimes = useQuery({ queryKey: ["runtimes"], queryFn: getRuntimes });
  const [steps, setSteps] = useState<SetupStepResult[] | null>(null);
  const [busyStep, setBusyStep] = useState<string>("");

  const setup = useMutation({
    mutationFn: (chosen: string[]) => runSetup(projectKey, chosen),
    onSuccess: (response) => {
      setSteps(response.steps);
      void queryClient.invalidateQueries({ queryKey: ["preflight", projectKey] });
    },
    onSettled: () => setBusyStep(""),
  });

  const start = (chosen: string[]) => {
    setBusyStep(chosen.length === 1 ? chosen[0] : "all");
    setup.mutate(chosen);
  };

  const runtimeInfo = report.data?.runtime ?? runtimes.data?.runtimes.find((r) => r.id === report.data?.runtime.id);

  if (report.isLoading) return <LoadingRows rows={4} />;
  if (report.error) {
    return (
      <ErrorState
        title="The preflight did not run"
        error={report.error}
        onRetry={() => void report.refetch()}
      />
    );
  }

  const data = report.data;
  const failing = data ? data.checks.filter((check) => check.state !== "ok") : [];

  return (
    <div className="flex flex-col">
      <div className="flex flex-wrap items-baseline gap-2 border-b border-rule bg-ground px-3 py-2">
        <span className="label text-ink-muted">Before it dispatches</span>
        <Badge tone={data?.ready ? "quiet" : "ink"} mono>
          {data?.ready ? "ready" : "not ready"}
        </Badge>
        {failing.length > 0 ? (
          <span className="text-[0.6875rem] text-ink-muted">
            {failing.map((check) => check.id).join(", ")} {failing.length === 1 ? "needs" : "need"} attention
          </span>
        ) : null}
        <Button
          variant="primary"
          size="sm"
          className="ml-auto"
          busy={busyStep === "all"}
          disabled={setup.isPending}
          icon={<Terminal size={12} aria-hidden="true" />}
          onClick={() => start([...SETUP_STEPS])}
        >
          Set up what the app can
        </Button>
      </div>
      <ul>
        {data?.checks.map((check) => (
          <CheckRow key={check.id} check={check} busyStep={busyStep} onRunStep={(step) => start([step])} />
        ))}
      </ul>
      {runtimeInfo ? (
        <RuntimeRow
          runtime={runtimeInfo}
          busy={runtimes.isFetching}
          onRecheck={() => {
            void runtimes.refetch();
            void report.refetch();
          }}
        />
      ) : null}
      {steps ? <SetupResults steps={steps} /> : null}
    </div>
  );
}

/** The live panel under the create form. It runs against a **draft**: the URL and the path are
 *  checkable before the project exists, and `rig` says so rather than pretending. */
export function DraftPreflight({ input, enabled }: { input: CreateProjectInput; enabled: boolean }) {
  // The checks cost a `git ls-remote` — up to 20 seconds — so they must not run on every keystroke.
  // The draft is debounced into a settled copy, and the query is keyed on that copy: typing "abc"
  // is one check, not three, and the in-flight one is cancelled when the value changes again.
  const settled = useDebounced(input, 700);
  const draft = useQuery({
    queryKey: ["preflight-draft", settled],
    queryFn: () => preflightDraft(settled),
    enabled: enabled && settled.key.trim().length > 0,
    // Retrying a bad URL would hammer the forge for the same answer.
    retry: false,
    staleTime: 0,
  });
  const runtimes = useQuery({ queryKey: ["runtimes"], queryFn: getRuntimes });

  return (
    <div className="border-t border-rule bg-ground/40">
      <div className="flex flex-wrap items-baseline gap-2 px-3 pt-2">
        <span className="label text-ink-muted">Checked before it exists</span>
        {draft.isFetching ? <span className="text-[0.6875rem] text-ink-muted">checking…</span> : null}
        {draft.data ? (
          <Badge tone={draft.data.ready ? "quiet" : "ink"} mono>
            {draft.data.ready ? "would be ready" : "would not be ready"}
          </Badge>
        ) : null}
      </div>
      {draft.data ? (
        <>
          <ul>
            {draft.data.checks.map((check) => (
              <CheckRow key={check.id} check={check} />
            ))}
          </ul>
          <RuntimeRow
            runtime={draft.data.runtime}
            busy={runtimes.isFetching}
            onRecheck={() => void draft.refetch()}
          />
        </>
      ) : (
        <p className="px-3 py-2 text-[0.6875rem] text-ink-muted">
          Fill in the key, repo and path and the checks run here, before anything is written.
        </p>
      )}
    </div>
  );
}

/** A value that only updates once the input has been still for `delay` ms. Used to keep a
 *  per-keystroke form from firing one network check per character. */
function useDebounced<T>(value: T, delay: number): T {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const timer = window.setTimeout(() => setSettled(value), delay);
    return () => window.clearTimeout(timer);
  }, [value, delay]);
  return settled;
}

/** The runtime picker, driven by the server's list so the four values are never hardcoded here. */
export function RuntimePicker({
  value,
  onChange,
  busy,
}: {
  value: string;
  onChange: (next: string) => void;
  busy: boolean;
}) {
  const runtimes = useQuery({ queryKey: ["runtimes"], queryFn: getRuntimes });
  const list = runtimes.data?.runtimes ?? [];
  return (
    <div className="flex flex-col gap-1">
      <label htmlFor="runtime" className="label text-ink-muted">
        Runtime
      </label>
      <select
        id="runtime"
        name="runtime"
        value={value}
        disabled={busy || list.length === 0}
        onChange={(event) => onChange(event.target.value)}
        className="min-h-[var(--density-control-h)] w-full rounded-chip border border-rule-strong bg-panel px-2 text-body text-ink disabled:cursor-not-allowed disabled:opacity-50"
      >
        {list.map((runtime) => (
          <option key={runtime.id} value={runtime.id}>
            {runtime.id}
            {runtime.auth_present ? " — logged in" : runtime.supported ? " — no credential" : " — not installed"}
          </option>
        ))}
      </select>
      <p className="text-[0.6875rem] text-ink-muted">
        Driven by the service: a runtime this host cannot authenticate is named, not hidden.
      </p>
    </div>
  );
}
