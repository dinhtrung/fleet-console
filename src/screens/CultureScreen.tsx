/** Culture: one project's CULTURE.md — read it, edit it, and say what a save does not do.
 *
 *  Everything goes through `src/lib/fleet.ts`; there is no `fetch` here.
 *
 *  The screen is written to be honest at its edges, because a culture file is read by models and a
 *  wrong edit is expensive:
 *   - it shows the path it actually read and the provenance (`derived` or `config`), so the operator
 *     can tell why the board is looking there;
 *   - it shows the exact sha it will send back, so a `409` is explained rather than surprising;
 *   - it states what a save cannot do: a running seat keeps the copy it was launched with.
 *  `exists: false` and `not_configured` are not errors: they are the screen saying where the file
 *  would live and which key would move it, with no editor until the operator chooses to create it. */

import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Copy, FileText, Pencil, RotateCcw } from "lucide-react";
import {
  getCulture,
  getProjects,
  isCultureConflict,
  putCulture,
  shortTime,
  type CultureSnapshot,
} from "../lib/fleet";
import {
  Badge,
  Button,
  ErrorState,
  FieldRow,
  LoadingRows,
  Panel,
  PanelHead,
  TextArea,
  useToast,
} from "../components/ui";
import { PageHeader } from "../shell/PageHeader";

const LIMIT = 256 * 1024;

function shortSha(sha: string): string {
  return sha ? sha.slice(0, 8) : "—";
}

function bytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

/** The header line: `rig fleet-console · derived · /path · sha 3f2a91c4 · 5.4 KB · 2m ago`. */
function CultureHeader({ culture }: { culture: CultureSnapshot }) {
  // A file that does not exist has no size and no sha: "0 B" reads like an empty file, which is a
  // different fact and a more alarming one.
  const parts = culture.exists
    ? [
        culture.rig ? `rig ${culture.rig}` : "no rig",
        culture.source,
        culture.path,
        `sha ${shortSha(culture.sha256)}`,
        bytes(culture.bytes),
        culture.mtime ? shortTime(culture.mtime) : "never written",
      ]
    : [
        culture.rig ? `rig ${culture.rig}` : "no rig",
        culture.source === "not_configured" ? "not configured" : culture.source,
        culture.path || "no path",
        "no file yet",
      ];
  return (
    <p className="font-mono text-xs text-muted-foreground" data-testid="culture-header">
      {parts.join(" · ")}
    </p>
  );
}

function CopyPath({ path }: { path: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <Button
      size="sm"
      variant="outline"
      onClick={() => {
        void navigator.clipboard.writeText(path).then(() => {
          setCopied(true);
          window.setTimeout(() => setCopied(false), 1500);
        });
      }}
    >
      {copied ? <Check size={14} /> : <Copy size={14} />}
      {copied ? "Copied" : "Copy path"}
    </Button>
  );
}

/** No file yet: say where it would live and which key moves it. `canCreate` is false when the
 *  project names no culture file at all — there is no path to create, so offering the button would
 *  be offering something that cannot work. */
function NotConfigured({
  culture,
  canCreate,
  onCreate,
}: {
  culture: CultureSnapshot;
  canCreate: boolean;
  onCreate: () => void;
}) {
  return (
    <Panel>
      <PanelHead title="No culture file yet" />
      <div className="space-y-3 p-4 text-sm">
        <p className="text-muted-foreground">
          {canCreate
            ? "The board reads this project's culture from the path below. Nothing has been created: the fleet keeps running on the files it started with."
            : "This project names no culture file, so the board has no path to read or write. Set culture_file below and reload."}
        </p>
        <FieldRow label="Expected path">
          <code className="font-mono text-xs">{culture.path || "(no path resolved)"}</code>
        </FieldRow>
        <FieldRow label="To change it">
          <code className="font-mono text-xs">projects[].culture_file</code> in{" "}
          <code className="font-mono text-xs">config.yaml</code>
        </FieldRow>
        <div className="flex gap-2">
          {culture.path ? <CopyPath path={culture.path} /> : null}
          {canCreate ? (
            <Button size="sm" onClick={onCreate}>
              <FileText size={14} /> Create it
            </Button>
          ) : null}
        </div>
      </div>
    </Panel>
  );
}

