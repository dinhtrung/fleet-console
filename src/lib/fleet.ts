/** Typed reads for the product screens. The board authenticates with the session cookie served at
 *  `/`, so every call is same-origin and carries no token. */
export interface BoardItem {
  number: number;
  title: string;
  state: string;
  seat?: string;
  age: string;
  reason: string;
  url?: string;
  labels: string[];
  /** Both commits a stale verdict names (console-ui): the judged sha and the head the review
   *  must look at. Absent on every other row. */
  verdict_sha?: string;
  head_sha?: string;
}

export interface BoardCounts {
  building: number;
  review: number;
  waiting: number;
  parked: number;
  blocked: number;
  /** Stale verdicts are their own count: parked means nobody is needed, and a stale verdict is
   *  a review waiting for the reviewer. */
  stale: number;
}

/** One unanswered operator question (#377): a note the merge gate could not read as a verdict. The
 *  board reads it from the ledger, so it survives a restarted service; a row the queue says is
 *  finished never reaches the client. */
export interface BoardQuestion {
  item: number;
  change_request: number;
  note_id: string;
  head: string;
  destination: string;
  waiting: string;
  qitem?: string;
}

export interface BoardProject {
  key: string;
  name: string;
  paused: boolean;
  /** How old each mirror read is (#394). The phrase, the thresholds and the never-read
   *  wording are built server-side, so every client prints the same thing. */
  forge_read: MirrorRead;
  rig_read: MirrorRead;
  counts: BoardCounts;
  items: BoardItem[];
  questions: BoardQuestion[];
}

/** One "last forge read <age>" line. `label` is the whole phrase; `state` is never, ok,
 *  quiet or stalled; `at` is the stamp the age was measured from, absent when the mirror
 *  has never completed that read. */
export interface MirrorRead {
  label: string;
  state: "never" | "ok" | "quiet" | "stalled";
  at?: string;
}

export interface BoardResponse {
  projects: BoardProject[];
  synced_at: string;
}

export interface HumanOption {
  id: string;
  label: string;
  recommended?: boolean;
}

export interface HumanQuestion {
  id: string;
  question: string;
  options: HumanOption[];
}

export interface Decision {
  id: string;
  summary: string;
  body: string;
  destination: string;
  state: string;
  created_at: string;
  deadline_at?: string;
  tier?: string;
  evidence_ref?: string;
  questions?: HumanQuestion[];
}

export interface Seat {
  session: string;
  rig: string;
  logical_id: string;
  state: string;
  runtime: string;
  model: string;
  permission: string;
  last_activity: string;
}

export interface Check {
  name: string;
  conclusion: string;
}

export interface Verdict {
  id: number;
  dispatch_id: number;
  sha: string;
  decision: string;
  source: string;
  at: string;
}

export interface EventRow {
  id: number;
  at: string;
  actor: string;
  kind: string;
  subject: string;
  detail: string;
  exit_code?: number;
  stdout_hash: string;
}

/** One row of the app's own event log, exactly as `GET /api/events` returns it. The audit screen
 *  renders these fields and no others: `actor`/`kind` are exact matches and `subject` is a prefix,
 *  because that is what the server's closed filter set means. */
export type AuditEvent = EventRow;

/** A keyset page. `next_before` is the smallest id in the page when the page is full and 0 on the
 *  last page, so the screen offers a further page only when it is non-zero. */
export interface AuditPage {
  events: AuditEvent[];
  next_before: number;
}

/** The server's closed filter set. A field the server does not know is a 400, so this type carries
 *  only the five filters plus the keyset cursor and the page size. */
export interface EventFilter {
  actor?: string;
  kind?: string;
  subject?: string;
  since?: string;
  until?: string;
  before?: number;
  limit?: number;
}

export interface Dispatch {
  id: number;
  project_id: number;
  item_number: number;
  attempt: number;
  seat: string;
  branch: string;
  worktree: string;
  base_sha: string;
  qitem_id: string;
  brief_path: string;
  created_at: string;
  ended_at?: string;
  end_reason: string;
}

