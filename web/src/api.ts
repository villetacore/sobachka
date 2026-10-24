export type Order = {
  id: number;
  source_id: number;
  source_name: string;
  url: string;
  title: string;
  description: string;
  category: string;
  budget: string;
  budget_value: number | null;
  published_at: string | null;
  created_at: string;
  profile_id: number | null;
  profile_name: string;
  score: number | null;
  status: "new" | "scored" | "filtered" | "matched" | "notified" | "error";
  error: string;
  pitch: string;
  feedback: number;
  matched: string[] | null;
  notified_at: string | null;
};

export type Profile = {
  id: number;
  name: string;
  description: string;
  skills: string;
  keywords: string[];
  minus_words: string[];
  min_budget: number;
  threshold: number;
  pitch_notes: string;
  pitch_template: string;
  enabled: boolean;
};

export type Source = {
  id: number;
  kind: string;
  name: string;
  url: string;
  enabled: boolean;
  interval_sec: number;
  last_run_at: string | null;
  last_error: string;
  pattern: string;
  cookie: string;
  config: Record<string, unknown> | null;
};

export type SourceKind = {
  id: string;
  title: string;
  hint: string;
  example_url: string;
};

export type SourcePreset = {
  name: string;
  kind: string;
  url: string;
  pattern: string;
  interval_sec: number;
  note: string;
  config?: Record<string, unknown>;
};

export type Settings = {
  notify_enabled: boolean;
  auto_pitch: boolean;
  notify_max_age_min: number;
  retrain_every: number;
};

export type Stats = {
  total: number;
  today: number;
  matched: number;
  notified: number;
  positive: number;
  negative: number;
  errors: number;
  unscored: number;
  filtered: number;
};

export type MLScore = {
  profile_id: number;
  score: number;
  base: number;
  head: number | null;
  matched_keywords: string[] | null;
};

export type Evaluation = {
  best: MLScore | null;
  profile: Profile | null;
  matched: boolean;
  scores: MLScore[] | null;
  filtered: Record<string, string>;
};

export type MLStatus = {
  health: {
    status?: string;
    head?: {
      trained: boolean;
      samples: number;
      max_weight: number;
      full_trust: number;
      self_weight: number;
      profiles: Record<string, { user: number; auto: number; weight: number }>;
      metrics: Record<string, number | string | null>;
      memory: number;
      new_since_train: number;
      trained_at: number | null;
      interval_min: number;
    } | null;
  };
  last_train: { at: string; result?: Record<string, number>; error?: string };
  error?: string;
};

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

export async function api<T>(
  path: string,
  init: { method?: string; body?: unknown } = {},
): Promise<T> {
  const res = await fetch(`/api${path}`, {
    method: init.method ?? "GET",
    credentials: "same-origin",
    headers: {
      "Content-Type": "application/json",
      "X-Sobachka-Request": "1",
    },
    body: init.body === undefined ? undefined : JSON.stringify(init.body),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    if (res.status === 401 && path !== "/auth/login")
      window.dispatchEvent(new Event("session-expired"));
    throw new ApiError(res.status, data.error ?? `HTTP ${res.status}`);
  }
  return data as T;
}

export const STATUS_LABEL: Record<Order["status"], string> = {
  new: "в очереди",
  scored: "не подошёл",
  filtered: "отфильтрован",
  matched: "подходит",
  notified: "отправлен в TG",
  error: "ошибка",
};

export function fmtDate(s: string | null): string {
  if (!s) return "—";
  return new Date(s).toLocaleString("ru-RU", {
    day: "2-digit",
    month: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function pct(v: number | null | undefined): string {
  return v == null ? "—" : `${Math.round(v * 100)}%`;
}

export type Role = "admin" | "operator" | "viewer";
export type User = {
  id: number;
  username: string;
  role: Role;
  enabled: boolean;
  telegram_chat_id: number | null;
  notify_enabled: boolean;
  all_profiles: boolean;
};
export const ROLE_LABEL: Record<Role, string> = {
  admin: "Администратор",
  operator: "Оператор",
  viewer: "Наблюдатель",
};
export type BotInfo = { id: number; username: string; name: string };
