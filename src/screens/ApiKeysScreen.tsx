/** The API-key directory.
 *
 *  One board and one dialog: the directory is the whole surface, and issuing, editing and revoking
 *  all happen in a modal over it. Issue and Edit are the same component, so the form, the validation
 *  and the refusal handling exist once. The secret a key is issued with is shown inside that dialog
 *  and nowhere else — it is never persisted, so it cannot be read back afterwards. */

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Copy, KeyRound, Lock, MoreHorizontal } from "lucide-react";
import { createApiKey, DEFAULT_PAGE_SIZE, listApiKeys, revokeApiKey, updateApiKey } from "../mock/api";
import { API_ENVIRONMENTS, API_SCOPES } from "../mock/types";
import type { ApiEnvironment, ApiKey, ApiKeyInput, ApiKeyStatus, ListQuery } from "../mock/types";
import { Gate, useAccess } from "../access/access";
import { RequirePermission } from "../components/RequirePermission";
import { PageHeader } from "../shell/PageHeader";
import { useScope } from "../shell/scope";
import { useListState } from "../lib/urlState";
import { DataGrid, GridSearch } from "../components/grid/DataGrid";
import type { GridColumn } from "../components/grid/DataGrid";
import {
  Badge,
  Button,
  Checkbox,
  ConfirmDialog,
  Dialog,
  FieldRow,
  Menu,
  Panel,
  SelectInput,
  StatusMagnet,
  TextInput,
  Toolbar,
  useToast,
} from "../components/ui";
import { absoluteDate, dateTime, plural, relativeTime } from "../lib/format";

const KEY_STATES: ApiKeyStatus[] = ["active", "revoked"];

const STATUS_LABEL: Record<ApiKeyStatus, string> = {
  active: "Active",
  revoked: "Revoked",
};

const ENVIRONMENT_LABEL: Record<ApiEnvironment, string> = {
  live: "Live",
  test: "Test",
};

function scopeLabel(id: string): string {
  return API_SCOPES.find((scope) => scope.id === id)?.label ?? id;
}

type Editor = { mode: "create" } | { mode: "edit"; key: ApiKey };

/** One dialog for both halves of the write path. `target` of null issues a new key; a key edits it,
 *  read-only once it has been revoked. The generated secret lives only in this component's state and
 *  is discarded the moment the dialog closes. */