export function CultureScreen() {
  const projects = useQuery({ queryKey: ["projects"], queryFn: getProjects });
  const [key, setKey] = useState<string | null>(null);
  const activeKey = key ?? projects.data?.projects[0]?.key ?? null;

  const culture = useQuery({
    queryKey: ["culture", activeKey],
    queryFn: () => getCulture(activeKey as string),
    enabled: Boolean(activeKey),
  });

  const queryClient = useQueryClient();
  const toast = useToast();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const [conflict, setConflict] = useState<{ sha: string; mtime?: string } | null>(null);
  const [saved, setSaved] = useState<{ sha256: string; backup?: string } | null>(null);

  const content = culture.data?.content ?? "";
  const dirty = editing && draft !== content;

  // Leaving the screen or the mode with unsaved changes asks first.
  useEffect(() => {
    if (!dirty) return;
    const onBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", onBeforeUnload);
    return () => window.removeEventListener("beforeunload", onBeforeUnload);
  }, [dirty]);

  const save = useMutation({
    mutationFn: (base: string | null) => putCulture(activeKey as string, draft, base),
    onSuccess: (result) => {
      setConflict(null);
      setSaved({ sha256: result.sha256, backup: result.backup });
      setEditing(false);
      void queryClient.invalidateQueries({ queryKey: ["culture", activeKey] });
      toast.done("Culture saved", shortSha(result.sha256));
    },
    onError: (error) => {
      if (isCultureConflict(error)) {
        setConflict({ sha: error.sha, mtime: error.mtime });
        return;
      }
      toast.problem("Save failed", error instanceof Error ? error.message : String(error));
    },
  });

  const draftBytes = useMemo(() => new TextEncoder().encode(draft).length, [draft]);
  const overLimit = draftBytes > LIMIT;

  if (projects.isPending) return <LoadingRows rows={3} />;
  if (projects.isError) return <ErrorState title="Projects did not load" error={projects.error} onRetry={() => void projects.refetch()} />;

  const list = projects.data?.projects ?? [];
  if (list.length === 0) {
    return (
      <ErrorState
        title="No projects"
        error={new Error("Register a project first; its culture file belongs to one project's rig.")}
        onRetry={() => void projects.refetch()}
      />
    );
  }

  if (!activeKey) return <LoadingRows rows={2} />;
  if (culture.isPending) return <LoadingRows rows={3} />;
  if (culture.isError) {
    return (
      <ErrorState
        title="Culture did not load"
        error={culture.error}
        onRetry={() => void culture.refetch()}
      />
    );
  }

  const snapshot = culture.data as CultureSnapshot;
  // Two different facts, not one. `not_configured` means the project names no culture file and the
  // resolver produced no path: nothing can be created until the operator sets `culture_file`. A
  // missing file at a real path is the case `Create it` is for. Conflating them made the button
  // appear where it could not possibly work.
  const notConfigured = snapshot.source === "not_configured" || !snapshot.path;
  const missing = !notConfigured && !snapshot.exists;

  return (
    <div className="space-y-4">
      <PageHeader
        title="Culture"
        description="The rules a rig's seats read when they start."
        actions={
          list.length > 1 ? (
            <select
              className="h-9 rounded-md border bg-transparent px-2 text-sm"
              value={activeKey}
              onChange={(event) => {
                if (dirty && !window.confirm("Discard unsaved changes?")) return;
                setKey(event.target.value);
                setEditing(false);
                setConflict(null);
                setSaved(null);
              }}
            >
              {list.map((project) => (
                <option key={project.key} value={project.key}>
                  {project.key}
                </option>
              ))}
            </select>
          ) : null
        }
      />

      <div className="space-y-1">
        <CultureHeader culture={snapshot} />
        {snapshot.exists && !snapshot.writable ? (
          <p className="text-xs text-amber-600" data-testid="culture-readonly">
            This file is not writable (mode 0444). A save will be refused until the mode changes.
          </p>
        ) : null}
      </div>

      {conflict ? (
        <Panel>
          <PanelHead title="The file changed on disk" />
          <div className="space-y-3 p-4 text-sm">
            <p className="text-muted-foreground">
              The copy you loaded is <code className="font-mono">{shortSha(snapshot.sha256)}</code>; the
              file is now <code className="font-mono">{shortSha(conflict.sha)}</code>
              {conflict.mtime ? ` (changed ${shortTime(conflict.mtime)})` : ""}. Your text is still in
              the editor — nothing was overwritten.
            </p>
            <div className="flex gap-2">
              <Button
                size="sm"
                variant="outline"
                onClick={() => {
                  setConflict(null);
                  setEditing(false);
                  void culture.refetch();
                }}
              >
                <RotateCcw size={14} /> Load latest
              </Button>
              <Button
                size="sm"
                onClick={() => {
                  setConflict(null);
                  save.mutate(conflict.sha);
                }}
              >
                Overwrite with the current sha
              </Button>
            </div>
          </div>
        </Panel>
      ) : null}

      {saved && !conflict ? (
        <Panel>
          <PanelHead title="Saved" />
          <div className="space-y-2 p-4 text-sm">
            <p>
              sha <code className="font-mono">{shortSha(saved.sha256)}</code>
              {saved.backup ? (
                <>
                  {" "}
                  · backup <code className="font-mono">{saved.backup.split("/").pop()}</code>
                </>
              ) : null}
            </p>
            <p className="text-muted-foreground" data-testid="culture-save-caveat">
              Seats that are already running keep the copy they were launched with; this applies the
              next time a seat starts. There is no restart button here — restarting a rig kills seats
              mid-turn, so that stays on the command line.
            </p>
          </div>
        </Panel>
      ) : null}

      {(notConfigured || missing) && !editing ? (
        <NotConfigured
          culture={snapshot}
          canCreate={missing}
          onCreate={() => {
            setDraft("");
            setConflict(null);
            setSaved(null);
            setEditing(true);
          }}
        />
      ) : (
        <Panel>
          <PanelHead
            title="Contents"
            actions={
              editing ? null : (
                <Button
                  size="sm"
                  onClick={() => {
                    setDraft(content);
                    setSaved(null);
                    setEditing(true);
                  }}
                >
                  <Pencil size={14} /> Edit
                </Button>
              )
            }
          />
          {editing ? (
            <div className="space-y-2 p-4">
              <TextArea
                label="Culture markdown"
                className="min-h-[24rem] font-mono text-xs"
                value={draft}
                onChange={(event) => setDraft(event.target.value)}
              />
              <div className="flex items-center justify-between text-xs">
                <span className={overLimit ? "text-red-600" : "text-muted-foreground"}>
                  {bytes(draftBytes)} / {bytes(LIMIT)}
                  {overLimit ? " — over the limit, the save will be refused" : ""}
                  {dirty ? " · unsaved changes" : ""}
                </span>
                <div className="flex gap-2">
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => {
                      if (dirty && !window.confirm("Discard unsaved changes?")) return;
                      setEditing(false);
                      setDraft(content);
                    }}
                  >
                    Cancel
                  </Button>
                  <Button
                    size="sm"
                    disabled={!dirty || overLimit || save.isPending}
                    onClick={() => save.mutate(snapshot.exists ? snapshot.sha256 : null)}
                  >
                    Save
                  </Button>
                </div>
              </div>
            </div>
          ) : (
            <pre
              className="max-h-[40rem] overflow-auto whitespace-pre-wrap p-4 font-mono text-xs leading-relaxed"
              data-testid="culture-content"
            >
              {content || "(empty file)"}
            </pre>
          )}
        </Panel>
      )}

      <p className="text-xs text-muted-foreground">
        <Badge tone="quiet">{snapshot.source === "config" ? "configured" : snapshot.source}</Badge>{" "}
        {snapshot.path}
      </p>
    </div>
  );
}
