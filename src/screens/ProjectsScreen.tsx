/** Projects: the registry the dispatcher reads, and the surface that keeps it true.
 *
 *  The list comes from `GET /api/projects`; the dialogs post the config file's own field names and
 *  put a `422` on the input it names. Client-side rules mirror the server's, so the common mistake
 *  costs no round trip — and a field the server refuses still wins, because its message replaces
 *  the local one. A create that succeeds but warns (a `paths.main` that does not exist yet) keeps
 *  the dialog open with the note: the project exists, and the operator still has to clone it.
 *
 *  Each row carries its own actions. Edit reuses the create form with the key pinned — the key is
 *  immutable, so a rename is a delete and a create. Pause flips the DB-owned flag the dispatcher
 *  reads, never the file. Delete counts what it removes from the list row itself (design D7) and
 *  asks for the key to be typed; a live dispatch refuses both with the remedy in the message. */

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pause, Pencil, Play, Plus, Trash2 } from "lucide-react";
import {
  createProject,
  deleteProject,
  getProjects,
  isApiError,
  setProjectOnhold,
  updateProject,
} from "../lib/fleet";
import type { ApiError, CreateProjectInput, Project, WaitingPanel } from "../lib/fleet";
import {
  Badge,
  Button,
  ConfirmDialog,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  EmptyState,
  ErrorState,
  LoadingRows,
  Panel,
  PanelHead,
  SelectInput,
  StatusMagnet,
  TextInput,
  useToast,
} from "../components/ui";
import { PageHeader } from "../shell/PageHeader";
import { DraftPreflight, ProjectPreflight, RuntimePicker } from "../components/ProjectPreflight";

/** The server's key rule, verbatim (`config.ValidateProject`): lowercase, 1–32 characters, starting
 *  with a letter or a digit. */
const KEY_RE = /^[a-z0-9][a-z0-9-]{0,31}$/;

/** The four the server validates against. The picker is driven by `GET /api/runtimes`; this list is
 *  only the client-side mirror, so a typo costs no round trip. */
const RUNTIMES = ["pi", "omp", "codex", "claude"];

const FORGES = [
  { value: "github", label: "GitHub" },
  { value: "gitlab", label: "GitLab" },
];

/** Field keys this dialog can paint. Anything else the service names is shown as a dialog-level
 *  message rather than silently dropped. */
const FIELD_KEYS: Record<string, true> = {
  key: true,
  name: true,
  forge: true,
  repo: true,
  repo_url: true,
  forge_host: true,
  default_branch: true,
  rigs: true,
  concurrency_cap: true,
  "paths.main": true,
  "paths.worktrees": true,
};

type Draft = {
  key: string;
  name: string;
  forge: string;
  repo: string;
  repo_url: string;
  forge_host: string;
  default_branch: string;
  rigs: string;
  concurrency_cap: string;
  runtime: string;
  paths_main: string;
  paths_worktrees: string;
  reviewer: string;
};

/** The error key each draft field owns. Only the two path fields differ, but they must be spelled
 *  out: deriving it by rewriting an underscore would have `concurrency_cap` clear
 *  `concurrency.cap`. */
const ERROR_KEY: Record<keyof Draft, string> = {
  key: "key",
  name: "name",
  forge: "forge",
  repo: "repo",
  repo_url: "repo_url",
  forge_host: "forge_host",
  default_branch: "default_branch",
  rigs: "rigs",
  concurrency_cap: "concurrency_cap",
  runtime: "runtime",
  paths_main: "paths.main",
  paths_worktrees: "paths.worktrees",
  reviewer: "reviewer",
};

const BLANK: Draft = {
  key: "",
  name: "",
  forge: "github",
  repo: "",
  repo_url: "",
  forge_host: "",
  default_branch: "main",
  rigs: "",
  concurrency_cap: "2",
  runtime: "omp",
  paths_main: "",
  paths_worktrees: "",
  reviewer: "",
};

/** The draft as the wire shape. Submit and the live panel both use this, so the checks run against
 *  exactly the body the create would send — not a second construction that could drift. */
