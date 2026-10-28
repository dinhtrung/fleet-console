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

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    credentials: "same-origin",
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers ?? {}),
    },
  });
  if (!response.ok) {
    const text = await response.text();
    let message = text;
    try {
      const parsed = JSON.parse(text) as { error?: string };
      message = parsed.error ?? text;
    } catch {
      // keep the raw body
    }
    throw new Error(message || `HTTP ${response.status}`);
  }
  return (await response.json()) as T;
}

export const getBoard = () => request<BoardResponse>("/api/board");

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

export const getSeats = () => request<{ seats: Seat[] }>("/api/seats");

export const getSeatTail = (session: string, lines = 24) =>
  request<{ session: string; lines: number; tail: string }>(
    `/api/seats/${encodeURIComponent(session)}/tail?lines=${lines}`,
  );

export const getEvents = () => request<{ events: EventRow[] }>("/api/events?limit=50");

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
