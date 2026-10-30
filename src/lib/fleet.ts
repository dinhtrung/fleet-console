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
}

export interface BoardCounts {
  building: number;
  review: number;
  waiting: number;
  parked: number;
  blocked: number;
}

export interface BoardProject {
  key: string;
  name: string;
  paused: boolean;
  counts: BoardCounts;
  items: BoardItem[];
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
}

/** Project registry. The shapes are the config file's tags (`design.md` §5), so the dialog and the
 *  writer cannot drift: a field named here is a field `POST /api/projects` reads. */
export interface Project {
  key: string;
  name: string;
  forge: "github" | "gitlab";
  repo: string;
  default_branch: string;
  rigs: string[];
  concurrency_cap: number;
  paths: {
    main: string;
    worktrees: string;
  };
  rig_template?: string;
  labels?: Record<string, string>;
  onhold?: boolean | null;
  gate_cmd?: string;
}

export interface ProjectsResponse {
  projects: Project[];
}

export interface CreateProjectInput {
  key: string;
  name: string;
  forge: "github" | "gitlab";
  repo: string;
  default_branch: string;
  rigs: string[];
  concurrency_cap: number;
  paths: {
    main: string;
    worktrees: string;
  };
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

  constructor(status: number, message: string, fieldErrors: Record<string, string> = {}) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.fieldErrors = fieldErrors;
  }
}

export function isApiError(error: unknown): error is ApiError {
  return error instanceof ApiError;
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    credentials: "same-origin",
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...init?.headers,
    },
  });
  if (!response.ok) {
    // Both project routes answer a refusal as `{error: "<sentence>", errors?: {<field>: "<message>"}}`.
    // The map is the only shape trusted to be one; anything else falls back to the status text.
    let message = response.statusText;
    let fieldErrors: Record<string, string> = {};
    try {
      const body: unknown = await response.json();
      if (body && typeof body === "object") {
        const { error, errors } = body as { error?: unknown; errors?: unknown };
        if (typeof error === "string" && error) message = error;
        if (errors && typeof errors === "object") fieldErrors = errors as Record<string, string>;
      }
    } catch {
      // A body that is not JSON leaves the status text as the message.
    }
    throw new ApiError(response.status, message, fieldErrors);
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

export const getItem = (number: number) => request<ItemDetail>(`/api/items/${number}`);

export const getDecisions = () => request<{ decisions: Decision[] }>("/api/decisions");

export const answerDecision = (id: string, decision: string) =>
  request<{ id: string; state: string; event_id: number }>(
    `/api/decisions/${encodeURIComponent(id)}/answer`,
    {
      method: "POST",
      body: JSON.stringify({ decision }),
    },
  );

export const getSeats = () => request<{ seats: Seat[] }>("/api/seats");

export const getSeatTail = (session: string, lines = 24) =>
  request<{ session: string; lines: number; tail: string }>(
    `/api/seats/${encodeURIComponent(session)}/tail?lines=${lines}`,
  );

export const getEvents = () => request<{ events: EventRow[] }>("/api/events?limit=50");

export function shortTime(value?: string): string {
  if (!value) return "";
  const date = new Date(value);
  if (isNaN(date.getTime())) return value;
  return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}