function draftInput(draft: Draft): CreateProjectInput {
  return {
    key: draft.key.trim(),
    name: draft.name.trim(),
    forge: draft.forge === "gitlab" ? "gitlab" : "github",
    repo: draft.repo.trim(),
    repo_url: draft.repo_url.trim(),
    // Sent as typed, so GitHub with a host is the service's 422 on forge_host —
    // never a silently cleared value (forge-host design D3: the refusal is the fact).
    forge_host: draft.forge_host.trim(),
    default_branch: draft.default_branch.trim(),
    rigs: splitRigs(draft.rigs),
    concurrency_cap: Number(draft.concurrency_cap),
    runtime: draft.runtime,
    paths: {
      main: draft.paths_main.trim(),
      worktrees: draft.paths_worktrees.trim(),
    },
    reviewer: draft.reviewer.trim(),
  };
}

function splitRigs(value: string): string[] {
  return value
    .split(",")
    .map((rig) => rig.trim())
    .filter(Boolean);
}

/** What the delete dialog says, from the list row alone (design D7: one set of numbers, from the
 *  list to the cascade). The rigs are named because deleting a registration does not stop a seat —
 *  it only stops the dispatcher planning work for them. */
function deleteDescription(project: Project): string {
  const rows =
    `${project.items} ${project.items === 1 ? "item" : "items"} and ` +
    `${project.dispatches} ${project.dispatches === 1 ? "dispatch" : "dispatches"} would be removed.`;
  const rigs =
    project.rigs.length > 0
      ? `Rigs ${project.rigs.map((rig) => `“${rig}”`).join(", ")} keep running — only the registration goes.`
      : "No rig is registered on it.";
  return `${rows} ${rigs}`;
}

/** The draft as the edit dialog opens it: the row itself, read back into the form's shape. */
function draftFromProject(project: Project): Draft {
  return {
    key: project.key,
    name: project.name,
    forge: project.forge === "gitlab" ? "gitlab" : "github",
    repo: project.repo,
    repo_url: project.repo_url ?? "",
    forge_host: project.forge_host ?? "",
    default_branch: project.default_branch,
    rigs: project.rigs.join(", "),
    concurrency_cap: String(project.concurrency_cap),
    runtime: project.runtime ?? "omp",
    reviewer: project.reviewer ?? "",
    paths_main: project.paths?.main ?? "",
    paths_worktrees: project.paths?.worktrees ?? "",
  };
}

/** The rules the loader applies, so the common mistake costs no round trip. `existing` is the
 *  registry as listed — the edit dialog passes it without the project being edited, so a rig the
 *  project already holds does not read as a collision with itself — and `editing` skips the key
 *  checks, because the key cannot change on an edit. A file the loader refuses is a registry the
 *  service can no longer list. */
function validate(draft: Draft, existing: Project[], editing: boolean): Record<string, string> {
  const errors: Record<string, string> = {};
  if (editing) {
    if (!draft.key.trim()) errors.key = "A key is required.";
  } else {
    if (!draft.key.trim()) errors.key = "A key is required.";
    else if (!KEY_RE.test(draft.key.trim()))
      errors.key = "Lowercase letters, digits and hyphens only, up to 32 characters, starting with a letter or digit.";
    else if (existing.some((project) => project.key === draft.key.trim()))
      errors.key = "A project with that key is already registered.";
  }
  if (!draft.name.trim()) errors.name = "A name is required.";
  if (!draft.repo.trim()) errors.repo = "A repo is required, as owner/name.";
  if (!draft.default_branch.trim()) errors.default_branch = "A default branch is required.";
  const rigs = splitRigs(draft.rigs);
  if (rigs.length === 0) errors.rigs = "At least one rig is required.";
  else if (new Set(rigs).size !== rigs.length) errors.rigs = "A rig is named twice.";
  else {
    const claimed = existing.find((project) => project.rigs.some((rig) => rigs.includes(rig)));
    if (claimed) {
      const rig = rigs.find((name) => claimed.rigs.includes(name)) ?? rigs[0];
      errors.rigs = `“${rig}” already belongs to ${claimed.key}. A rig may belong to one project.`;
    }
  }
  const cap = Number(draft.concurrency_cap);
  if (!Number.isInteger(cap) || cap < 0) errors.concurrency_cap = "A whole number, zero or more.";
  if (!RUNTIMES.includes(draft.runtime)) errors.runtime = "Choose one of the runtimes this host lists.";
  return errors;
}


/** The project's "waiting on a human" panel (#429). It reports the wait and points at the place a
 *  person answers it — the OpenRig UI. It deliberately has no control that answers: a board that can
 *  approve an agent's prompt can grant an agent anything by accident (design D5).
 *
 *  Three states, and none of them may collapse into another: an unread rig is `unknown`, never an
 *  empty list; a seat whose prompt has no text is still listed, saying the rig gave no question; and
 *  a seat with a question shows the text and how long the app has seen it. */
