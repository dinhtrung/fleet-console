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

/** Project registry */
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
  rig_template: string;
  labels: Record<string, string>;
  onhold: boolean | null;
  gate_cmd: string | null;
}

export interface ProjectsResponse {
  projects: Project[];
  synced_at: string;
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
  rig_template: string;
  labels: Record<string, string>;
  onhold: boolean;
  gate_cmd: string;
}

export interface CreateProjectResponse {
  project: Project;
  warnings: string[];
}

export interface ApiError {
  status: number;
  field_errors: Record<string, string>;
  message: string;
}

export function isApiError(error: unknown): error is ApiError {
  return typeof error === "object" && error !== null && "status" in error && "field_errors" in error;
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
    const error: ApiError = {
      status: response.status,
      field_errors: {},
      message: "",
    };
    try {
      const body = await response.json();
      if (body.field_errors) {
        error.field_errors = body.field_errors;
      }
      if (body.message) {
        error.message = body.message;
      }
    } catch {
      error.message = response.statusText;
    }
    throw error;
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