function KeyDialog({
  target,
  onClose,
  onChanged,
}: {
  target: ApiKey | null;
  onClose: () => void;
  onChanged: () => Promise<void>;
}) {
  const { can } = useAccess();
  const toast = useToast();
  const revoked = target?.status === "revoked";
  const readOnly = Boolean(revoked) || !can("apikeys.write");

  const [name, setName] = useState(target?.name ?? "");
  const [scopes, setScopes] = useState<string[]>(target ? [...target.scopes] : []);
  const [environment, setEnvironment] = useState<ApiEnvironment>(target?.environment ?? "test");
  const [errors, setErrors] = useState<{ name?: string; scopes?: string }>({});
  const [secret, setSecret] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  const createMutation = useMutation({ mutationFn: (input: ApiKeyInput) => createApiKey(input) });
  const updateMutation = useMutation({
    mutationFn: (input: { id: string; patch: Partial<ApiKeyInput> }) => updateApiKey(input.id, input.patch),
  });
  const busy = createMutation.isPending || updateMutation.isPending;

  const toggleScope = (id: string, on: boolean) => {
    setErrors((current) => ({ ...current, scopes: undefined }));
    setScopes((current) => (on ? [...current, id] : current.filter((value) => value !== id)));
  };

  const copySecret = async () => {
    if (!secret) return;
    try {
      await navigator.clipboard.writeText(secret);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      toast.problem(
        "Could not copy the secret",
        "Select it and copy it by hand — it will not be shown again once this dialog is closed.",
      );
    }
  };

  const submit = async () => {
    if (readOnly) return;
    const trimmed = name.trim();
    const next: { name?: string; scopes?: string } = {};
    if (trimmed.length < 2) next.name = "A key name of at least two characters is required.";
    if (scopes.length === 0) next.scopes = "A key needs at least one scope — a key that may do nothing is not useful.";
    if (next.name || next.scopes) {
      setErrors(next);
      return;
    }
    setErrors({});

    try {
      if (!target) {
        const result = await createMutation.mutateAsync({ name: trimmed, scopes, environment });
        await onChanged();
        setSecret(result.secret);
        toast.done(
          `“${result.key.name}” issued`,
          "The key is on the directory. Its secret is shown below and will not be shown again.",
        );
        return;
      }

      // Only the fields that actually differ are sent, so the audit event names the change.
      const patch: Partial<ApiKeyInput> = {};
      if (trimmed !== target.name) patch.name = trimmed;
      if ([...scopes].sort().join(",") !== [...target.scopes].sort().join(",")) patch.scopes = scopes;
      if (environment !== target.environment) patch.environment = environment;
      if (Object.keys(patch).length === 0) {
        onClose();
        toast.problem("Nothing changed", `“${target.name}” already has those values, so nothing was recorded.`);
        return;
      }

      const updated = await updateMutation.mutateAsync({ id: target.id, patch });
      await onChanged();
      toast.done(`“${updated.name}” updated`, "The change is recorded in the audit record.");
      onClose();
    } catch (error) {
      // The API writes its refusals for the operator; they land against the field that caused them
      // and the dialog stays open so the input can be corrected.
      const message = error instanceof Error ? error.message : "The key was refused.";
      if (/scope/i.test(message)) setErrors({ scopes: message });
      else setErrors({ name: message });
    }
  };

  const displayName = target?.name ?? "—";
  const displayEnvironment = target ? ENVIRONMENT_LABEL[target.environment] : "—";
  const displayScopes = target?.scopes ?? [];

  let title: string;
  let description: string;
  if (secret) {
    title = "Key issued — copy the secret";
    description =
      "This is the only time the secret is shown. It is not stored anywhere, so it cannot be recovered once this dialog is closed.";
  } else if (!target) {
    title = "Issue an API key";
    description =
      "The secret is generated when you issue the key and shown once. Only a fingerprint and the last four characters are kept.";
  } else if (revoked) {
    title = `Revoked key: ${displayName}`;
    description =
      "This key has been revoked. It is read-only and there is no path to reactivate it. The secret is not stored and cannot be shown again.";
  } else {
    title = `Edit ${displayName}`;
    description =
      "Change the name, the scopes or the environment. Saving records one audit event naming what changed. The secret is not stored and cannot be shown again.";
  }

  return (
    <Dialog
      open
      onClose={() => {
        if (!busy) onClose();
      }}
      title={title}
      description={description}
      width="md"
      footer={
        secret ? (
          <Button variant="primary" onClick={onClose}>
            Done
          </Button>
        ) : readOnly ? (
          <Button variant="primary" onClick={onClose}>
            Close
          </Button>
        ) : (
          <>
            <Button variant="quiet" onClick={onClose} disabled={busy}>
              Cancel
            </Button>
            <Button variant="primary" onClick={() => void submit()} busy={busy}>
              {target ? "Save changes" : "Issue key"}
            </Button>
          </>
        )
      }
    >
      {secret ? (
        <div className="flex flex-col gap-3">
          <p className="text-body text-ink">
            Copy the secret now. It is shown here once and is not stored anywhere, so it cannot be
            recovered after this dialog is closed.
          </p>
          <div className="flex items-stretch gap-1.5">
            <code className="min-w-0 flex-1 overflow-x-auto rounded-chip border border-rule-strong bg-ground px-2 py-2 font-mono text-body text-ink">
              {secret}
            </code>
            <Button
              variant="outline"
              size="sm"
              icon={copied ? <Check size={14} /> : <Copy size={14} />}
              onClick={() => void copySecret()}
            >
              {copied ? "Copied" : "Copy"}
            </Button>
          </div>
        </div>
      ) : readOnly ? (
        <div className="flex flex-col gap-2">
          <p className="flex items-start gap-2 border border-rule bg-ground px-2 py-1.5 text-[0.6875rem] text-ink-muted">
            <Lock size={13} aria-hidden="true" className="mt-0.5 shrink-0 text-ink" />
            <span>Revoked keys are read-only. There is no control anywhere to reactivate one.</span>
          </p>
          <FieldRow label="Name">{displayName}</FieldRow>
          <FieldRow label="Environment">{displayEnvironment}</FieldRow>
          <FieldRow label="Scopes">
            <span className="flex flex-wrap items-center gap-1">
              {displayScopes.map((id) => (
                <Badge key={id} tone="quiet" mono>
                  {id}
                </Badge>
              ))}
            </span>
          </FieldRow>
          <p className="text-[0.6875rem] text-ink-muted">The secret is not stored, so it cannot be shown again.</p>
        </div>
      ) : (
        <form
          className="flex flex-col gap-3"
          onSubmit={(event) => {
            event.preventDefault();
            void submit();
          }}
        >
          <TextInput
            label="Key name"
            value={name}
            onChange={(event) => {
              setName(event.target.value);
              setErrors((current) => ({ ...current, name: undefined }));
            }}
            error={errors.name}
            hint="A name you will recognise in the directory, for example “CI pipeline”."
            autoComplete="off"
          />
          <fieldset className="flex flex-col gap-1" aria-describedby={errors.scopes ? "key-scopes-error" : undefined}>
            <legend className="label text-ink-muted">Scopes</legend>
            <div className="flex flex-col gap-1.5">
              {API_SCOPES.map((scope) => (
                <Checkbox
                  key={scope.id}
                  label={scope.label}
                  hint={scope.id}
                  checked={scopes.includes(scope.id)}
                  onChange={(on) => toggleScope(scope.id, on)}
                />
              ))}
            </div>
            {errors.scopes ? (
              <p id="key-scopes-error" role="alert" className="text-[0.6875rem] font-semibold text-attention">
                {errors.scopes}
              </p>
            ) : null}
          </fieldset>
          <SelectInput
            label="Environment"
            value={environment}
            onChange={(event) => setEnvironment(event.target.value as ApiEnvironment)}
            options={API_ENVIRONMENTS.map((value) => ({ value, label: ENVIRONMENT_LABEL[value] }))}
            hint="A key can only be used against the environment it was issued for."
          />
        </form>
      )}
    </Dialog>
  );
}

