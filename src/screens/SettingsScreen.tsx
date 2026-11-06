/** Settings: the service's configuration, as the service actually resolved it.
 *
 *  Every value here comes from `GET /api/config`, which reads the same effective configuration
 *  `--print-config` prints. So the screen cannot show a setting the process is not using, and a
 *  read-only key is refused rather than merely greyed. A save sends back the hash the read carried;
 *  a stale one is a conflict that reloads instead of overwriting the operator's edit.
 *
 *  A saved change needs a restart to take effect, because the process reads its configuration once
 *  at boot. The screen states the command; it never restarts the service itself. */

import { useId, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { TriangleAlert } from "lucide-react";
import { getSettings, isSettingsConflict, putSettings } from "../lib/fleet";
import type { SettingsSnapshot } from "../lib/fleet";
import { PageHeader } from "../shell/PageHeader";
import {
  Badge,
  Button,
  ErrorState,
  FieldRow,
  LoadingRows,
  Panel,
  PanelHead,
  SelectInput,
  TextInput,
  useToast,
} from "../components/ui";

/** The six keys the route accepts. A value the server does not list is never rendered. */
const EDITABLE: { key: string; label: string; hint: string; kind: "bool" | "duration" }[] = [
  {
    key: "tick_interval",
    label: "Poll interval",
    hint: "How often a busy fleet is polled. A duration such as 15s or 2m.",
    kind: "duration",
  },
  {
    key: "notify_interval",
    label: "Notice interval",
    hint: "How often the operator is told about work. A duration such as 30m.",
    kind: "duration",
  },
  {
    key: "act",
    label: "Act",
    hint: "Whether the loop may write to the forge, the rig and git. false is a dry run.",
    kind: "bool",
  },
  {
    key: "rig_lifecycle.enabled",
    label: "Rig lifecycle",
    hint: "Whether the app starts a rig when work arrives and stops it when the rig goes idle.",
    kind: "bool",
  },
  {
    key: "rig_lifecycle.idle_after",
    label: "Stop a rig after",
    hint: "How long a rig may sit with no work before it is stopped. A duration such as 10m.",
    kind: "duration",
  },
  {
    key: "rig_lifecycle.idle_poll_max",
    label: "Idle poll ceiling",
    hint: "The longest the poll may back off while the fleet is idle. Refused when below the poll interval.",
    kind: "duration",
  },
];

const BOOL_OPTIONS = [
  { value: "true", label: "true" },
  { value: "false", label: "false" },
];

const RESTART_COMMAND = "systemctl --user restart fleet-console";

/** The source badge: where a value came from. `environment` and `flag` outrank the file, so naming
 *  the source is how an operator knows why an edit to the file did not change what they see. */
function SourceBadge({ source }: { source?: string }) {
  if (!source) return null;
  const tone = source === "file" ? "quiet" : source === "default" ? "quiet" : "tint";
  return (
    <Badge tone={tone} mono title={`This value came from the ${source}`}>
      {source}
    </Badge>
  );
}

export function SettingsScreen() {
  const settingsQuery = useQuery({ queryKey: ["settings"], queryFn: getSettings, staleTime: 0 });

  if (!settingsQuery.data) {
    return (
      <>
        <PageHeader title="Settings" crumbs={[{ label: "Board", to: "/" }, { label: "Settings" }]} />
        <div className="mx-auto max-w-3xl px-3 py-3 sm:px-5">
          <Panel>
            {settingsQuery.isError ? (
              <ErrorState error={settingsQuery.error} onRetry={() => void settingsQuery.refetch()} />
            ) : (
              <LoadingRows rows={6} />
            )}
          </Panel>
        </div>
      </>
    );
  }

  // Mounting the form against the loaded snapshot seeds its state directly — no effect, so a
  // refresh underneath never clobbers an edit in progress.
  return <SettingsForm snapshot={settingsQuery.data} />;
}

interface SettingsFormProps {
  snapshot: SettingsSnapshot;
}

function SettingsForm({ snapshot }: SettingsFormProps) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const formId = useId();

  const [values, setValues] = useState<Record<string, string>>(snapshot.settings);
  const [baseline, setBaseline] = useState<Record<string, string>>(snapshot.settings);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState<null | { changed: string[]; restart: boolean }>(null);
  const [conflict, setConflict] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const dirtyKeys = EDITABLE.map((f) => f.key).filter((key) => (values[key] ?? "") !== (baseline[key] ?? ""));
  const dirty = dirtyKeys.length > 0;


  const onSave = async () => {
    if (!dirty) return;
    setSaving(true);
    setError(null);
    setConflict(null);
    setSaved(null);
    const body: Record<string, string> = {};
    for (const key of dirtyKeys) body[key] = values[key];
    try {
      const result = await putSettings(body, snapshot.hash);
      setBaseline(values);
      setSaved({ changed: result.changed, restart: result.restart_required });
      if (result.saved) {
        toast.done(
          "Configuration saved",
          result.restart_required ? "Restart the service to apply it." : "The file already held these values.",
        );
      } else {
        toast.done("No change", "The file already held these values.");
      }
      // The save answers with the new hash and the values after the write, so a second save from
      // this screen is a real read-modify-write rather than a stale one.
      await queryClient.invalidateQueries({ queryKey: ["settings"] });
    } catch (err) {
      if (isSettingsConflict(err)) {
        // A 409 means the file moved. Reloading is the only correct next step: retrying with the
        // old hash would be refused again, and forcing the write would discard the other edit.
        setConflict(err.message);
        toast.problem("The file changed on disk", "Reload the settings and try again.");
      } else {
        const message = err instanceof Error ? err.message : "The change was refused.";
        setError(message);
        toast.problem("Configuration refused", message);
      }
    } finally {
      setSaving(false);
    }
  };

  const onReload = async () => {
    setConflict(null);
    setError(null);
    setSaved(null);
    const fresh = await getSettings();
    queryClient.setQueryData(["settings"], fresh);
    setValues(fresh.settings);
    setBaseline(fresh.settings);
  };

  return (
    <>
      <PageHeader
        title="Settings"
        description="The configuration the service is running with, where each value came from, and the keys it refuses to change."
        crumbs={[{ label: "Board", to: "/" }, { label: "Settings" }]}
      />

      <div className="mx-auto flex max-w-3xl flex-col gap-3 px-3 py-3 sm:px-5">
        {conflict ? (
          <Panel>
            <div className="flex flex-wrap items-start gap-3 px-3 py-3" role="alert">
              <TriangleAlert size={14} aria-hidden="true" className="mt-0.5 text-attention" />
              <span className="min-w-[16rem] flex-1">
                <span className="block text-body font-semibold text-ink">The file changed since this page loaded</span>
                <span className="block text-[0.6875rem] text-ink-muted">
                  {conflict} Reloading shows the current values; your unsaved edits are discarded, so copy anything you
                  want to keep first.
                </span>
              </span>
              <Button variant="primary" onClick={() => void onReload()}>
                Reload settings
              </Button>
            </div>
          </Panel>
        ) : null}

        {saved ? (
          <Panel>
            <div className="px-3 py-3" role="status" aria-live="polite">
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-body font-semibold text-ink">
                  {saved.changed.length > 0 ? "Saved" : "No change"}
                </span>
                {saved.changed.map((key) => (
                  <Badge key={key} mono tone="quiet">
                    {key}
                  </Badge>
                ))}
              </div>
              {saved.restart ? (
                <p className="mt-2 max-w-[62ch] text-[0.6875rem] text-ink-muted">
                  The service reads its configuration once at boot, so this needs a restart to take effect. Run:
                </p>
              ) : (
                <p className="mt-2 max-w-[62ch] text-[0.6875rem] text-ink-muted">
                  These values already matched the file; its bytes are unchanged.
                </p>
              )}
              {saved.restart ? (
                <pre className="mt-2 overflow-x-auto rounded-chip border border-rule bg-surface px-2 py-1.5 font-mono text-[0.6875rem] text-ink">
                  {RESTART_COMMAND}
                </pre>
              ) : null}
            </div>
          </Panel>
        ) : null}

        {error ? (
          <Panel>
            <div className="px-3 py-3" role="alert">
              <span className="block text-body font-semibold text-ink">The change was refused</span>
              <span className="block text-[0.6875rem] text-ink-muted">{error}</span>
            </div>
          </Panel>
        ) : null}

        <form
          id={formId}
          onSubmit={(event) => {
            event.preventDefault();
            void onSave();
          }}
        >
          <Panel>
            <PanelHead
              title="Configuration"
              description="Each value shows where it came from. A key the service refuses is listed below, disabled and with the reason."
            />
            <div className="grid gap-3 px-3 py-3 sm:grid-cols-2">
              {EDITABLE.map((field) => {
                const value = values[field.key] ?? "";
                const source = snapshot.source[field.key];
                if (field.kind === "bool") {
                  return (
                    <div key={field.key} className="flex flex-col gap-1">
                      <SelectInput
                        label={field.label}
                        value={value === "true" ? "true" : "false"}
                        onChange={(event) => setValues({ ...values, [field.key]: event.target.value })}
                        options={BOOL_OPTIONS}
                        hint={field.hint}
                        disabled={saving}
                      />
                      <span className="flex items-center gap-1.5 text-[0.6875rem] text-ink-muted">
                        <SourceBadge source={source} />
                        <span className="font-mono">{field.key}</span>
                      </span>
                    </div>
                  );
                }
                return (
                  <div key={field.key} className="flex flex-col gap-1">
                    <TextInput
                      label={field.label}
                      value={value}
                      onChange={(event) => setValues({ ...values, [field.key]: event.target.value })}
                      hint={field.hint}
                      disabled={saving}
                      autoComplete="off"
                      spellCheck={false}
                    />
                    <span className="flex items-center gap-1.5 text-[0.6875rem] text-ink-muted">
                      <SourceBadge source={source} />
                      <span className="font-mono">{field.key}</span>
                    </span>
                  </div>
                );
              })}
            </div>
          </Panel>

          <div className="mt-3 flex flex-wrap items-center gap-2">
            <Button type="submit" variant="primary" busy={saving} disabled={!dirty || Boolean(conflict)}>
              Save configuration
            </Button>
            <Button variant="outline" onClick={() => setValues(baseline)} disabled={!dirty || saving}>
              Discard changes
            </Button>
            <p role="status" aria-live="polite" className="text-[0.6875rem] text-ink-muted">
              {dirty ? `Unsaved changes to ${dirtyKeys.length} key${dirtyKeys.length === 1 ? "" : "s"}.` : "No unsaved changes."}
            </p>
          </div>
        </form>

        <Panel>
          <PanelHead
            title="Read-only"
            description="Keys the settings surface refuses to change. Each is set in the file or the service's environment, and each explains why it cannot be edited here."
          />
          <div className="grid gap-2 px-3 py-3 sm:grid-cols-2">
            {snapshot.read_only.length === 0 ? (
              <FieldRow label="Nothing refused">Every key the file declares is editable from this screen.</FieldRow>
            ) : (
              snapshot.read_only.map((entry) => (
                <FieldRow key={entry.key} label={entry.key}>
                  <span className="block text-ink-muted">{entry.reason}</span>
                </FieldRow>
              ))
            )}
          </div>
          <p className="px-3 pb-3 text-[0.6875rem] text-ink-muted">
            A read-only key is disabled here rather than hidden, so you can see the setting exists and why it is not
            yours to change from this screen. Edit it in the file, then restart the service.
          </p>
        </Panel>
      </div>
    </>
  );
}
