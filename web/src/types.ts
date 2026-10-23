export type Locale = "zh-TW" | "zh-CN" | "en";
export type UiLanguage = Locale;
export type Runner = "codex" | "hermes";
export type ReportLevel = "full" | "brief" | "off";
export type DmMode = "off" | "only" | "both";

export interface Schedule {
  enabled: boolean;
  time: string;
  timezone: string;
}

export interface MemberConfig {
  runner: Runner;
  keywords: string[];
  persona: string;
  perDay: number;
  reportLevel: ReportLevel;
  language: Locale;
  autoComment: boolean;
  commentPerRound: number;
  mentionName: string;
  dmMode: DmMode;
  dmUid: number;
  replyToComments: boolean;
  replyPerRound: number;
  dmWatch: boolean;
  dmWatchMinutes: number;
  dmReplyPerDay: number;
  favFolderId: number;
  transcribe: boolean;
  asrModel: "tiny" | "base" | "small" | "medium";
  schedule: Schedule;
}

export interface PublicUser {
  id: string;
  username: string;
  display_name: string;
  role: "admin" | "member";
}

export interface RunnerInfo {
  available: boolean;
  command: string;
  home?: string;
}

export interface RunInfo {
  id: string;
  kind: string;
  runner: Runner;
  status: string;
  scheduled_for?: string;
  started_at?: string;
  finished_at?: string;
  picked_count: number;
  error?: string | null;
}

export interface StatusPayload {
  user: PublicUser;
  config: MemberConfig;
  schedule: Schedule & { next_run_at?: string | null };
  bilibili: { logged_in: boolean; uid?: number | null; nickname?: string | null };
  picked_today: number;
  remaining_today: number;
  runners: Record<Runner, RunnerInfo>;
  runner_route: {
    runner: Runner;
    provider: string;
    model: string;
    source: "admin" | "runtime-default";
  };
  active_run?: RunInfo | null;
  latest_run?: RunInfo | null;
  admin_settings: { outbound_enabled: boolean };
  version: string;
}

export interface WatchedRow {
  bvid: string;
  day: string;
  title: string;
  up: string;
  url: string;
  picked: boolean;
  state: string;
  tier: string;
  why: string;
  one_liner: string;
  delivered: boolean;
  cover: string;
}

export interface DeliveryRow {
  id: string;
  kind: string;
  bvid: string;
  content: string;
  status: string;
  remote_id?: string;
  visible?: boolean | null;
  created_at: string;
}

export interface TokenRow {
  id_hash: string;
  label: string;
  created_at: string;
  last_used_at?: string | null;
}

export interface UsagePayload {
  days: number;
  since: string;
  totals: {
    requests: number;
    input_tokens: number;
    output_tokens: number;
    cache_read_tokens: number;
    cost_usd: number;
    cache_hit_percent: number;
  };
  daily: Array<{
    date: string;
    requests: number;
    input_tokens: number;
    output_tokens: number;
    cache_read_tokens: number;
    cost_usd: number;
  }>;
  providers: Array<{
    app_type: string;
    provider: string;
    model: string;
    requests: number;
    input_tokens: number;
    output_tokens: number;
    cache_read_tokens: number;
    cost_usd: number;
  }>;
  runs: Array<{
    id: string;
    runner: Runner;
    provider: string;
    model: string;
    input_tokens: number;
    output_tokens: number;
    cache_read_tokens: number;
    cost_usd: number;
    status: string;
    finished_at?: string;
  }>;
  balances: Array<{
    provider: string;
    currency: string;
    total: number;
    granted: number;
    topped_up: number;
    available: boolean;
    low: boolean;
    source: string;
    fetched_at: string;
  }>;
}