export interface ItemDetail {
  project: BoardProject;
  item: BoardItem;
  dispatch?: Dispatch;
  brief?: string;
  change_request?: { number: number; checks: Check[] };
  verdicts: Verdict[] | null;
  events: EventRow[] | null;
  /** A stale verdict (console-ui): the newest verdict names a sha other than the head. The
   *  drawer names both commits and says the review is pending. */
  verdict_stale?: boolean;
  verdict_sha?: string;
  head_sha?: string;
}

/** Project registry. The shapes are the config file's tags (`design.md` §5), so the dialog and the
 *  writer cannot drift: a field named here is a field `POST /api/projects` reads. */
export interface Project {
  key: string;
  name: string;
  forge: "github" | "gitlab";
  repo: string;
  /** The clone URL and the forge API's host (forge-host, design D6): the board writes the same
   *  two fields it reads. Both are read for GitLab; forge_host set for GitHub is a 422. */
  repo_url?: string;
  forge_host?: string;
  default_branch: string;
  rigs: string[];
  concurrency_cap: number;
  paths: {
    main: string;
    worktrees: string;
  };
  runtime?: string;
  rig_template?: string;
  labels?: Record<string, string>;
  /** The DB-owned pause (design D5): true means the act loop skips this project. The file's
   *  `onhold:` is folded in by the boot pass, so the board reads one flag. */
  onhold: boolean;
  /** The merged view's facts, from the list read alone (design D7). `source` says where the row
   *  lives — `both` is the ordinary case, `db` a store-only project the file no longer names, and
   *  an edit is refused for one of those because the file is the source of an edit. */
  source: "file" | "both" | "db";
  enabled: boolean;
  items: number;
  dispatches: number;
  live_dispatch: number;
  gate_cmd?: string;
  /** The reviewer seat's member prefix (#384): review rows go to `<reviewer>@<rig>`. Empty means
   *  the service's default, so the edit dialog carries it back unchanged. */
  reviewer?: string;
}

export interface ProjectsResponse {
  projects: Project[];
}

/** Preflight: four checks, each with a verdict, the fact found and how to fix it. `detail` is the
 *  command's own last line, rendered verbatim — a paraphrase would hide a typo behind "auth failed". */
export type CheckState = "ok" | "warn" | "fail" | "unknown";

export interface PreflightFix {
  command: string;
  step: string;
  why: string;
}

export interface PreflightCheck {
  id: "git_url" | "base_dir" | "rig" | "runtime" | string;
  label: string;
  state: CheckState;
  detail: string;
  fix: PreflightFix;
}

export interface RuntimeInfo {
  id: string;
  supported: boolean;
  auth_present: boolean;
  login_hint: string;
  login_doc: string;
}

export interface PreflightReport {
  project: string;
  checks: PreflightCheck[];
  ready: boolean;
  runtime: RuntimeInfo;
  checked_at: string;
}

export interface SetupStepResult {
  step: string;
  state: "done" | "skipped" | "failed";
  command: string;
  output: string;
}

export interface SetupResponse {
  project: string;
  steps: SetupStepResult[];
}

/** The four step ids, in the order the server runs them. The client never chooses the order. */
export const SETUP_STEPS = ["mkdir_base", "clone", "apply_rig", "up_rig"] as const;

export interface CreateProjectInput {
  key: string;
  name: string;
  forge: "github" | "gitlab";
  repo: string;
  /** Same two fields as Project (forge-host, design D6). Empty keeps the resolver's answer. */
  repo_url?: string;
  forge_host?: string;
  default_branch: string;
  rigs: string[];
  concurrency_cap: number;
  /** The runtime the picker chose; the server validates it against the same four it lists. */
  runtime: string;
  paths: {
    main: string;
    worktrees: string;
  };
  /** Carried so a whole-definition save never clears it (#384); empty is the default. */
  reviewer?: string;
}

export interface CreateProjectResponse {
  project: Project;
  warnings: string[] | null;
}

/** A refusal from the service. `fieldErrors` is the `422` body's `errors` map, kept as a map so the
 *  dialog can put each message on the input it belongs to instead of in one flat sentence. */
export class ApiError extends Error {
  readonly status: number;
  readonly fieldErrors: Record<string, string>;
  /** The decoded refusal body, when it was JSON. Routes whose `409` carries fields (the culture
   *  write's `sha` and `mtime`) read them from here rather than re-requesting. */
  readonly body: unknown;