/** One row's menu. A revoked key offers only a read-only view; the write actions exist solely behind
 *  the `apikeys.write` gate, so an identity that may not write is never shown a control that can only
 *  fail. */
function KeyActions({
  row,
  onOpen,
  onRevoke,
}: {
  row: ApiKey;
  onOpen: (key: ApiKey) => void;
  onRevoke: (key: ApiKey) => void;
}) {
  const view = { id: "view", label: "View key", hint: "Read-only", onSelect: () => onOpen(row) };
  const writeItems = [
    { id: "edit", label: "Edit key", onSelect: () => onOpen(row) },
    { id: "revoke", label: "Revoke key", destructive: true, onSelect: () => onRevoke(row) },
  ];
  const label = `Actions for ${row.name}`;

  return (
    <div className="flex justify-end">
      <Gate
        permission="apikeys.write"
        fallback={<Menu label={label} trigger={<MoreHorizontal size={14} />} items={[view]} />}
      >
        <Menu
          label={label}
          trigger={<MoreHorizontal size={14} />}
          items={row.status === "revoked" ? [view] : writeItems}
        />
      </Gate>
    </div>
  );
}

export function ApiKeysScreen() {
  const scope = useScope();
  const toast = useToast();
  const queryClient = useQueryClient();

  const [state, setState] = useListState({
    page: 1,
    pageSize: DEFAULT_PAGE_SIZE,
    sort: "createdAt",
    dir: "desc",
    q: "",
    status: "",
    environment: "",
  });

  const [editor, setEditor] = useState<Editor | null>(null);
  const [revoking, setRevoking] = useState<ApiKey | null>(null);
  const [revokeError, setRevokeError] = useState<string | null>(null);

  const uiDir: "asc" | "desc" = state.dir === "asc" ? "asc" : "desc";
  const query: ListQuery = {
    page: state.page,
    pageSize: state.pageSize,
    sort: state.sort || undefined,
    dir: uiDir,
    q: state.q.trim() || undefined,
    status: state.status || undefined,
    environment: state.environment || undefined,
  };

  const keysQuery = useQuery({ queryKey: ["api-keys", query], queryFn: () => listApiKeys(query) });
  const rows = keysQuery.data?.items ?? [];
  const total = keysQuery.data?.total ?? 0;
  const page = keysQuery.data?.page ?? state.page;

  const filtersActive = Boolean(state.q.trim() || state.status || state.environment);
  // A filtered board with no rows is "nothing matches", never "no keys exist".
  const noMatchesQuery = filtersActive ? state.q.trim() || "the current filters" : undefined;

  const revokeMutation = useMutation({ mutationFn: (id: string) => revokeApiKey(id) });

  const refreshWrites = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["api-keys"] }),
      queryClient.invalidateQueries({ queryKey: ["audit"] }),
      queryClient.invalidateQueries({ queryKey: ["dashboard"] }),
    ]);
  };

  const onSort = (columnId: string) => {
    setState({
      sort: columnId,
      dir: state.sort === columnId && state.dir === "asc" ? "desc" : "asc",
      page: 1,
    });
  };

  const clearFilters = () => setState({ q: "", status: "", environment: "", page: 1 });

  const confirmRevoke = async () => {
    if (!revoking) return;
    setRevokeError(null);
    try {
      await revokeMutation.mutateAsync(revoking.id);
      await refreshWrites();
      toast.done(
        `“${revoking.name}” revoked`,
        "It can no longer authenticate. The key stays on the board as a revoked record, and nothing reactivates it.",
      );
      setRevoking(null);
    } catch (error) {
      setRevokeError(error instanceof Error ? error.message : "The key was not revoked.");
    }
  };

  const columns: GridColumn<ApiKey>[] = [
    {
      id: "name",
      header: "Key",
      sortable: true,
      hideable: false,
      width: "18rem",
      cell: (row) => (
        <span className="min-w-0">
          <span className="block truncate text-body font-semibold text-ink" title={row.name}>
            {row.name}
          </span>
          <span
            className="block truncate font-mono text-[0.625rem] text-ink-muted"
            title={`${row.fingerprint} · ends ${row.lastFour}`}
          >
            {row.fingerprint}
          </span>
        </span>
      ),
    },
    {
      id: "scopes",
      header: "Scopes",
      sortable: false,
      width: "7rem",
      cell: (row) => (
        <span className="num" title={row.scopes.map(scopeLabel).join(", ")}>
          {plural(row.scopes.length, "scope")}
        </span>
      ),
    },
    {
      id: "environment",
      header: "Environment",
      sortable: true,
      width: "8rem",
      cell: (row) => (
        <span title={`Issued for ${ENVIRONMENT_LABEL[row.environment]}`}>
          <Badge tone="quiet" mono>
            {row.environment}
          </Badge>
        </span>
      ),
    },
    {
      id: "status",
      header: "State",
      sortable: true,
      width: "6rem",
      cell: (row) => <StatusMagnet status={row.status} />,
    },
    {
      id: "createdAt",
      header: "Created",
      sortable: true,
      align: "right",
      width: "8rem",
      cell: (row) => (
        <span className="num" title={dateTime(row.createdAt)}>
          {absoluteDate(row.createdAt)}
        </span>
      ),
    },
    {
      id: "lastUsedAt",
      header: "Last used",
      sortable: true,
      align: "right",
      width: "8rem",
      cell: (row) => (
        <span className="num" title={row.lastUsedAt ? dateTime(row.lastUsedAt) : "Never used"}>
          {row.lastUsedAt ? relativeTime(row.lastUsedAt) : "Never"}
        </span>
      ),
    },
    {
      id: "actions",
      header: "Actions",
      sortable: false,
      align: "right",
      hideable: false,
      width: "5rem",
      cell: (row) => (
        <KeyActions
          row={row}
          onOpen={(key) => setEditor({ mode: "edit", key })}
          onRevoke={(key) => {
            setRevokeError(null);
            setRevoking(key);
          }}
        />
      ),
    },
  ];

  return (
    <RequirePermission permission="apikeys.read" what="The API-key directory" title="API keys">
      <PageHeader
        title="API keys"
        count={plural(total, "key")}
        description="Keys issued to the workspace. A secret is shown once when a key is issued and is never stored, so it cannot be read back."
        crumbs={[{ label: "Board", to: "/" }, { label: "API keys" }]}
        actions={
          <Gate permission="apikeys.write">
            <Button variant="primary" icon={<KeyRound size={13} />} onClick={() => setEditor({ mode: "create" })}>
              Issue key
            </Button>
          </Gate>
        }
      />

      <div className="min-w-0 px-3 py-3 sm:px-5">
        <Panel>
          <Toolbar>
            <GridSearch
              value={state.q}
              onChange={(q) => setState({ q })}
              placeholder="Search by name or last characters"
            />
            <div className="w-40">
              <SelectInput
                label="State"
                value={state.status}
                onChange={(event) => setState({ status: event.target.value })}
                options={[
                  { value: "", label: "All states" },
                  ...KEY_STATES.map((status) => ({ value: status, label: STATUS_LABEL[status] })),
                ]}
              />
            </div>
            <div className="w-40">
              <SelectInput
                label="Environment"
                value={state.environment}
                onChange={(event) => setState({ environment: event.target.value })}
                options={[
                  { value: "", label: "All environments" },
                  ...API_ENVIRONMENTS.map((value) => ({ value, label: ENVIRONMENT_LABEL[value] })),
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
            caption="Issued API keys"
            total={total}
            page={page}
            pageSize={state.pageSize}
            sort={state.sort}
            dir={uiDir}
            onSort={onSort}
            onPage={(next) => setState({ page: next })}
            onPageSize={(size) => setState({ pageSize: size })}
            onRowClick={(row) => setEditor({ mode: "edit", key: row })}
            isLoading={keysQuery.isLoading}
            error={keysQuery.error}
            onRetry={() => void keysQuery.refetch()}
            emptyTitle="No API keys yet"
            emptyBody="No key has been issued to this workspace. Issue one to let a service authenticate; you choose what it may do and which environment it may do it in."
            emptyAction={
              <Gate permission="apikeys.write">
                <Button
                  variant="outline"
                  size="sm"
                  icon={<KeyRound size={13} />}
                  onClick={() => setEditor({ mode: "create" })}
                >
                  Issue key
                </Button>
              </Gate>
            }
            noMatchesQuery={noMatchesQuery}
            onClearFilters={clearFilters}
            sweepKey={scope.sweepKey}
          />
        </Panel>
      </div>

      {editor ? (
        <KeyDialog
          key={editor.mode === "edit" ? editor.key.id : "new-key"}
          target={editor.mode === "edit" ? editor.key : null}
          onClose={() => setEditor(null)}
          onChanged={refreshWrites}
        />
      ) : null}

      {revoking ? (
        <ConfirmDialog
          open
          onClose={() => {
            if (!revokeMutation.isPending) {
              setRevoking(null);
              setRevokeError(null);
            }
          }}
          onConfirm={() => void confirmRevoke()}
          title={`Revoke ${revoking.name}?`}
          description={`${revoking.name} will stop authenticating immediately and cannot be reactivated. Anything using it starts failing with an authentication error. The key stays on the board as a revoked record.`}
          confirmLabel="Revoke key"
          requireTyped={revoking.name}
          typedLabel={`Type “${revoking.name}” to confirm`}
          busy={revokeMutation.isPending}
          error={revokeError ?? undefined}
        />
      ) : null}
    </RequirePermission>
  );
}
