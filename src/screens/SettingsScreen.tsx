/** Settings: the workspace, your own profile, the appearance and the one destructive door.
 *
 *  One form, one save — and it will not let an edit vanish quietly: leaving with unsaved changes
 *  is blocked, cancelling asks first, and the destructive zone sits apart from everything else
 *  behind a typed confirmation. A slug the service refuses is reported next to the slug field, not
 *  swallowed. */

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useBlocker } from "@tanstack/react-router";
import { RotateCcw, TriangleAlert } from "lucide-react";
import { getSettings, resetDatabase, updateSettings, updateUser } from "../mock/api";
import { USER_STATUSES, isApiError } from "../mock/types";
import type { UserStatus, WorkspaceSettings } from "../mock/types";
import { useAuth } from "../auth/session";
import { RequirePermission } from "../components/RequirePermission";
import { PageHeader } from "../shell/PageHeader";
import { AppearanceControl } from "../shell/AppearanceControl";
import {
  Button,
  ConfirmDialog,
  Dialog,
  ErrorState,
  FieldRow,
  LoadingRows,
  Panel,
  PanelHead,
  SelectInput,
  TextInput,
  useToast,
} from "../components/ui";
import { isSlug } from "../lib/format";

/* ------------------------------------------------------------------ local preferences */

type Density = "comfortable" | "compact";

const DENSITY_KEY = "fleet-console.density";

const DENSITY_OPTIONS: { value: Density; label: string }[] = [
  { value: "comfortable", label: "Comfortable" },
  { value: "compact", label: "Compact" },
];

function readDensity(): Density {
  try {
    return localStorage.getItem(DENSITY_KEY) === "compact" ? "compact" : "comfortable";
  } catch {
    return "comfortable";
  }
}

/** Density is a personal preference with no service endpoint, so it is stored beside the
 *  appearance choice and announced on the document for the board's styles to pick up. */
function writeDensity(density: Density): void {
  try {
    localStorage.setItem(DENSITY_KEY, density);
  } catch {
    // storage unavailable: the preference still applies for this session
  }
  document.documentElement.dataset.density = density;
}

const STATUS_LABELS: Record<UserStatus, string> = {
  active: "Active",
  invited: "Invited",
  suspended: "Suspended",
  deactivated: "Deactivated",
};

const STATUS_OPTIONS: { value: UserStatus; label: string }[] = USER_STATUSES.map((value) => ({
  value,
  label: STATUS_LABELS[value],
}));

const LANDING_OPTIONS: { value: WorkspaceSettings["defaultLanding"]; label: string }[] = [
  { value: "dashboard", label: "Overview" },
  { value: "users", label: "Users" },
  { value: "sessions", label: "Sessions" },
  { value: "audit", label: "Audit record" },
];

const MAX_DISPLAY_NAME = 64;

/* ------------------------------------------------------------------ the form model */

interface Draft {
  name: string;
  slug: string;
  defaultUserStatus: UserStatus;
  defaultLanding: WorkspaceSettings["defaultLanding"];
  displayName: string;
  density: Density;
}

interface FieldErrors {
  name?: string;
  slug?: string;
  displayName?: string;
}

function seedDraft(settings: WorkspaceSettings, displayName: string): Draft {
  return {
    name: settings.name,
    slug: settings.slug,
    defaultUserStatus: settings.defaultUserStatus,
    defaultLanding: settings.defaultLanding,
    displayName,
    density: readDensity(),
  };
}

function editedFields(draft: Draft, baseline: Draft): Partial<Record<keyof Draft, true>> {
  const changed: Partial<Record<keyof Draft, true>> = {};
  if (draft.name.trim() !== baseline.name) changed.name = true;
  if (draft.slug.trim() !== baseline.slug) changed.slug = true;
  if (draft.defaultUserStatus !== baseline.defaultUserStatus) changed.defaultUserStatus = true;
  if (draft.defaultLanding !== baseline.defaultLanding) changed.defaultLanding = true;
  if (draft.displayName.trim() !== baseline.displayName) changed.displayName = true;
  if (draft.density !== baseline.density) changed.density = true;
  return changed;
}