function WaitingOnAHuman({ panel, projectName }: { panel: WaitingPanel; projectName: string }) {
  return (
    <Panel>
      <PanelHead
        title="Waiting on a human"
        count={panel.known ? panel.seats.length : undefined}
        description={`Seats on ${projectName}'s rigs that have stopped and need a person.`}
        actions={
          panel.ui_url ? (
            <a
              href={panel.ui_url}
              target="_blank"
              rel="noreferrer"
              className="text-[0.6875rem] underline decoration-rule-strong text-ink"
            >
              Answer in the rig UI
            </a>
          ) : null
        }
      />
      {!panel.known ? (
        <p className="flex flex-wrap items-center gap-2 border-t border-rule px-3 py-2 text-[0.6875rem] text-ink">
          <StatusMagnet status="unknown" />
          The rig read failed, so who is waiting is unknown — not clear.
          {panel.reason ? <span className="font-mono text-ink-muted">{panel.reason}</span> : null}
        </p>
      ) : panel.seats.length === 0 ? (
        <p className="border-t border-rule px-3 py-2 text-[0.6875rem] text-ink-muted">
          No seat on this project&apos;s rigs is waiting on a human.
        </p>
      ) : (
        <div className="overflow-x-auto border-t border-rule">
          <table className="w-full border-collapse text-body">
            <thead>
              <tr className="border-b border-rule text-left">
                <th className="label px-3 py-1.5 font-medium text-ink-muted">Seat</th>
                <th className="label px-3 py-1.5 font-medium text-ink-muted">Rig</th>
                <th className="label px-3 py-1.5 font-medium text-ink-muted">Question</th>
                <th className="label px-3 py-1.5 font-medium text-ink-muted">Waiting</th>
              </tr>
            </thead>
            <tbody>
              {panel.seats.map((seat, index) => (
                <tr key={`${seat.seat}-${seat.kind}-${index}`} className="border-b border-rule last:border-0">
                  <td className="px-3 py-[var(--density-row-pad-y)] font-mono text-[0.6875rem] text-ink">
                    {seat.seat}
                  </td>
                  <td className="px-3 py-[var(--density-row-pad-y)] font-mono text-[0.6875rem] text-ink-muted">
                    {seat.rig}
                  </td>
                  <td className="px-3 py-[var(--density-row-pad-y)] text-ink">
                    {seat.question ? seat.question : <span className="text-ink-muted">the rig gave no question</span>}
                    {seat.kind ? <Badge tone="quiet" className="ml-2" mono>{seat.kind}</Badge> : null}
                  </td>
                  <td className="num px-3 py-[var(--density-row-pad-y)] text-ink-muted">
                    {seat.since ?? "—"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          <p className="px-3 py-1.5 text-[0.6875rem] text-ink-muted">
            The board does not answer for you: read the prompt and answer it where the seat is running.
          </p>
        </div>
      )}
    </Panel>
  );
}

export function ProjectsScreen() {
  const queryClient = useQueryClient();
  const toast = useToast();
  const projects = useQuery({ queryKey: ["projects"], queryFn: getProjects });

  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<Draft>(BLANK);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [created, setCreated] = useState<{ project: Project; warnings: string[] } | null>(null);
  // The project whose preflight is open. A row is the entry point: the surface exists to answer
  // "will this dispatch?", and that question belongs to one project, not to the list.
  const [inspecting, setInspecting] = useState<Project | null>(null);
  // The row whose edit dialog is open; null means the dialog (when open) is a create. The same
  // form serves both — the fields are the file's fields either way.
  const [editing, setEditing] = useState<Project | null>(null);
  // The row whose delete dialog is open, and the refusal it last answered.
  const [confirming, setConfirming] = useState<Project | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  // The board lists projects from its own query; refreshing both means a change is there without a
  // reload (`design.md` §6). Every success path below waits on it.
  const refreshBoth = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["projects"] }),
      queryClient.invalidateQueries({ queryKey: ["board"] }),
    ]);
  };

  // A `422` names its field; put each message on the input it belongs to and keep anything else as
  // a dialog-level sentence. Create and edit share it, so the two dialogs cannot drift.
  const applyFieldErrors = (error: ApiError) => {
    const onField: Record<string, string> = {};
    const rest: string[] = [];
    for (const [field, message] of Object.entries(error.fieldErrors)) {
      if (FIELD_KEYS[field]) onField[field] = message;
      else rest.push(message);
    }
    setErrors(onField);
    setFormError(rest.length > 0 ? rest.join(" ") : null);
  };

  const create = useMutation({
    mutationFn: (input: CreateProjectInput) => createProject(input),
    onSuccess: async (response) => {
      await refreshBoth();
      setDraft(BLANK);
      setErrors({});
      setFormError(null);
      const warnings = response.warnings ?? [];
      if (warnings.length > 0) {
        setCreated({ project: response.project, warnings });
        return;
      }
      setOpen(false);
      toast.done(`“${response.project.name}” added`, `${response.project.key} is on the board.`);
    },
    onError: (error) => {
      if (isApiError(error) && Object.keys(error.fieldErrors).length > 0) {
        applyFieldErrors(error);
        return;
      }
      setFormError(isApiError(error) ? error.message : "The service could not be reached.");
    },
  });

  const update = useMutation({
    mutationFn: ({ key, input }: { key: string; input: CreateProjectInput }) =>
      updateProject(key, input),
    onSuccess: async (response) => {
      await refreshBoth();
      setOpen(false);
      setEditing(null);
      setErrors({});
      setFormError(null);
      const changed =
        response.changed.length > 0 ? `changed: ${response.changed.join(", ")}` : "no field changed";
      const warnings = response.warnings ?? [];
      toast.done(
        `“${response.project.name}” updated`,
        warnings.length > 0 ? `${changed}. ${warnings.join(" ")}` : changed,
      );
    },
    onError: (error) => {
      if (isApiError(error) && Object.keys(error.fieldErrors).length > 0) {
        applyFieldErrors(error);
        return;
      }
      // A `409` is dialog-level on purpose: the server's message names the dispatch, the item and
      // the branch, and carries the remedy — pause the project, wait for the row to end, then edit.
      setFormError(isApiError(error) ? error.message : "The service could not be reached.");
    },
  });

  const pause = useMutation({
    mutationFn: (project: Project) => setProjectOnhold(project.key, !project.onhold),
    onSuccess: async (response) => {
      await refreshBoth();
      toast.done(
        response.project.onhold ? `“${response.project.name}” paused` : `“${response.project.name}” resumed`,
        response.project.onhold
          ? "The dispatcher will not plan new work for it."
          : "The dispatcher plans work for it again.",
      );
    },
    onError: (error) => {
      toast.problem(isApiError(error) ? error.message : "The service could not be reached.");
    },
  });

  const remove = useMutation({
    mutationFn: (project: Project) =>
      deleteProject(project.key, project.key).then((response) => ({ project, response })),
    onSuccess: async ({ project, response }) => {
      await refreshBoth();
      setConfirming(null);
      setDeleteError(null);
      toast.done(
        `“${project.name}” deleted`,
        `items=${response.deleted.items}, dispatches=${response.deleted.dispatches}, verdicts=${response.deleted.verdicts} removed.`,
      );
    },
    onError: (error) => {
      // Shown inside the dialog: a live dispatch refuses with the remedy — pause first, wait for
      // the row to end, then delete — and a step failure names the step it stopped at.
      setDeleteError(isApiError(error) ? error.message : "The service could not be reached.");
    },
  });

  const busy = create.isPending || update.isPending;

  const change = (field: keyof Draft, value: string) => {
    setDraft((current) => ({ ...current, [field]: value }));
    const errorKey = ERROR_KEY[field];
    setErrors((current) => {
      if (!current[errorKey]) return current;
      const next = { ...current };
      delete next[errorKey];
      return next;
    });
  };

  const openDialog = () => {
    setEditing(null);
    setDraft(BLANK);
    setErrors({});
    setFormError(null);
    setCreated(null);
    setOpen(true);
  };

  const openEditDialog = (project: Project) => {
    setEditing(project);
    setDraft(draftFromProject(project));
    setErrors({});
    setFormError(null);
    setCreated(null);
    setOpen(true);
  };

  const closeDialog = () => {
    if (busy) return;
    setOpen(false);
    setEditing(null);
    setCreated(null);
    setFormError(null);
    setErrors({});
  };

  const openDeleteDialog = (project: Project) => {
    setConfirming(project);
    setDeleteError(null);
  };

  const submit = () => {
    // The edit compares against the registry without the project itself, so its own rigs and key
    // do not read as collisions with themselves.
    const listed = projects.data?.projects ?? [];
    const others = editing ? listed.filter((project) => project.key !== editing.key) : listed;
    const found = validate(draft, others, !!editing);
    setErrors(found);
    setFormError(null);
    if (Object.keys(found).length > 0) return;
    if (editing) update.mutate({ key: editing.key, input: draftInput(draft) });
    else create.mutate(draftInput(draft));
  };

  const list = projects.data?.projects ?? [];

  return (
    <div className="flex flex-col">
      <PageHeader
        title="Projects"
        count={projects.data ? list.length : undefined}
        description="The registry the dispatcher reads. A project added here is written into config.yaml and mirrored to the board."
        actions={
          <Button variant="primary" icon={<Plus size={14} aria-hidden="true" />} onClick={openDialog}>
            New project
          </Button>
        }
      />
      <div className="px-3 py-3 sm:px-5">
        {inspecting ? (
          <div className="mb-3">
            <div className="flex flex-wrap items-baseline gap-2 pb-1">
              <h2 className="text-body font-semibold text-ink">{inspecting.name}</h2>
              <span className="font-mono text-[0.6875rem] text-ink-muted">{inspecting.key}</span>
              <Button variant="quiet" size="sm" className="ml-auto" onClick={() => setInspecting(null)}>
                Close
              </Button>
            </div>
            <div className="mb-3">
              <WaitingOnAHuman panel={inspecting.waiting} projectName={inspecting.name} />
            </div>
            <Panel>
              <ProjectPreflight projectKey={inspecting.key} />
            </Panel>
          </div>
        ) : null}
        <Panel>
          {projects.isLoading ? (
            <LoadingRows rows={4} />
          ) : projects.error ? (
            <ErrorState
              title="Projects did not load"
              error={projects.error}
              onRetry={() => void projects.refetch()}
            />
          ) : list.length === 0 ? (
            <EmptyState
              title="The project registry is empty"
              body="No project is registered in the config file the service was started with. Add the first one and the board has something to dispatch."
              action={
                <Button variant="outline" icon={<Plus size={14} aria-hidden="true" />} onClick={openDialog}>
                  Add first project
                </Button>
              }
            />
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full border-collapse text-body">
                <thead>
                  <tr className="border-b border-rule text-left">
                    <th className="label px-3 py-1.5 font-medium text-ink-muted">Key</th>
                    <th className="label px-3 py-1.5 font-medium text-ink-muted">Name</th>
                    <th className="label px-3 py-1.5 font-medium text-ink-muted">Forge</th>
                    <th className="label px-3 py-1.5 font-medium text-ink-muted">Repo</th>
                    <th className="label px-3 py-1.5 font-medium text-ink-muted">Rigs</th>
                    <th className="label px-3 py-1.5 text-right font-medium text-ink-muted">Cap</th>
                    <th className="label px-3 py-1.5 text-right font-medium text-ink-muted">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {list.map((project) => (
                    <tr
                      key={project.key}
                      className="cursor-pointer border-b border-rule last:border-0 hover:bg-hover"
                      tabIndex={0}
                      onClick={() => setInspecting(project)}
                      onKeyDown={(event) => {
                        if (event.key === "Enter" || event.key === " ") {
                          event.preventDefault();
                          setInspecting(project);
                        }
                      }}
                    >
                      <td className="px-3 py-[var(--density-row-pad-y)] font-mono text-[0.6875rem] text-ink">
                        {project.key}
                      </td>
                      <td className="px-3 py-[var(--density-row-pad-y)] text-ink">{project.name}</td>
                      <td className="px-3 py-[var(--density-row-pad-y)]">
                        <Badge tone={project.forge === "github" ? "quiet" : "tint"} mono>
                          {project.forge}
                        </Badge>
                      </td>
                      <td className="px-3 py-[var(--density-row-pad-y)] font-mono text-[0.6875rem] text-ink">
                        {project.repo}
                      </td>
                      <td className="px-3 py-[var(--density-row-pad-y)]">
                        <span className="flex flex-wrap items-center gap-1">
                          {project.rigs.map((rig) => (
                            <Badge key={rig} tone="quiet" mono>
                              {rig}
                            </Badge>
                          ))}
                          {project.waiting.known && project.waiting.seats.length > 0 ? (
                            <Badge
                              tone="ink"
                              title={`${project.waiting.seats.length} seat(s) waiting on a human`}
                            >
                              {project.waiting.seats.length} waiting on a human
                            </Badge>
                          ) : null}
                        </span>
                      </td>
                      <td className="num px-3 py-[var(--density-row-pad-y)] text-right text-ink">
                        {project.concurrency_cap}
                      </td>
                      <td
                        className="px-3 py-[var(--density-row-pad-y)] text-right"
                        onClick={(event) => event.stopPropagation()}
                        onKeyDown={(event) => event.stopPropagation()}
                      >
                        <span className="inline-flex items-center justify-end gap-1">
                          <Button
                            variant="quiet"
                            size="sm"
                            icon={<Pencil size={13} aria-hidden="true" />}
                            disabled={project.source === "db"}
                            title={
                              project.source === "db"
                                ? "This project lives only in the database. The config file is the source of an edit, so only delete applies here."
                                : "Edit this project"
                            }
                            onClick={() => openEditDialog(project)}
                          >
                            Edit
                          </Button>
                          <Button
                            variant="quiet"
                            size="sm"
                            icon={
                              project.onhold ? (
                                <Play size={13} aria-hidden="true" />
                              ) : (
                                <Pause size={13} aria-hidden="true" />
                              )
                            }
                            title={
                              project.onhold
                                ? "Let the dispatcher plan work for it again"
                                : "Stop the dispatcher from planning new work for it"
                            }
                            onClick={() => pause.mutate(project)}
                            busy={pause.isPending && pause.variables?.key === project.key}
                          >
                            {project.onhold ? "Resume" : "Pause"}
                          </Button>
                          <Button
                            variant="quiet"
                            size="sm"
                            icon={<Trash2 size={13} aria-hidden="true" />}
                            title="Delete this project"
                            onClick={() => openDeleteDialog(project)}
                          >
                            Delete
                          </Button>
                        </span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </Panel>
      </div>

      <Dialog
        open={open}
        onOpenChange={(next) => {
          if (!next) closeDialog();
        }}
      >
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>
              {created ? "Project created" : editing ? `Edit ${editing.name}` : "New project"}
            </DialogTitle>
            <DialogDescription>
              {created
                ? "The project is written and on the board. Below is what the service could not check yet."
                : editing
                  ? "The same fields the create form validates, written back over the entry in config.yaml. The service validates the same rules and names the field it refuses."
                  : "Every field here is a field in config.yaml. The service validates the same rules and names the field it refuses."}
            </DialogDescription>
          </DialogHeader>

          {created ? (
            <div className="flex flex-col gap-2">
              <p className="text-body text-ink">
                <span className="font-mono text-[0.6875rem] text-ink-muted">{created.project.key}</span>{" "}
                {created.project.name}
              </p>
              <ul
                className="flex flex-col gap-1 border border-rule bg-ground px-2 py-1.5 text-[0.6875rem] text-ink-muted"
                aria-live="polite"
              >
                {created.warnings.map((warning) => (
                  <li key={warning}>{warning}</li>
                ))}
              </ul>
            </div>
          ) : (
            <form
              className="flex flex-col gap-3"
              onSubmit={(event) => {
                event.preventDefault();
                submit();
              }}
            >
              <div className="grid gap-3 sm:grid-cols-2">
                <TextInput
                  label="Key"
                  value={draft.key}
                  onChange={(event) => change("key", event.target.value)}
                  error={errors.key}
                  hint={
                    editing
                      ? "Immutable — delete and recreate to change it."
                      : "Lowercase, hyphenated — the name the other surfaces use."
                  }
                  disabled={!!editing}
                  autoComplete="off"
                />
                <TextInput
                  label="Name"
                  value={draft.name}
                  onChange={(event) => change("name", event.target.value)}
                  error={errors.name}
                  autoComplete="off"
                />
              </div>
              <div className="grid gap-3 sm:grid-cols-2">
                <SelectInput
                  label="Forge"
                  value={draft.forge}
                  onChange={(event) => change("forge", event.target.value)}
                  error={errors.forge}
                  options={FORGES}
                />
                <TextInput
                  label="Repo"
                  value={draft.repo}
                  onChange={(event) => change("repo", event.target.value)}
                  error={errors.repo}
                  hint="owner/name, as the forge spells it."
                  autoComplete="off"
                />
              </div>
              <div className="grid gap-3 sm:grid-cols-2">
                <TextInput
                  label="Forge host"
                  value={draft.forge_host}
                  onChange={(event) => change("forge_host", event.target.value)}
                  error={errors.forge_host}
                  disabled={draft.forge !== "gitlab"}
                  hint={
                    draft.forge === "gitlab"
                      ? "host[:port] of the GitLab instance; leave empty to use repo_url's host, or gitlab.com."
                      : "GitHub's host is gh's business (GH_HOST) — leave it empty."
                  }
                  autoComplete="off"
                />
                <TextInput
                  label="Repo URL"
                  value={draft.repo_url}
                  onChange={(event) => change("repo_url", event.target.value)}
                  error={errors.repo_url}
                  disabled={draft.forge !== "gitlab"}
                  hint={
                    draft.forge === "gitlab"
                      ? "The clone URL; drives setup and preflight."
                      : "GitHub needs no clone URL here — repo owner/name and gh drive it."
                  }
                  autoComplete="off"
                />
              </div>
              <div className="grid gap-3 sm:grid-cols-2">
                <TextInput
                  label="Default branch"
                  value={draft.default_branch}
                  onChange={(event) => change("default_branch", event.target.value)}
                  error={errors.default_branch}
                  autoComplete="off"
                />
                <TextInput
                  label="Concurrency cap"
                  type="number"
                  min={0}
                  value={draft.concurrency_cap}
                  onChange={(event) => change("concurrency_cap", event.target.value)}
                  error={errors.concurrency_cap}
                  hint="How many items may build at once."
                />
              </div>
              <RuntimePicker
                value={draft.runtime}
                onChange={(next) => change("runtime", next)}
                busy={busy}
              />
              {errors.runtime ? <p className="text-[0.6875rem] font-semibold text-attention">{errors.runtime}</p> : null}
              <TextInput
                label="Rigs"
                value={draft.rigs}
                onChange={(event) => change("rigs", event.target.value)}
                error={errors.rigs}
                hint="Comma-separated in dispatch order, for example “fleet-console, ghost”."
                autoComplete="off"
              />
              <TextInput
                label="Reviewer"
                value={draft.reviewer}
                onChange={(event) => change("reviewer", event.target.value)}
                error={errors.reviewer}
                hint="The reviewer seat's member prefix; the rig is appended. Empty is “dev-review”."
                autoComplete="off"
              />
              <div className="grid gap-3 sm:grid-cols-2">
                <TextInput
                  label="paths.main"
                  value={draft.paths_main}
                  onChange={(event) => change("paths_main", event.target.value)}
                  error={errors["paths.main"]}
                  hint="The checkout. A path that does not exist yet is accepted, with a warning."
                  autoComplete="off"
                />
                <TextInput
                  label="paths.worktrees"
                  value={draft.paths_worktrees}
                  onChange={(event) => change("paths_worktrees", event.target.value)}
                  error={errors["paths.worktrees"]}
                  hint="Where per-item worktrees are cut."
                  autoComplete="off"
                />
              </div>
              {formError ? (
                <p
                  role="alert"
                  className="border border-rule bg-ground px-2 py-1.5 text-[0.6875rem] font-semibold text-attention"
                >
                  {formError}
                </p>
              ) : null}
            </form>
          )}

          <DialogFooter>
            {created ? (
              <Button variant="primary" onClick={closeDialog}>
                Done
              </Button>
            ) : (
              <>
                <Button variant="quiet" onClick={closeDialog} disabled={busy}>
                  Cancel
                </Button>
                <Button variant="primary" onClick={submit} busy={busy}>
                  {editing ? "Save changes" : "Create project"}
                </Button>
              </>
            )}
          </DialogFooter>
          {/* The live panel: it runs against the draft, and creation stays enabled whatever it
              says. Refusing to write a half-configured project is how the registry starts lying
              about what exists. */}
          {!created && !editing && draft.key.trim() ? <DraftPreflight input={draftInput(draft)} enabled /> : null}
        </DialogContent>
      </Dialog>

      {confirming ? (
        <ConfirmDialog
          open
          onClose={() => setConfirming(null)}
          onConfirm={() => remove.mutate(confirming)}
          title={`Delete “${confirming.name}”?`}
          description={deleteDescription(confirming)}
          confirmLabel="Delete project"
          requireTyped={confirming.key}
          typedLabel={`Type “${confirming.key}” to confirm`}
          busy={remove.isPending}
          error={deleteError ?? undefined}
        />
      ) : null}
    </div>
  );
}