  constructor(status: number, message: string, fieldErrors: Record<string, string> = {}, body?: unknown) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.fieldErrors = fieldErrors;
    this.body = body;
  }
}

export function isApiError(error: unknown): error is ApiError {
  return error instanceof ApiError;
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const method = (init?.method ?? "GET").toUpperCase();
  // Every mutating route sits behind the service's idempotency middleware, which answers `400`
  // without this header. One key per call: a retry is a new request unless the caller reuses it.
  const idempotencyKey =
    method === "GET" || method === "HEAD" || method === "OPTIONS" ? null : crypto.randomUUID();
  const response = await fetch(path, {
    credentials: "same-origin",
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...(idempotencyKey ? { "Idempotency-Key": idempotencyKey } : {}),
      ...init?.headers,
    },
  });
  if (!response.ok) {
    // Both project routes answer a refusal as `{error: "<sentence>", errors?: {<field>: "<message>"}}`.
    // The map is the only shape trusted to be one; anything else falls back to the status text.
    let message = response.statusText;
    let fieldErrors: Record<string, string> = {};
    let decoded: unknown;
    try {
      const body: unknown = await response.json();
      decoded = body;
      if (body && typeof body === "object") {
        const { error, errors } = body as { error?: unknown; errors?: unknown };
        if (typeof error === "string" && error) message = error;
        if (errors && typeof errors === "object") fieldErrors = errors as Record<string, string>;
      }
    } catch {
      // A body that is not JSON leaves the status text as the message.
    }
    throw new ApiError(response.status, message, fieldErrors, decoded);
  }
  return response.json();
}

export const getBoard = () => request<BoardResponse>("/api/board");

export const getProjects = () => request<ProjectsResponse>("/api/projects");

export const createProject = (input: CreateProjectInput) =>
  request<CreateProjectResponse>("/api/projects", {
    method: "POST",
    body: JSON.stringify(input),
  });

/** The edit's answer: the stored project, the field names that changed and the loader's warnings. */
export interface UpdateProjectResponse {
  project: Project;
  changed: string[];
  warnings: string[] | null;
}

/** Replaces the whole definition (design D2). The key is immutable — a body naming a different one
 *  is a `422` on `key` — and an identity change under a live dispatch is a `409` whose message
 *  carries the remedy: pause the project, wait for the row to end, then retry (design D3). */
export const updateProject = (key: string, input: CreateProjectInput) =>
  request<UpdateProjectResponse>(`/api/projects/${encodeURIComponent(key)}`, {
    method: "PUT",
    body: JSON.stringify(input),
  });

/** The DB-owned pause (design D5). Never writes the config file, so the pause survives a boot. */
export const setProjectOnhold = (key: string, onhold: boolean) =>
  request<UpdateProjectResponse>(`/api/projects/${encodeURIComponent(key)}/onhold`, {
    method: "PUT",
    body: JSON.stringify({ onhold }),
  });

/** What a delete removed — the same numbers the dialog warned with (design D7). */
export interface DeleteProjectResponse {
  deleted: {
    items: number;
    dispatches: number;
    verdicts: number;
  };
}

/** Deletes the file entry and the store's rows for it (design D1's order). `confirm` must equal the
 *  key; a live dispatch refuses with the remedy, and a half-finished delete resumes on retry. */
export const deleteProject = (key: string, confirm: string) =>
  request<DeleteProjectResponse>(
    `/api/projects/${encodeURIComponent(key)}?confirm=${encodeURIComponent(confirm)}`,
    { method: "DELETE" },
  );

/** The four checks for a registered project. */
export const getPreflight = (key: string) =>
  request<PreflightReport>(`/api/projects/${encodeURIComponent(key)}/preflight`);

/** The same checks against a body that is not a project yet. `rig` answers unknown, because the
 *  rig is created with the project — a draft cannot have one. */
export const preflightDraft = (input: CreateProjectInput) =>
  request<PreflightReport>("/api/projects/preflight", {
    method: "POST",
    body: JSON.stringify(input),
  });