/** The rules the surface checks before it asks the service: an empty or over-long display name and
 *  a malformed slug never leave the form. A duplicate slug is the service's to refuse, and its
 *  message lands on the field. */
function validate(draft: Draft, baseline: Draft): FieldErrors {
  const errors: FieldErrors = {};
  if (draft.name.trim().length < 2) errors.name = "The workspace needs a name of at least two characters.";
  const slug = draft.slug.trim();
  if (slug !== baseline.slug && !isSlug(slug)) {
    errors.slug =
      "Use lowercase letters, numbers and single hyphens, three to forty-eight characters (for example fleet-console).";
  }
  const displayName = draft.displayName.trim();
  if (displayName.length === 0) errors.displayName = "A display name is required.";
  else if (displayName.length > MAX_DISPLAY_NAME) {
    errors.displayName = `Keep the display name to ${MAX_DISPLAY_NAME} characters or fewer.`;
  }
  return errors;
}

export function SettingsScreen() {
  return (
    <RequirePermission permission="settings.write" what="Workspace settings" title="Settings">
      <SettingsBoard />
    </RequirePermission>
  );
}

function SettingsBoard() {
  const settingsQuery = useQuery({ queryKey: ["settings"], queryFn: getSettings });

  if (!settingsQuery.data) {
    return (
      <>
        <PageHeader title="Settings" crumbs={[{ label: "Board", to: "/" }, { label: "Settings" }]} />
        <div className="mx-auto max-w-3xl px-3 py-3 sm:px-5">
          <Panel>
            {settingsQuery.isError ? (
              <ErrorState error={settingsQuery.error} onRetry={() => void settingsQuery.refetch()} />
            ) : (
              <LoadingRows rows={5} />
            )}
          </Panel>
        </div>
      </>
    );
  }

  // Mounting the form against the loaded settings seeds its state directly — no effect, so an
  // edit is never clobbered by a refresh underneath it.
  return <SettingsForm settings={settingsQuery.data} />;
}

