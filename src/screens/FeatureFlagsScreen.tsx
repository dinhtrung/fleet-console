/** The feature flag directory.
 *
 *  One board for reading every flag — name, key, state, rollout, environments, owner and when it
 *  last changed — narrowed by search, state and environment. Creating and editing happen in a
 *  `SidePanel` that enters from the right; at desktop it is deliberately non-modal so the directory
 *  stays readable beside the record being changed. Deleting is irreversible and goes through a
 *  `ConfirmDialog` that asks for the flag's key. Every write is gated behind `flags.write`, every
 *  refusal is shown against the field that caused it, and a save with nothing changed never claims
 *  success. */

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pencil, Plus, Trash2 } from "lucide-react";
import {
  createFlag,
  DEFAULT_PAGE_SIZE,
  deleteFlag,
  listFlags,
  listUsers,
  updateFlag,
} from "../mock/api";
import { FLAG_ENVIRONMENTS, FLAG_STATES, isApiError } from "../mock/types";
import type { FeatureFlag, FeatureFlagInput, FlagState, ListQuery, User } from "../mock/types";
import { useScope } from "../shell/scope";
import { Gate } from "../access/access";
import { RequirePermission } from "../components/RequirePermission";
import { PageHeader } from "../shell/PageHeader";
import { useListState } from "../lib/urlState";
import { DataGrid, GridSearch } from "../components/grid/DataGrid";
import type { GridColumn } from "../components/grid/DataGrid";
import {
  Badge,
  Button,
  Checkbox,
  ConfirmDialog,
  IconButton,
  Panel,
  SelectInput,
  SidePanel,
  StatusMagnet,
  TextArea,
  TextInput,
  Toolbar,
  useToast,
} from "../components/ui";
import { dateTime, plural, relativeTime } from "../lib/format";

/** The row shape `listFlags` returns: the flag with its owner resolved. */
type FlagRow = FeatureFlag & { owner: User | undefined };

const STATE_LABEL: Record<FlagState, string> = {
  on: "On",
  off: "Off",
  gradual: "Gradual",
};

/** Environment names in one place: the dense directory uses the short form and keeps the full name in
 *  the tooltip, the panel and the filter use the full name. */
const ENV_LABELS: Record<string, { full: string; short: string }> = {
  production: { full: "Production", short: "PROD" },
  staging: { full: "Staging", short: "STAGE" },
  development: { full: "Development", short: "DEV" },
};

const envFull = (environment: string) => ENV_LABELS[environment]?.full ?? environment;
const envShort = (environment: string) => ENV_LABELS[environment]?.short ?? environment;

interface FlagForm {
  key: string;
  name: string;
  description: string;
  state: FlagState;
  rollout: string;
  environments: string[];
  ownerId: string;
}

const EMPTY_FORM: FlagForm = {
  key: "",
  name: "",
  description: "",
  state: "off",
  rollout: "",
  environments: [],
  ownerId: "",
};

function formOf(flag: FeatureFlag): FlagForm {
  return {
    key: flag.key,
    name: flag.name,
    description: flag.description,
    state: flag.state,
    rollout: String(flag.rollout),
    environments: [...flag.environments],
    ownerId: flag.ownerId,
  };
}

/** Unsaved-changes comparison: environments are a set, so their order is not a difference. */
function serializeForm(form: FlagForm): string {
  return JSON.stringify({ ...form, environments: [...form.environments].sort() });
}

/** Send only the fields that actually differ from the saved flag. */
function patchOf(baseline: FlagForm, input: FeatureFlagInput): Partial<FeatureFlagInput> {
  const patch: Partial<FeatureFlagInput> = {};
  if (input.key !== baseline.key) patch.key = input.key;
  if (input.name !== baseline.name) patch.name = input.name;
  if (input.description !== baseline.description) patch.description = input.description;
  if (input.state !== baseline.state) patch.state = input.state;
  if (input.rollout !== Number(baseline.rollout)) patch.rollout = input.rollout;
  if ([...input.environments].sort().join(",") !== [...baseline.environments].sort().join(",")) {
    patch.environments = input.environments;
  }
  if (input.ownerId !== baseline.ownerId) patch.ownerId = input.ownerId;
  return patch;
}