/** Run the allowlisted steps. An empty list means all four, in the server's fixed order. */
export const runSetup = (key: string, steps: string[] = []) =>
  request<SetupResponse>(`/api/projects/${encodeURIComponent(key)}/setup`, {
    method: "POST",
    body: JSON.stringify({ steps }),
  });

/** The runtime list the picker is driven by. Nothing is hardcoded on the client: a fifth runtime
 *  added on the server appears here without a client change. */
export const getRuntimes = () => request<{ runtimes: RuntimeInfo[] }>("/api/runtimes");

export const getItem = (number: number) => request<ItemDetail>(`/api/items/${number}`);

export const getDecisions = () => request<{ decisions: Decision[] }>("/api/decisions");

export const answerDecision = (id: string, decision: string) =>
  request<{ id: string; state: string; event_id: number }>(
    `/api/decisions/${encodeURIComponent(id)}/answer`,
    {
      method: "POST",
      headers: { "Idempotency-Key": crypto.randomUUID() },
      body: JSON.stringify({ decision }),
    },
  );

/** What an approve/reject did: the note that landed, the sha it names, and where the merge
 *  happens. `merge: "manual"` is the adoption gap — the note is evidence, nothing merges it. */
export interface VerdictResult {
  project: string;
  item: number;
  verdict: "accept" | "reject";
  sha: string;
  change_request: number;
  ready: boolean;
  merge: "act-tick" | "manual";
  checks?: { name: string; conclusion: string }[];
}

/** Posts the operator's verdict on the item's open change request. It is the same
 *  `ACCEPT <sha>` / `REJECT <sha>` note a reviewer seat posts, so the merge gate reads one
 *  record either way and the newest verdict wins. A reject without a reason is refused by
 *  the service: the reason is the builder's work list. */
export const postVerdict = (number: number, verdict: "accept" | "reject", reason: string) =>
  request<VerdictResult>(`/api/items/${number}/verdict`, {
    method: "POST",
    body: JSON.stringify({ verdict, reason }),
  });

export const getSeats = () => request<{ seats: Seat[] }>("/api/seats");

/** The pane tail for one seat. The session contains `@`, so the path segment is escaped;
 *  `lines` is passed through untouched because the route owns the 1..200 clamp. */
export const seatTail = (session: string, lines = 24) =>
  request<{ session: string; lines: number; tail: string }>(
    `/api/seats/${encodeURIComponent(session)}/tail?lines=${lines}`,
  );

/** The newest page of the event log, or the page before a cursor. Every filter the server accepts
 *  is passed through as a query parameter; an empty value is omitted so "no filter" is the absence
 *  of the parameter rather than an empty string the server would have to interpret. */
export const listEvents = (filter: EventFilter = {}) => {
  const params = new URLSearchParams();
  for (const key of ["actor", "kind", "subject", "since", "until"] as const) {
    const value = filter[key];
    if (value) params.set(key, value);
  }
  if (filter.before && filter.before > 0) params.set("before", String(filter.before));
  if (filter.limit && filter.limit > 0) params.set("limit", String(filter.limit));
  const query = params.toString();
  return request<AuditPage>(`/api/events${query ? `?${query}` : ""}`);
};

/** One event by id. A missing row is a 404, which the screen renders as "no such audit event"
 *  rather than as a failed read. */
export const getEvent = (id: number) => request<AuditEvent>(`/api/events/${id}`);

export function shortTime(value?: string): string {
  if (!value) return "—";
  const at = new Date(value);
  if (Number.isNaN(at.getTime())) return value;
  const seconds = Math.max(0, Math.round((Date.now() - at.getTime()) / 1000));
  if (seconds < 60) return `${seconds}s ago`;
  if (seconds < 3600) return `${Math.round(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.round(seconds / 3600)}h ago`;
  return `${Math.round(seconds / 86400)}d ago`;
}

/** The culture file as the board reads it. `exists: false` is a normal answer — the screen needs
 *  `path` in order to explain where the file would live. */
export interface CultureSnapshot {
  path: string;
  source: "config" | "derived" | "not_configured";
  rig: string | null;
  exists: boolean;
  writable: boolean;
  bytes: number;
  sha256: string;
  mtime?: string;
  content?: string;
}