function SettingsForm({ settings }: { settings: WorkspaceSettings }) {
  const { identity, switchTo } = useAuth();
  const toast = useToast();
  const queryClient = useQueryClient();

  const displayName = identity?.name ?? "";
  const [draft, setDraft] = useState<Draft>(() => seedDraft(settings, displayName));
  const [baseline, setBaseline] = useState<Draft>(() => seedDraft(settings, displayName));
  const [errors, setErrors] = useState<FieldErrors>({});
  const [saving, setSaving] = useState(false);
  const [discardOpen, setDiscardOpen] = useState(false);
  const [resetOpen, setResetOpen] = useState(false);

  const dirty = Object.keys(editedFields(draft, baseline)).length > 0;

  // Leaving the surface with edits pending warns first; a reload or a closed tab is caught too.
  const blocker = useBlocker({
    disabled: !dirty,
    enableBeforeUnload: dirty,
    withResolver: true,
    shouldBlockFn: () => dirty,
  });

  const onSave = async () => {
    const nextErrors = validate(draft, baseline);
    setErrors(nextErrors);
    if (Object.keys(nextErrors).length > 0) return;

    const changed = editedFields(draft, baseline);
    setSaving(true);
    try {
      if (changed.name || changed.slug || changed.defaultUserStatus || changed.defaultLanding) {
        const patch: Partial<WorkspaceSettings> = {};
        if (changed.name) patch.name = draft.name.trim();
        if (changed.slug) patch.slug = draft.slug.trim();
        if (changed.defaultUserStatus) patch.defaultUserStatus = draft.defaultUserStatus;
        if (changed.defaultLanding) patch.defaultLanding = draft.defaultLanding;
        try {
          await updateSettings(patch);
        } catch (error) {
          const message = isApiError(error) ? error.message : "The change was refused.";
          // A duplicate, empty or malformed slug comes back as a conflict: it lands on the field.
          const field: keyof FieldErrors = /slug/i.test(message) ? "slug" : "name";
          setErrors({ [field]: message });
          toast.problem("Workspace settings refused", message);
          return;
        }
      }

      if (changed.displayName && identity) {
        try {
          await updateUser(identity.userId, { name: draft.displayName.trim() });
          // Re-apply the identity so the rail shows the new name without a fresh sign-in.
          await switchTo(identity.userId);
        } catch (error) {
          const message = isApiError(error) ? error.message : "The display name was refused.";
          setErrors({ displayName: message });
          toast.problem("Display name refused", message);
          return;
        }
      }

      if (changed.density) writeDensity(draft.density);

      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["settings"] }),
        queryClient.invalidateQueries({ queryKey: ["users"] }),
        queryClient.invalidateQueries({ queryKey: ["dashboard"] }),
        queryClient.invalidateQueries({ queryKey: ["audit"] }),
        ...(identity ? [queryClient.invalidateQueries({ queryKey: ["user", identity.userId] })] : []),
      ]);

      setBaseline(draft);
      setErrors({});
      toast.done("Settings saved", "The workspace, your profile and the audit record now show the change.");
    } finally {
      setSaving(false);
    }
  };

  const confirmDiscard = () => {
    setDraft({ ...baseline });
    setErrors({});
    setDiscardOpen(false);
  };

  const confirmReset = async () => {
    resetDatabase();
    setResetOpen(false);
    await queryClient.invalidateQueries();
    const fresh = await getSettings();
    const reseeded = seedDraft(fresh, identity?.name ?? "");
    setDraft(reseeded);
    setBaseline(reseeded);
    setErrors({});
    toast.done("Board reset to its seed", "Every change made in this browser was discarded.");
  };

  return (
    <>
      <PageHeader
        title="Settings"
        description="The workspace configuration, your own profile and preferences, the appearance, and a separated zone for actions that cannot be undone."
        crumbs={[{ label: "Board", to: "/" }, { label: "Settings" }]}
      />

      <form
        className="mx-auto flex max-w-3xl flex-col gap-3 px-3 py-3 sm:px-5"
        onSubmit={(event) => {
          event.preventDefault();
          void onSave();
        }}
      >
        <Panel>
          <PanelHead
            title="Workspace"
            description="The name and identifier the board is built on, and the defaults applied to new records."
          />
          <div className="grid gap-3 px-3 py-3 sm:grid-cols-2">
            <TextInput
              label="Workspace name"
              value={draft.name}
              onChange={(event) => setDraft({ ...draft, name: event.target.value })}
              error={errors.name}
              autoComplete="off"
            />
            <TextInput
              label="Identifier slug"
              value={draft.slug}
              onChange={(event) => setDraft({ ...draft, slug: event.target.value })}
              error={errors.slug}
              hint="Lowercase letters, numbers and single hyphens; used in links."
              autoComplete="off"
            />
            <SelectInput
              label="Default status for new users"
              value={draft.defaultUserStatus}
              onChange={(event) => setDraft({ ...draft, defaultUserStatus: event.target.value as UserStatus })}
              options={STATUS_OPTIONS}
            />
            <SelectInput
              label="Default landing surface"
              value={draft.defaultLanding}
              onChange={(event) =>
                setDraft({ ...draft, defaultLanding: event.target.value as WorkspaceSettings["defaultLanding"] })
              }
              options={LANDING_OPTIONS}
            />
          </div>
        </Panel>

        <Panel>
          <PanelHead title="Your profile" description="How you are named on the board. Your email address is fixed." />
          <div className="grid gap-3 px-3 py-3 sm:grid-cols-2">
            <TextInput
              label="Display name"
              value={draft.displayName}
              onChange={(event) => setDraft({ ...draft, displayName: event.target.value })}
              error={errors.displayName}
              hint={`${MAX_DISPLAY_NAME} characters or fewer.`}
              autoComplete="off"
            />
            <FieldRow label="Signed in as">{identity?.email ?? "—"}</FieldRow>
            <SelectInput
              label="Display density"
              value={draft.density}
              onChange={(event) => setDraft({ ...draft, density: event.target.value as Density })}
              options={DENSITY_OPTIONS}
              hint="Stored as a personal preference and applied on the next load."
            />
          </div>
        </Panel>

        <Panel>
          <PanelHead
            title="Appearance"
            description="Light, dark or follow the system. The choice is stored in this browser and restored on the next load."
          />
          <div className="flex flex-col gap-2 px-3 py-3">
            {/* The control takes the panel tone here: on the rail it is drawn in rail colours, and on
                a light panel those would be white on white. */}
            <AppearanceControl tone="panel" />
            <p className="max-w-[60ch] text-[0.6875rem] text-ink-muted">
              Appearance applies as soon as it is chosen; it is not part of the unsaved changes above.
            </p>
          </div>
        </Panel>

        <div className="flex flex-wrap items-center gap-2">
          <Button type="submit" variant="primary" busy={saving} disabled={!dirty}>
            Save changes
          </Button>
          <Button variant="outline" onClick={() => setDiscardOpen(true)} disabled={!dirty}>
            Cancel
          </Button>
          <p role="status" aria-live="polite" className="text-[0.6875rem] text-ink-muted">
            {dirty ? "Unsaved changes." : "No unsaved changes — save and cancel are unavailable."}
          </p>
        </div>
      </form>

      <section className="mx-auto max-w-3xl px-3 pb-6 sm:px-5">
        <div className="flex items-center gap-2 border-t border-rule-strong pt-4">
          <TriangleAlert size={14} aria-hidden="true" className="text-attention" />
          <h2 className="label text-ink-muted">Destructive zone</h2>
        </div>
        <p className="mt-1 max-w-[60ch] text-[0.6875rem] text-ink-muted">
          Kept apart from the settings above. An action here cannot be undone and demands a typed confirmation.
        </p>
        <Panel className="mt-3">
          <div className="flex flex-wrap items-center gap-3 px-3 py-3">
            <span className="min-w-[16rem] flex-1">
              <span className="block text-body font-semibold text-ink">Reset the board to its seed</span>
              <span className="block text-[0.6875rem] text-ink-muted">
                Discards every user, role, organization, session and audit event in this browser and writes the
                deterministic seed back.
              </span>
            </span>
            <Button
              variant="attention"
              icon={<RotateCcw size={13} aria-hidden="true" />}
              onClick={() => setResetOpen(true)}
            >
              Reset the board…
            </Button>
          </div>
        </Panel>
      </section>

      <ConfirmDialog
        open={discardOpen}
        onClose={() => setDiscardOpen(false)}
        onConfirm={confirmDiscard}
        title="Discard unsaved changes?"
        description="The workspace and profile fields go back to their last saved values. Nothing on the board changes."
        confirmLabel="Discard changes"
      />

      <ConfirmDialog
        open={resetOpen}
        onClose={() => setResetOpen(false)}
        onConfirm={() => void confirmReset()}
        title="Reset the board to its seed?"
        description="Every record and the whole audit trail in this browser are discarded and the deterministic seed is written back. This cannot be undone."
        confirmLabel="Reset the board"
        requireTyped={baseline.slug}
        typedLabel={`Type ${baseline.slug} to confirm`}
      />

      {blocker.status === "blocked" ? (
        <Dialog
          open
          onClose={() => blocker.reset?.()}
          title="Leave settings without saving?"
          description="You have unsaved changes. Leaving now discards them; stay here to keep editing."
          width="sm"
          footer={
            <>
              <Button variant="quiet" onClick={() => blocker.reset?.()}>
                Stay on settings
              </Button>
              <Button variant="attention" onClick={() => blocker.proceed?.()}>
                Discard and leave
              </Button>
            </>
          }
        />
      ) : null}
    </>
  );
}