type FieldKey = "key" | "name" | "description" | "rollout" | "environments" | "owner";

/** The API writes its refusals for the operator; route the message to the field it names. */
function fieldForMessage(message: string): FieldKey | null {
  const m = message.toLowerCase();
  if (m.includes("valid key") || m.includes("with the key")) return "key";
  if (m.includes("flag name")) return "name";
  if (m.includes("rollout")) return "rollout";
  if (m.includes("environment")) return "environments";
  if (m.includes("owner")) return "owner";
  return null;
}

export function FeatureFlagsScreen() {
  return (
    <RequirePermission permission="flags.read" what="The feature flag directory" title="Feature flags">
      <FeatureFlagsBoard />
    </RequirePermission>
  );
}

function FeatureFlagsBoard() {
  const { sweepKey } = useScope();
  const toast = useToast();
  const queryClient = useQueryClient();

  const [list, setList] = useListState({
    page: 1,
    pageSize: DEFAULT_PAGE_SIZE,
    sort: "name",
    dir: "asc",
    q: "",
    state: "",
    environment: "",
  });

  const query: ListQuery = {
    page: list.page,
    pageSize: list.pageSize,
    sort: list.sort,
    dir: list.dir === "desc" ? "desc" : "asc",
    q: list.q.trim() || undefined,
    status: list.state || undefined,
    environment: list.environment || undefined,
  };

  const flagsQuery = useQuery({ queryKey: ["flags", query], queryFn: () => listFlags(query) });
  const ownersQuery = useQuery({
    queryKey: ["users", { picker: "flag-owners" }],
    queryFn: () => listUsers({ pageSize: 50, sort: "name", dir: "asc" }),
  });

  const rows: FlagRow[] = flagsQuery.data?.items ?? [];
  const total = flagsQuery.data?.total ?? 0;
  const page = flagsQuery.data?.page ?? list.page;
  const owners = ownersQuery.data?.items ?? [];

  const filtersActive = Boolean(list.q.trim() || list.state || list.environment);
  // A filtered board with no rows is "nothing matches", never "the board has no flags".
  const noMatchesQuery = filtersActive ? list.q.trim() || "the current filters" : undefined;

  const [panelMode, setPanelMode] = useState<"create" | "edit" | null>(null);
  const [editingRow, setEditingRow] = useState<FlagRow | null>(null);
  const [form, setForm] = useState<FlagForm>(EMPTY_FORM);
  const [baseline, setBaseline] = useState<FlagForm>(EMPTY_FORM);
  const [fieldErrors, setFieldErrors] = useState<Partial<Record<FieldKey, string>>>({});
  const [formError, setFormError] = useState<string | null>(null);

  const [confirmDiscard, setConfirmDiscard] = useState(false);
  const [pendingRow, setPendingRow] = useState<FlagRow | null>(null);

  const [deleteTarget, setDeleteTarget] = useState<FlagRow | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const isDirty = panelMode !== null && serializeForm(form) !== serializeForm(baseline);

  const clearErrors = () => {
    setFieldErrors({});
    setFormError(null);
  };

  const openCreate = () => {
    setForm(EMPTY_FORM);
    setBaseline(EMPTY_FORM);
    setEditingRow(null);
    clearErrors();
    setPanelMode("create");
  };

  const openEdit = (row: FlagRow) => {
    const next = formOf(row);
    setForm(next);
    setBaseline(next);
    setEditingRow(row);
    clearErrors();
    setPanelMode("edit");
  };

  const closePanel = () => {
    setPanelMode(null);
    setEditingRow(null);
    setConfirmDiscard(false);
    setPendingRow(null);
  };

  /** Closing the panel keeps unsaved edits until the operator chooses to discard them. */
  const attemptClose = () => {
    if (isDirty) {
      setPendingRow(null);
      setConfirmDiscard(true);
      return;
    }
    closePanel();
  };

  /** SidePanel's own dismissal path (its close control, Escape or the narrow backdrop). */
  const beforeClose = () => {
    if (isDirty) {
      setPendingRow(null);
      setConfirmDiscard(true);
      return false;
    }
    return true;
  };

  /** The row's edit control also toggles: activating the control that opened the panel closes it. */
  const onRowEdit = (row: FlagRow) => {
    if (panelMode === "edit" && editingRow?.id === row.id) {
      attemptClose();
      return;
    }
    if (isDirty) {
      setPendingRow(row);
      setConfirmDiscard(true);
      return;
    }
    openEdit(row);
  };

  const confirmDiscardEdits = () => {
    const row = pendingRow;
    setConfirmDiscard(false);
    setPendingRow(null);
    if (row) openEdit(row);
    else closePanel();
  };

  const refreshWrites = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["flags"] }),
      queryClient.invalidateQueries({ queryKey: ["audit"] }),
      queryClient.invalidateQueries({ queryKey: ["dashboard"] }),
    ]);
  };

  const createMutation = useMutation({
    mutationFn: (input: FeatureFlagInput) => createFlag(input),
  });
  const updateMutation = useMutation({
    mutationFn: (vars: { id: string; patch: Partial<FeatureFlagInput> }) => updateFlag(vars.id, vars.patch),
  });
  const deleteMutation = useMutation({ mutationFn: (id: string) => deleteFlag(id) });

  /** A refusal is shown against the field it names, and the entered values are kept. */
  const reportWriteError = (error: unknown) => {
    if (isApiError(error) && error.code === "conflict") {
      const field = fieldForMessage(error.message);
      if (field) {
        setFieldErrors((current) => ({ ...current, [field]: error.message }));
        return;
      }
    }
    setFormError(error instanceof Error ? error.message : "The flag could not be saved.");
  };

  const submit = async () => {
    clearErrors();

    // The range and whole-number rule belong to the API; this only stops an empty or non-numeric
    // entry from coercing to a silent zero.
    const rolloutRaw = form.rollout.trim();
    if (rolloutRaw === "" || !/^\d+(\.\d+)?$/.test(rolloutRaw)) {
      setFieldErrors({ rollout: "Rollout must be a whole number between 0 and 100." });
      return;
    }

    const input: FeatureFlagInput = {
      key: form.key.trim(),
      name: form.name.trim(),
      description: form.description.trim(),
      state: form.state,
      rollout: Number(rolloutRaw),
      environments: form.environments,
      ownerId: form.ownerId,
    };

    if (panelMode === "create") {
      try {
        await createMutation.mutateAsync(input);
        await refreshWrites();
        closePanel();
        toast.done(`${input.key} created`, "The flag is on the board and one audit event was recorded.");
      } catch (error) {
        reportWriteError(error);
      }
      return;
    }

    if (panelMode !== "edit" || !editingRow) return;

    // Only the fields that actually changed are recorded.
    const patch = patchOf(baseline, input);
    if (Object.keys(patch).length === 0) {
      closePanel();
      toast.problem("Nothing changed", "No field differs from the saved flag, so nothing was written.");
      return;
    }

    try {
      await updateMutation.mutateAsync({ id: editingRow.id, patch });
      await refreshWrites();
      closePanel();
      toast.done(`${input.key} updated`, "The directory shows the saved values and one audit event was recorded.");
    } catch (error) {
      reportWriteError(error);
    }
  };

  const requestDelete = (row: FlagRow) => {
    setDeleteError(null);
    setDeleteTarget(row);
  };

  const confirmDelete = async () => {
    if (!deleteTarget) return;
    setDeleteError(null);
    const remaining = Math.max(0, total - 1);
    const target = deleteTarget;
    try {
      await deleteMutation.mutateAsync(target.id);
      await refreshWrites();
      const key = target.key;
      setDeleteTarget(null);
      // A flag cannot be edited after it has been deleted: close the panel if it was showing it.
      if (editingRow?.id === target.id) closePanel();
      toast.done(
        `${key} deleted`,
        `${plural(remaining, "flag")} remain on the board and one audit event recorded who deleted it.`,
      );
    } catch (error) {
      setDeleteError(error instanceof Error ? error.message : "The flag could not be deleted.");
    }
  };

  const onSort = (columnId: string) => {
    setList({
      sort: columnId,
      dir: list.sort === columnId && list.dir === "asc" ? "desc" : "asc",
      page: 1,
    });
  };

  const clearFilters = () => setList({ q: "", state: "", environment: "", page: 1 });

  const columns: GridColumn<FlagRow>[] = [
    {
      id: "name",
      header: "Flag",
      sortable: true,
      hideable: false,
      width: "11rem",
      cell: (row) => (
        <div className="flex min-w-0 max-w-[11rem] flex-col">
          <span className="truncate text-body font-semibold text-ink" title={row.name}>
            {row.name}
          </span>
          <span className="truncate text-[0.625rem] text-ink-muted" title={row.description || "No description"}>
            {row.description || "No description"}
          </span>
        </div>
      ),
    },
    {
      id: "key",
      header: "Key",
      sortable: false,
      width: "8rem",
      cell: (row) => (
        <span className="block max-w-[8rem] truncate font-mono text-[0.6875rem] text-ink" title={row.key}>
          {row.key}
        </span>
      ),
    },
    {
      id: "state",
      header: "State",
      sortable: true,
      width: "5.5rem",
      cell: (row) => <StatusMagnet status={row.state} />,
    },
    {
      id: "rollout",
      header: "Rollout",
      sortable: true,
      align: "right",
      width: "4rem",
      cell: (row) => <span className="num text-ink">{row.rollout}%</span>,
    },
    {
      id: "environments",
      header: "Environments",
      sortable: false,
      width: "6.5rem",
      cell: (row) => {
        // One line per row, and a bounded width: the directory shows the first two in their short
        // form and counts the rest, with every full name in the tooltip. Letting the badges wrap made
        // row height vary from 52px to 93px on one page, and full labels pushed the table past its
        // container at a 1280px viewport, which no other grid in the panel does.
        const shown = row.environments.slice(0, 2);
        const rest = row.environments.slice(2);
        return (
          <span className="flex items-center gap-1 whitespace-nowrap">
            {row.environments.length === 0 ? <span className="text-ink-muted">None</span> : null}
            {shown.map((environment) => (
              <Badge key={environment} tone="quiet" title={envFull(environment)}>
                {envShort(environment)}
              </Badge>
            ))}
            {rest.length > 0 ? (
              <span
                className="num text-[0.6875rem] text-ink-muted"
                title={rest.map((environment) => envFull(environment)).join(", ")}
              >
                +{rest.length}
              </span>
            ) : null}
          </span>
        );
      },
    },
    {
      id: "owner",
      header: "Owner",
      sortable: true,
      width: "7rem",
      cell: (row) => (
        <span className="block max-w-[7rem] truncate text-ink" title={row.owner?.email ?? row.ownerId}>
          {row.owner?.name ?? "—"}
        </span>
      ),
    },
    {
      id: "updatedAt",
      header: "Last change",
      sortable: true,
      align: "right",
      width: "6rem",
      cell: (row) => (
        <span className="num" title={dateTime(row.updatedAt)}>
          {relativeTime(row.updatedAt)}
        </span>
      ),
    },
    {
      id: "actions",
      header: "Actions",
      sortable: false,
      hideable: false,
      align: "right",
      width: "5rem",
      cell: (row) => (
        <Gate permission="flags.write">
          <span className="flex items-center justify-end gap-1">
            <IconButton
              label={
                panelMode === "edit" && editingRow?.id === row.id
                  ? `Close the editor for ${row.name}`
                  : `Edit ${row.name}`
              }
              onClick={() => onRowEdit(row)}
            >
              <Pencil size={13} />
            </IconButton>
            <IconButton label={`Delete ${row.name}`} onClick={() => requestDelete(row)}>
              <Trash2 size={13} />
            </IconButton>
          </span>
        </Gate>
      ),
    },
  ];

  return (
    <>
      <PageHeader
        title="Feature flags"
        count={plural(total, "flag")}
        description="Every flag on the board with the state, rollout and environments it is running in. Create and edit beside the directory; deletion is confirmed against the flag's key."
        crumbs={[{ label: "Board", to: "/" }, { label: "Feature flags" }]}
        actions={
          <Gate permission="flags.write">
            <Button variant="primary" icon={<Plus size={13} />} onClick={openCreate}>
              New flag
            </Button>
          </Gate>
        }
      />

      <div className="min-w-0 px-3 py-3 sm:px-5">
        <Panel>
          <Toolbar>
            <GridSearch value={list.q} onChange={(q) => setList({ q })} placeholder="Search by name or key" />
            <div className="w-40">
              <SelectInput
                label="State"
                value={list.state}
                onChange={(event) => setList({ state: event.target.value })}
                options={[
                  { value: "", label: "All states" },
                  ...FLAG_STATES.map((state) => ({ value: state, label: STATE_LABEL[state] })),
                ]}
              />
            </div>
            <div className="w-48">
              <SelectInput
                label="Environment"
                value={list.environment}
                onChange={(event) => setList({ environment: event.target.value })}
                options={[
                  { value: "", label: "All environments" },
                  ...FLAG_ENVIRONMENTS.map((environment) => ({
                    value: environment,
                    label: envFull(environment),
                  })),
                ]}
              />
            </div>
            {filtersActive ? (
              <Button variant="quiet" size="sm" onClick={clearFilters}>
                Clear filters
              </Button>
            ) : null}
          </Toolbar>

          <DataGrid
            rows={rows}
            columns={columns}
            rowKey={(row) => row.id}
            caption="Feature flags"
            total={total}
            page={page}
            pageSize={list.pageSize}
            sort={list.sort}
            dir={list.dir === "desc" ? "desc" : "asc"}
            onSort={onSort}
            onPage={(page) => setList({ page })}
            onPageSize={(pageSize) => setList({ pageSize })}
            isLoading={flagsQuery.isLoading}
            error={flagsQuery.error}
            onRetry={() => void flagsQuery.refetch()}
            emptyTitle="No feature flags yet"
            emptyBody="The board has no flags. Create the first one to control a rollout without shipping code."
            emptyAction={
              <Gate permission="flags.write">
                <Button variant="outline" size="sm" icon={<Plus size={13} />} onClick={openCreate}>
                  New flag
                </Button>
              </Gate>
            }
            noMatchesQuery={noMatchesQuery}
            onClearFilters={clearFilters}
            sweepKey={sweepKey}
          />
        </Panel>
      </div>

      <Gate permission="flags.write">
        <SidePanel
          open={panelMode !== null}
          onClose={closePanel}
          onBeforeClose={beforeClose}
          title={panelMode === "create" ? "New feature flag" : editingRow ? `Edit ${editingRow.name}` : "Edit feature flag"}
          description={
            panelMode === "create"
              ? "The flag appears in the directory immediately. Every change is recorded in the audit record."
              : `Editing ${editingRow?.key ?? ""}. Only the fields you change are written; the change is recorded in the audit record.`
          }
          footer={
            <>
              <Button
                variant="quiet"
                onClick={attemptClose}
                disabled={createMutation.isPending || updateMutation.isPending}
              >
                Cancel
              </Button>
              <Button
                variant="primary"
                type="submit"
                form="flag-form"
                busy={createMutation.isPending || updateMutation.isPending}
              >
                {panelMode === "create" ? "Create flag" : "Save changes"}
              </Button>
            </>
          }
        >
          <form
            id="flag-form"
            className="flex flex-col gap-3"
            onSubmit={(event) => {
              event.preventDefault();
              void submit();
            }}
          >
            <TextInput
              label="Key"
              value={form.key}
              onChange={(event) => setForm((current) => ({ ...current, key: event.target.value }))}
              error={fieldErrors.key}
              hint="Lowercase words separated by single hyphens or dots, for example checkout.express-lane."
              className="font-mono"
              autoComplete="off"
              autoCapitalize="none"
              spellCheck={false}
            />
            <TextInput
              label="Name"
              value={form.name}
              onChange={(event) => setForm((current) => ({ ...current, name: event.target.value }))}
              error={fieldErrors.name}
              autoComplete="off"
            />
            <TextArea
              label="Description"
              value={form.description}
              onChange={(event) => setForm((current) => ({ ...current, description: event.target.value }))}
              error={fieldErrors.description}
              hint="What the flag controls, in one line."
              rows={3}
            />
            <SelectInput
              label="State"
              value={form.state}
              onChange={(event) => setForm((current) => ({ ...current, state: event.target.value as FlagState }))}
              options={FLAG_STATES.map((state) => ({ value: state, label: STATE_LABEL[state] }))}
              hint="On serves everyone, off serves no one, gradual serves the rollout percentage."
            />
            <TextInput
              label="Rollout"
              inputMode="numeric"
              value={form.rollout}
              onChange={(event) => setForm((current) => ({ ...current, rollout: event.target.value }))}
              error={fieldErrors.rollout}
              hint="A whole number between 0 and 100."
              className="font-mono"
            />

            <fieldset className="flex flex-col gap-1">
              <legend className="label text-ink-muted">Environments</legend>
              <div className="flex flex-col gap-1.5">
                {FLAG_ENVIRONMENTS.map((environment) => (
                  <Checkbox
                    key={environment}
                    label={envFull(environment)}
                    checked={form.environments.includes(environment)}
                    onChange={(next) =>
                      setForm((current) => ({
                        ...current,
                        environments: next
                          ? [...current.environments, environment]
                          : current.environments.filter((value) => value !== environment),
                      }))
                    }
                  />
                ))}
              </div>
              {fieldErrors.environments ? (
                <p className="text-[0.6875rem] font-semibold text-attention">{fieldErrors.environments}</p>
              ) : (
                <p className="text-[0.6875rem] text-ink-muted">A flag has to apply to at least one environment.</p>
              )}
            </fieldset>

            <SelectInput
              label="Owner"
              value={form.ownerId}
              onChange={(event) => setForm((current) => ({ ...current, ownerId: event.target.value }))}
              options={[
                { value: "", label: ownersQuery.isLoading ? "Loading people…" : "Choose an owner" },
                ...owners.map((owner) => ({ value: owner.id, label: owner.name })),
              ]}
              error={fieldErrors.owner}
              hint="The person who owns this flag's rollout."
            />

            {formError ? (
              <p role="alert" className="text-[0.6875rem] font-semibold text-attention">
                {formError}
              </p>
            ) : null}
          </form>
        </SidePanel>
      </Gate>

      <Gate permission="flags.write">
        <ConfirmDialog
          open={Boolean(deleteTarget)}
          onClose={() => {
            if (!deleteMutation.isPending) {
              setDeleteTarget(null);
              setDeleteError(null);
            }
          }}
          onConfirm={() => void confirmDelete()}
          title={deleteTarget ? `Delete the flag “${deleteTarget.key}”?` : "Delete this flag?"}
          description={
            deleteTarget
              ? `“${deleteTarget.key}” will be removed from every environment. This cannot be undone, and the deletion is recorded in the audit record.`
              : ""
          }
          confirmLabel="Delete flag"
          requireTyped={deleteTarget?.key}
          typedLabel={deleteTarget ? `Type “${deleteTarget.key}” to confirm` : undefined}
          busy={deleteMutation.isPending}
          error={deleteError ?? undefined}
        />
      </Gate>

      <ConfirmDialog
        open={confirmDiscard}
        onClose={() => {
          setConfirmDiscard(false);
          setPendingRow(null);
        }}
        onConfirm={confirmDiscardEdits}
        title="Discard your edits?"
        description="The changes you have made in the panel will be lost. The flag on the board is unchanged."
        confirmLabel="Discard edits"
      />
    </>
  );
}