export interface CultureWriteResult {
  sha256: string;
  bytes: number;
  mtime: string;
  backup?: string;
}

/** A `409` from the culture write: the file moved since the editor read it. It carries the sha and
 *  mtime that are on disk now, which is what the banner needs to offer "Load latest" and
 *  "Overwrite" without a second round trip. Kept as its own type rather than a flag on `ApiError`
 *  so the screen cannot mistake a `409` for any other refusal. */
export class CultureConflict extends Error {
  readonly sha: string;
  readonly mtime?: string;

  constructor(message: string, sha: string, mtime?: string) {
    super(message);
    this.name = "CultureConflict";
    this.sha = sha;
    this.mtime = mtime;
  }
}

export function isCultureConflict(error: unknown): error is CultureConflict {
  return error instanceof CultureConflict;
}

export const getCulture = (key: string) =>
  request<CultureSnapshot>(`/api/projects/${encodeURIComponent(key)}/culture`);

/** Saves the file. `baseSHA` is the sha the editor loaded; `null` means "I expect no file". A stale
 *  base rejects with `CultureConflict` carrying the sha on disk. */
export async function putCulture(
  key: string,
  content: string,
  baseSHA: string | null,
): Promise<CultureWriteResult> {
  try {
    return await request<CultureWriteResult>(
      `/api/projects/${encodeURIComponent(key)}/culture`,
      { method: "PUT", body: JSON.stringify({ content, base_sha: baseSHA }) },
    );
  } catch (error) {
    if (isApiError(error) && error.status === 409) {
      // `{error, sha, mtime}` for a stale write, `{error, sha: null}` when the file is absent and a
      // base was sent. The sha is the whole point of the type: without it the banner could not
      // offer "Overwrite" without another round trip.
      const body = (error.body ?? {}) as { sha?: unknown; mtime?: unknown };
      const sha = typeof body.sha === "string" ? body.sha : "";
      const mtime = typeof body.mtime === "string" ? body.mtime : undefined;
      throw new CultureConflict(error.message, sha, mtime);
    }
    throw error;
  }
}

/* ------------------------------------------------------------------ settings */

/** One key the settings surface refuses, with the server's reason. Rendering it editable would be a
 *  bug: the route answers 400 for it. */
export interface ReadOnlyKey {
  key: string;
  reason: string;
}

/** GET /api/config. `settings` carries only what PUT accepts; `source` says where each value came
 *  from (flag, environment, file or default); `hash` is the file bytes this read came from and must
 *  be sent back on save. */
export interface SettingsSnapshot {
  settings: Record<string, string>;
  source: Record<string, string>;
  read_only: ReadOnlyKey[];
  hash: string;
}

/** PUT /api/config. `saved: false` means the file already held these values, so the same shape as
 *  "unchanged" from --apply-config. `restart_required` names the unit command's consequence without
 *  running it. */
export interface SettingsWriteResult {
  saved: boolean;
  changed: string[];
  restart_required: boolean;
  settings: Record<string, string>;
  source: Record<string, string>;
  hash: string;
}

/** A `409` from the settings write: the file changed since the screen loaded it. The screen must
 *  reload rather than retry, because a retry with the old hash would be refused again. Its own type
 *  so the screen cannot mistake it for a validation refusal. */
export class SettingsConflict extends Error {
  constructor(message: string) {
    super(message);
    this.name = "SettingsConflict";
  }
}

export function isSettingsConflict(error: unknown): error is SettingsConflict {
  return error instanceof SettingsConflict;
}

export const getSettings = () => request<SettingsSnapshot>("/api/config");

/** Saves the given keys. `hash` is the hash `getSettings` returned; a stale one rejects with
 *  `SettingsConflict`. A key absent from `values` is left alone, so a save writes only what the
 *  operator touched. */
export async function putSettings(values: Record<string, string>, hash: string): Promise<SettingsWriteResult> {
  try {
    return await request<SettingsWriteResult>("/api/config", {
      method: "PUT",
      body: JSON.stringify({ values, hash }),
    });
  } catch (error) {
    if (isApiError(error) && error.status === 409) {
      throw new SettingsConflict(error.message);
    }
    throw error;
  }
}
