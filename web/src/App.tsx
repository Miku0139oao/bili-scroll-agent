import {
  FormEvent,
  ReactNode,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  ArrowRight,
  BadgeCheck,
  BarChart3,
  Check,
  Copy,
  Eye,
  EyeOff,
  FileText,
  Gauge,
  Inbox,
  KeyRound,
  Layers3,
  LogOut,
  MessageCircleMore,
  Play,
  QrCode,
  Radio,
  ScanLine,
  Settings2,
  ShieldCheck,
  Smartphone,
  SlidersHorizontal,
  Sun,
} from "lucide-react";
import { ApiError, api, restoreCsrf, setCsrf } from "./api";
import { tr, type MessageKey } from "./i18n";
import { AdminProviders } from "./pages/AdminProviders";
import {
  Choice,
  EmptyState,
  Field,
  InlineState,
  Metric,
  Panel,
  Toggle,
  compactNumber,
  errorText,
  type Toast,
} from "./ui";
import type {
  DeliveryRow,
  MemberConfig,
  PublicUser,
  RunnerInfo,
  StatusPayload,
  TokenRow,
  UiLanguage,
  UsagePayload,
  WatchedRow,
} from "./types";

type Page = "daily" | "preferences" | "interaction" | "usage" | "advanced" | "admin";
function useUiLanguage(): [UiLanguage, (language: UiLanguage) => void] {
  const [language, setLanguage] = useState<UiLanguage>(() => {
    const saved = localStorage.getItem("bili-scroll-ui-language");
    return saved === "zh-CN" || saved === "en" ? saved : "zh-TW";
  });
  const update = (value: UiLanguage) => {
    localStorage.setItem("bili-scroll-ui-language", value);
    setLanguage(value);
  };
  return [language, update];
}

export function App() {
  const [language, setLanguage] = useUiLanguage();
  const [user, setUser] = useState<PublicUser | null | undefined>(undefined);
  const inviteToken = useMemo(() => {
    const match = window.location.pathname.match(/^\/invite\/([^/]+)$/);
    return match?.[1] || "";
  }, []);

  const boot = useCallback(async () => {
    if (inviteToken) {
      setUser(null);
      return;
    }
    try {
      const me = await api<{ user: PublicUser }>("/api/auth/me");
      await restoreCsrf();
      setUser(me.user);
    } catch {
      setUser(null);
    }
  }, [inviteToken]);

  useEffect(() => {
    void boot();
  }, [boot]);

  if (user === undefined) return <LoadingScreen language={language} />;
  if (inviteToken) {
    return (
      <InvitePage
        token={inviteToken}
        language={language}
        onAuthenticated={(nextUser, csrf) => {
          setCsrf(csrf);
          setUser(nextUser);
          window.history.replaceState({}, "", "/");
        }}
      />
    );
  }
  if (!user) {
    return (
      <LoginPage
        language={language}
        setLanguage={setLanguage}
        onAuthenticated={(nextUser, csrf) => {
          setCsrf(csrf);
          setUser(nextUser);
        }}
      />
    );
  }
  return (
    <Dashboard
      user={user}
      language={language}
      setLanguage={setLanguage}
      onLogout={() => {
        api("/api/auth/logout", { method: "POST" }).catch(() => undefined);
        setCsrf("");
        setUser(null);
      }}
    />
  );
}

function LanguageSwitch({
  language,
  setLanguage,
}: {
  language: UiLanguage;
  setLanguage: (language: UiLanguage) => void;
}) {
  return (
    <div className="language-switch" aria-label="Language">
      {(["zh-TW", "zh-CN", "en"] as UiLanguage[]).map((value) => (
        <button
          className={language === value ? "active" : ""}
          key={value}
          onClick={() => setLanguage(value)}
        >
          {value === "zh-TW" ? "繁" : value === "zh-CN" ? "简" : "EN"}
        </button>
      ))}
    </div>
  );
}

function LoadingScreen({ language }: { language: UiLanguage }) {
  return (
    <div className="center-screen">
      <BrandGlyph large />
      <p>{tr(language, "loading")}</p>
    </div>
  );
}

function LoginPage({
  language,
  setLanguage,
  onAuthenticated,
}: {
  language: UiLanguage;
  setLanguage: (language: UiLanguage) => void;
  onAuthenticated: (user: PublicUser, csrf: string) => void;
}) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [totp, setTotp] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const result = await api<{ user: PublicUser; csrf: string }>("/api/auth/login", {
        method: "POST",
        body: JSON.stringify({ username, password, totp }),
      });
      onAuthenticated(result.user, result.csrf);
    } catch (reason) {
      setError(errorText(reason));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="auth-layout">
      <section className="auth-story">
        <div className="auth-brand-lockup">
          <BrandGlyph large />
          <div>
            <strong>{tr(language, "app")}</strong>
            <span>DAILY VIDEO INTELLIGENCE</span>
          </div>
        </div>
        <div className="eyebrow auth-eyebrow">CODEX / HERMES / CC-SWITCH</div>
        <h1>{tr(language, "app")}</h1>
        <p>{tr(language, "tagline")}</p>
        <div className="signal-list">
          <span><BadgeCheck size={13} /> 字幕原文判斷</span>
          <span><Gauge size={13} /> 每日額度與判重</span>
          <span><ShieldCheck size={13} /> 多使用者隔離</span>
          <span><Layers3 size={13} /> 寫入預設關閉</span>
        </div>
        <div className="auth-preview">
          <div className="preview-topline">
            <div className="preview-live"><Radio size={12} /> TODAY · LIVE DIGEST</div>
            <span>20:00</span>
          </div>
          <div className="preview-grid">
            <div className="preview-video">
              <div className="preview-cover">
                <Play size={22} fill="currentColor" />
              </div>
              <strong>真正讀過字幕才推薦</strong>
              <span>值得看 · Agent 工具鏈</span>
            </div>
            <div className="preview-report">
              <div><FileText size={14} /> 今日日報</div>
              <span />
              <span />
              <span className="short" />
              <div className="preview-score">
                <strong>5</strong>
                <small>則精選</small>
              </div>
            </div>
          </div>
        </div>
      </section>
      <section className="auth-card">
        <div className="auth-topline">
          <BrandGlyph />
          <LanguageSwitch language={language} setLanguage={setLanguage} />
        </div>
        <div className="auth-heading">
          <span className="eyebrow"><ShieldCheck size={12} /> SECURE ACCESS</span>
          <h2>{tr(language, "login")}</h2>
          <p>使用帳號密碼與驗證器的一次性代碼登入。</p>
        </div>
        <form onSubmit={submit} className="stack">
          <Field label={tr(language, "username")}>
            <input
              autoComplete="username"
              value={username}
              onChange={(event) => setUsername(event.target.value)}
              required
            />
          </Field>
          <Field label={tr(language, "password")}>
            <span className="input-with-action">
              <input
                type={showPassword ? "text" : "password"}
                autoComplete="current-password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                required
              />
              <button
                type="button"
                className="input-action"
                aria-label={showPassword ? "隱藏密碼" : "顯示密碼"}
                onClick={() => setShowPassword((current) => !current)}
              >
                {showPassword ? <EyeOff size={16} /> : <Eye size={16} />}
              </button>
            </span>
          </Field>
          <Field label="驗證器代碼">
            <input
              inputMode="numeric"
              pattern="[0-9]{6}"
              maxLength={6}
              autoComplete="one-time-code"
              placeholder="000000"
              className="totp-input"
              value={totp}
              onChange={(event) =>
                setTotp(event.target.value.replace(/\D/g, "").slice(0, 6))
              }
              required
            />
          </Field>
          <TotpGuide language={language} compact />
          {error ? <InlineState tone="error">{error}</InlineState> : null}
          <button className="button primary full" disabled={busy}>
            {busy ? "驗證中" : tr(language, "login")}
            {!busy ? <ArrowRight size={15} /> : null}
          </button>
        </form>
        <p className="fine-print">邀請制服務。帳號由 Admin 建立，不開放公開註冊。</p>
      </section>
    </div>
  );
}

function InvitePage({
  token,
  language,
  onAuthenticated,
}: {
  token: string;
  language: UiLanguage;
  onAuthenticated: (user: PublicUser, csrf: string) => void;
}) {
  const [info, setInfo] = useState<{ username: string; display_name: string } | null>(null);
  const [password, setPassword] = useState("");
  const [setupToken, setSetupToken] = useState("");
  const [secret, setSecret] = useState("");
  const [uri, setUri] = useState("");
  const [qr, setQr] = useState("");
  const [totp, setTotp] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api<{ username: string; display_name: string }>(
      `/api/auth/invite/${encodeURIComponent(token)}`,
    )
      .then(setInfo)
      .catch((reason) => setError(errorText(reason)));
  }, [token]);

  const accept = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const result = await api<{
        setup_token: string;
        secret: string;
        otpauth_uri: string;
        otpauth_qr: string;
      }>("/api/auth/invite/accept", {
        method: "POST",
        body: JSON.stringify({ token, password }),
      });
      setSetupToken(result.setup_token);
      setSecret(result.secret);
      setUri(result.otpauth_uri);
      setQr(result.otpauth_qr);
    } catch (reason) {
      setError(errorText(reason));
    } finally {
      setBusy(false);
    }
  };

  const verify = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const result = await api<{ user: PublicUser; csrf: string }>(
        "/api/auth/invite/verify",
        {
          method: "POST",
          body: JSON.stringify({ setup_token: setupToken, totp }),
        },
      );
      onAuthenticated(result.user, result.csrf);
    } catch (reason) {
      setError(errorText(reason));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="center-screen">
      <section className="auth-card invite-card">
        <div className="brand-row">
          <BrandGlyph />
          <div>
            <h1>接受邀請</h1>
            <p className="muted">
              {info ? `${info.display_name} · ${info.username}` : tr(language, "loading")}
            </p>
          </div>
        </div>
        {!setupToken ? (
          <form className="stack" onSubmit={accept}>
            <Field label="設定密碼（至少 12 字元）">
              <input
                type="password"
                autoComplete="new-password"
                minLength={12}
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                required
              />
            </Field>
            {error ? <InlineState tone="error">{error}</InlineState> : null}
            <button className="button primary" disabled={busy || !info}>
              建立 TOTP
            </button>
          </form>
        ) : (
          <form className="stack" onSubmit={verify}>
            <InlineState tone="warn">
              驗證器設定資料只顯示一次。先掃 QR 或手動輸入密鑰，再填寫目前的六位數。
            </InlineState>
            <TotpQrCard
              username={info?.username || ""}
              secret={secret}
              uri={uri}
              qr={qr}
            />
            <TotpGuide language={language} />
            <details>
              <summary>無法掃碼？展開 TOTP URI</summary>
              <code className="secret-box wrap">{uri}</code>
            </details>
            <Field label="驗證器目前的 6 位數">
              <input
                inputMode="numeric"
                pattern="[0-9]{6}"
                maxLength={6}
                autoComplete="one-time-code"
                placeholder="000000"
                className="totp-input"
                value={totp}
                onChange={(event) =>
                  setTotp(event.target.value.replace(/\D/g, "").slice(0, 6))
                }
                required
              />
            </Field>
            {error ? <InlineState tone="error">{error}</InlineState> : null}
            <button className="button primary" disabled={busy}>
              完成設定
            </button>
          </form>
        )}
      </section>
    </div>
  );
}

function Dashboard({
  user,
  language,
  setLanguage,
  onLogout,
}: {
  user: PublicUser;
  language: UiLanguage;
  setLanguage: (language: UiLanguage) => void;
  onLogout: () => void;
}) {
  const [page, setPage] = useState<Page>("daily");
  const [status, setStatus] = useState<StatusPayload | null>(null);
  const [toast, setToast] = useState<Toast>(null);
  const [stepUpToken, setStepUpToken] = useState("");

  const loadStatus = useCallback(async () => {
    try {
      const result = await api<StatusPayload & { ok: boolean }>("/api/me/status");
      setStatus(result);
    } catch (reason) {
      if (reason instanceof ApiError && reason.status === 401) {
        onLogout();
        return;
      }
      setToast({ tone: "error", text: errorText(reason) });
    }
  }, [onLogout]);

  useEffect(() => {
    void loadStatus();
    const timer = window.setInterval(() => void loadStatus(), 5000);
    const events = new EventSource("/api/me/events");
    const onRun = (event: MessageEvent<string>) => {
      try {
        const data = JSON.parse(event.data) as { status?: string };
        setToast({
          tone: data.status === "failed" ? "error" : "ok",
          text: data.status === "failed" ? "本輪執行失敗" : "本輪狀態已更新",
        });
      } catch {
        setToast({ tone: "ok", text: "本輪狀態已更新" });
      }
      void loadStatus();
    };
    events.addEventListener("run", onRun as EventListener);
    return () => {
      window.clearInterval(timer);
      events.close();
    };
  }, [loadStatus]);

  useEffect(() => {
    if (!toast) return;
    const timer = window.setTimeout(() => setToast(null), 5000);
    return () => window.clearTimeout(timer);
  }, [toast]);

  const saveConfig = async (
    patch: Partial<MemberConfig>,
    dangerous = false,
  ): Promise<boolean> => {
    if (dangerous && !stepUpToken) {
      setToast({ tone: "warn", text: "請先完成寫入安全確認" });
      return false;
    }
    try {
      await api("/api/me/config", {
        method: "PATCH",
        body: JSON.stringify(patch),
        stepUp: dangerous ? stepUpToken : undefined,
      });
      await loadStatus();
      setToast({ tone: "ok", text: tr(language, "success") });
      return true;
    } catch (reason) {
      setToast({ tone: "error", text: errorText(reason) });
      return false;
    }
  };

  if (!status) return <LoadingScreen language={language} />;
  const navItems: { id: Page; label: MessageKey; icon: ReactNode }[] = [
    { id: "daily", label: "daily", icon: <Sun size={17} /> },
    { id: "preferences", label: "preferences", icon: <SlidersHorizontal size={17} /> },
    { id: "interaction", label: "interaction", icon: <MessageCircleMore size={17} /> },
    { id: "usage", label: "usage", icon: <BarChart3 size={17} /> },
    { id: "advanced", label: "advanced", icon: <Settings2 size={17} /> },
  ];
  if (user.role === "admin") {
    navItems.push({ id: "admin", label: "admin", icon: <ShieldCheck size={17} /> });
  }

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand-row">
          <BrandGlyph />
          <div className="brand-copy">
            <strong>{tr(language, "app")}</strong>
            <span>Agent Service</span>
          </div>
        </div>
        <nav>
          {navItems.map((item) => (
            <button
              key={item.id}
              className={page === item.id ? "active" : ""}
              onClick={() => setPage(item.id)}
            >
              <span className="nav-icon">{item.icon}</span>
              {tr(language, item.label)}
            </button>
          ))}
        </nav>
        <div className="sidebar-bottom">
          <div className="mini-user">
            <span className="avatar">{user.display_name.slice(0, 1).toUpperCase()}</span>
            <div>
              <strong>{user.display_name}</strong>
              <small>@{user.username} · {user.role}</small>
            </div>
          </div>
          <button className="ghost-button" onClick={onLogout}>
            <LogOut size={14} /> {tr(language, "logout")}
          </button>
        </div>
      </aside>

      <main>
        <header className="topbar">
          <div>
            <div className="eyebrow">{status.version} · BILI SCROLL AGENT</div>
            <h1>{tr(language, navItems.find((item) => item.id === page)?.label || "daily")}</h1>
          </div>
          <div className="topbar-actions">
            <span className={`status-dot ${status.active_run ? "busy" : ""}`} />
            <span className="top-status">
              {status.active_run
                ? `${status.active_run.runner} · ${status.active_run.status}`
                : status.bilibili.logged_in
                  ? "服務正常"
                  : "B 站待登入"}
            </span>
            <LanguageSwitch language={language} setLanguage={setLanguage} />
          </div>
        </header>

        <div className={`page-frame ${page === "daily" ? "wide" : ""}`}>
          {page === "daily" ? (
            <DailyPage
              status={status}
              language={language}
              reload={loadStatus}
              saveConfig={saveConfig}
              setToast={setToast}
            />
          ) : null}
          {page === "preferences" ? (
            <PreferencesPage
              status={status}
              language={language}
              saveConfig={saveConfig}
              setToast={setToast}
            />
          ) : null}
          {page === "interaction" ? (
            <InteractionPage
              status={status}
              language={language}
              saveConfig={saveConfig}
              stepUpToken={stepUpToken}
              setStepUpToken={setStepUpToken}
              setToast={setToast}
            />
          ) : null}
          {page === "advanced" ? (
            <AdvancedPage
              status={status}
              language={language}
              reload={loadStatus}
              setToast={setToast}
            />
          ) : null}
          {page === "usage" ? (
            <UsagePage status={status} language={language} setToast={setToast} />
          ) : null}
          {page === "admin" && user.role === "admin" ? (
            <AdminPage language={language} setToast={setToast} runners={status.runners} />
          ) : null}
        </div>
      </main>
      {toast ? <div className={`toast ${toast.tone}`}>{toast.text}</div> : null}
    </div>
  );
}

function DailyPage({
  status,
  language,
  reload,
  saveConfig,
  setToast,
}: {
  status: StatusPayload;
  language: UiLanguage;
  reload: () => Promise<void>;
  saveConfig: (patch: Partial<MemberConfig>) => Promise<boolean>;
  setToast: (toast: Toast) => void;
}) {
  const [schedule, setSchedule] = useState(status.config.schedule);
  const [runner, setRunner] = useState(status.config.runner);
  const [perDay, setPerDay] = useState(status.config.perDay);
  const [busy, setBusy] = useState("");
  const [watched, setWatched] = useState<WatchedRow[]>([]);
  const [watchedTotal, setWatchedTotal] = useState(0);
  const [stateFilter, setStateFilter] = useState("");
  const [tierFilter, setTierFilter] = useState("");
  const [report, setReport] = useState("");
  const [reportOpen, setReportOpen] = useState(false);

  useEffect(() => {
    setSchedule(status.config.schedule);
    setRunner(status.config.runner);
    setPerDay(status.config.perDay);
  }, [status.config]);

  const loadWatched = useCallback(async () => {
    const query = new URLSearchParams({ limit: "30", offset: "0" });
    if (stateFilter) query.set("state", stateFilter);
    if (tierFilter) query.set("tier", tierFilter);
    try {
      const result = await api<{ rows: WatchedRow[]; total: number }>(
        `/api/me/watched?${query}`,
      );
      setWatched(result.rows);
      setWatchedTotal(result.total);
    } catch (reason) {
      setToast({ tone: "error", text: errorText(reason) });
    }
  }, [setToast, stateFilter, tierFilter]);

  useEffect(() => {
    void loadWatched();
  }, [loadWatched]);

  const runNow = async () => {
    if (status.active_run) return;
    setBusy("run");
    try {
      await api("/api/me/runs", { method: "POST" });
      setToast({ tone: "ok", text: tr(language, "runRequested") });
      await reload();
    } catch (reason) {
      setToast({ tone: "error", text: errorText(reason) });
      await reload();
    } finally {
      setBusy("");
    }
  };

  const cancelRun = async () => {
    if (!status.active_run) return;
    setBusy("cancel");
    try {
      await api(`/api/me/runs/${status.active_run.id}/cancel`, { method: "POST" });
      setToast({ tone: "warn", text: "已要求取消本輪" });
      await reload();
    } catch (reason) {
      setToast({ tone: "error", text: errorText(reason) });
    } finally {
      setBusy("");
    }
  };

  const showReport = async () => {
    if (reportOpen) {
      setReportOpen(false);
      return;
    }
    try {
      const result = await api<{ markdown: string; exists: boolean }>("/api/me/report");
      setReport(result.exists ? result.markdown : "今天還沒有日報。");
      setReportOpen(true);
    } catch (reason) {
      setToast({ tone: "error", text: errorText(reason) });
    }
  };

  return (
    <div className="stack large">
      <section className="metric-grid">
        <Metric label={tr(language, "pickedToday")} value={`${status.picked_today}`} suffix={`/ ${status.config.perDay}`} />
        <Metric label={tr(language, "remaining")} value={`${status.remaining_today}`} suffix="部影片" />
        <Metric
          label={tr(language, "account")}
          value={status.bilibili.logged_in ? "已連接" : "未登入"}
          suffix={status.bilibili.nickname || ""}
          tone={status.bilibili.logged_in ? "good" : "warning"}
        />
        <Metric
          label="Runner"
          value={status.config.runner.toUpperCase()}
          suffix={status.runners[status.config.runner]?.available ? "可用" : "未安裝"}
          tone={status.runners[status.config.runner]?.available ? "good" : "danger"}
        />
      </section>

      <div className="split-grid">
        <Panel title={tr(language, "schedule")} description="服務在本機執行；時區與時間都可逐人設定。">
          <div className="form-grid two">
            <Toggle
              label={tr(language, "enabled")}
              checked={schedule.enabled}
              onChange={(checked) => setSchedule({ ...schedule, enabled: checked })}
            />
            <Field label={tr(language, "time")}>
              <input
                type="time"
                value={schedule.time}
                onChange={(event) => setSchedule({ ...schedule, time: event.target.value })}
              />
            </Field>
            <Field label={tr(language, "timezone")}>
              <select
                value={schedule.timezone}
                onChange={(event) => setSchedule({ ...schedule, timezone: event.target.value })}
              >
                <option value="Asia/Shanghai">Asia/Shanghai</option>
                <option value="Asia/Taipei">Asia/Taipei</option>
                <option value="Asia/Hong_Kong">Asia/Hong_Kong</option>
                <option value="Asia/Tokyo">Asia/Tokyo</option>
                <option value="Europe/London">Europe/London</option>
                <option value="America/Los_Angeles">America/Los_Angeles</option>
                <option value="UTC">UTC</option>
              </select>
            </Field>
            <Field label={tr(language, "runner")}>
              <select value={runner} onChange={(event) => setRunner(event.target.value)}>
                {Object.entries(status.runners).map(([name, info]) => (
                  <option key={name} value={name} disabled={!info.available}>
                    {info.label || name} {info.available ? "✓" : `（${info.reason || "未安裝"}）`}
                  </option>
                ))}
              </select>
            </Field>
            <Field label={tr(language, "quota")}>
              <input
                type="number"
                min={0}
                max={50}
                value={perDay}
                onChange={(event) => setPerDay(Number(event.target.value))}
              />
            </Field>
          </div>
          <div className="button-row">
            <button
              className="button"
              disabled={Boolean(busy)}
              onClick={() =>
                void saveConfig({ schedule, runner, perDay }).then((ok) => {
                  if (ok) void reload();
                })
              }
            >
              {busy ? tr(language, "saving") : tr(language, "save")}
            </button>
            <button
              className="button primary"
              onClick={() => void runNow()}
              disabled={Boolean(busy) || Boolean(status.active_run)}
              title={status.active_run ? "目前已有執行中的 run，請先等待或取消" : undefined}
            >
              {status.active_run ? "執行中…" : tr(language, "runNow")}
            </button>
            {status.active_run ? (
              <button className="button danger" onClick={() => void cancelRun()} disabled={Boolean(busy)}>
                {tr(language, "cancel")}
              </button>
            ) : null}
          </div>
        </Panel>
        <Panel title={tr(language, "report")} description={status.schedule.next_run_at ? `下次排程：${new Date(status.schedule.next_run_at).toLocaleString()}` : "目前沒有下一步排程。"}>
          <div className="report-hero">
            <div className="report-number">{status.picked_today}</div>
            <div>
              <strong>今日精選</strong>
              <p>只計入日報中 `### BV…` 的項目，補跑不會重複佔額度。</p>
            </div>
          </div>
          <button className="button subtle full" onClick={() => void showReport()}>
            {reportOpen ? "收起日報" : "查看今日日報"}
          </button>
          {reportOpen ? <pre className="markdown-view">{report}</pre> : null}
          {status.latest_run?.error ? (
            <InlineState tone="error">{status.latest_run.error}</InlineState>
          ) : null}
        </Panel>
      </div>

      <section className="panel">
        <div className="panel-heading inline">
          <div>
            <h2>{tr(language, "viewed")}</h2>
            <p>所有挑選與跳過紀錄，依日期保存。</p>
          </div>
          <span className="count-pill">{watchedTotal}</span>
        </div>
        <div className="filter-row">
          {[
            ["", tr(language, "all")],
            ["picked", tr(language, "picked")],
            ["skipped", tr(language, "skipped")],
          ].map(([value, label]) => (
            <button
              key={value}
              className={`chip ${stateFilter === value ? "active" : ""}`}
              onClick={() => setStateFilter(value)}
            >
              {label}
            </button>
          ))}
          <span className="filter-separator" />
          {[
            ["", "全部檔位"],
            ["值得看", "值得看"],
            ["可以看", "可以看"],
            ["不值当", "不值当"],
          ].map(([value, label]) => (
            <button
              key={value}
              className={`chip ${tierFilter === value ? "active" : ""}`}
              onClick={() => setTierFilter(value)}
            >
              {label}
            </button>
          ))}
        </div>
        {watched.length ? (
          <div className="video-grid">
            {watched.map((item) => (
              <a
                className="video-card"
                key={item.bvid}
                href={item.url}
                target="_blank"
                rel="noreferrer"
              >
                {item.cover ? <img src={item.cover} alt="" loading="lazy" /> : <div className="cover-placeholder">B</div>}
                <div className="video-card-body">
                  <div className="video-card-meta">
                    <span className={`tier tier-${item.tier || "none"}`}>{item.tier || (item.picked ? "已挑選" : "已跳過")}</span>
                    <span>{item.day}</span>
                  </div>
                  <h3>{item.title || item.bvid}</h3>
                  <p>{item.up} · {item.bvid}</p>
                  {item.why ? <p className="why">{item.why}</p> : null}
                </div>
              </a>
            ))}
          </div>
        ) : (
          <EmptyState title="還沒有觀看紀錄" body="完成第一輪後，挑中與跳過的影片會出現在這裡。" />
        )}
      </section>
    </div>
  );
}

function PreferencesPage({
  status,
  language,
  saveConfig,
  setToast,
}: {
  status: StatusPayload;
  language: UiLanguage;
  saveConfig: (patch: Partial<MemberConfig>, dangerous?: boolean) => Promise<boolean>;
  setToast: (toast: Toast) => void;
}) {
  const [config, setConfig] = useState<MemberConfig>(status.config);
  const [keywords, setKeywords] = useState(status.config.keywords.join("\n"));
  const [folders, setFolders] = useState<{ id: number; title: string; count: number }[]>([]);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    setConfig(status.config);
    setKeywords(status.config.keywords.join("\n"));
  }, [status.config]);

  useEffect(() => {
    api<{ folders: { id: number; title: string; count: number }[] }>("/api/me/fav-folders")
      .then((result) => setFolders(result.folders))
      .catch(() => setFolders([]));
  }, []);

  const save = async () => {
    setBusy(true);
    const list = keywords
      .split("\n")
      .map((item) => item.trim())
      .filter(Boolean);
    const ok = await saveConfig(
      {
        ...config,
        keywords: list,
        favFolderId: config.favFolderId,
      },
      config.favFolderId > 0,
    );
    setBusy(false);
    if (!ok) setToast({ tone: "warn", text: "若包含自動收藏，請先在「互動」完成安全確認。" });
  };

  return (
    <div className="stack large">
      <Panel title={tr(language, "keywords")} description="一行一個。留空時使用系統內建的技術與創作工具關鍵字。">
        <textarea
          rows={7}
          value={keywords}
          onChange={(event) => setKeywords(event.target.value)}
          placeholder={"DeepSeek Harness\nCodex plugin\nHermes Agent\n創作工具"}
        />
      </Panel>
      <Panel title={tr(language, "persona")} description="只影響這個使用者的日報語氣；不會讀取主機任何人的 AGENTS.md。">
        <textarea
          rows={8}
          value={config.persona}
          onChange={(event) => setConfig({ ...config, persona: event.target.value })}
          placeholder="例如：用簡潔、務實的繁體中文，不要浮誇，指出影片真正可用的資訊。"
        />
      </Panel>
      <div className="split-grid">
        <Panel title={tr(language, "reportLevel")}>
          <div className="choice-stack">
            {[
              ["full", "完整", "包含標題、UP 主、連結、理由與一句話。"],
              ["brief", "薄版", "每條只留檔位與連結。"],
              ["off", "不寫日報", "仍保留判重與 archive，但不在資料庫生成日報。"],
            ].map(([value, label, note]) => (
              <Choice
                key={value}
                active={config.reportLevel === value}
                title={label}
                note={note}
                onClick={() => setConfig({ ...config, reportLevel: value as MemberConfig["reportLevel"] })}
              />
            ))}
          </div>
        </Panel>
        <Panel title="輸出與收藏">
          <div className="stack">
            <Field label={tr(language, "language")}>
              <select
                value={config.language}
                onChange={(event) =>
                  setConfig({ ...config, language: event.target.value as MemberConfig["language"] })
                }
              >
                <option value="zh-TW">繁體中文</option>
                <option value="zh-CN">简体中文</option>
                <option value="en">English</option>
              </select>
            </Field>
            <Field label="自動收藏到">
              <select
                value={config.favFolderId || 0}
                onChange={(event) =>
                  setConfig({ ...config, favFolderId: Number(event.target.value) })
                }
              >
                <option value={0}>不自動收藏</option>
                {folders.map((folder) => (
                  <option value={folder.id} key={folder.id}>
                    {folder.title}（{folder.count}）
                  </option>
                ))}
              </select>
            </Field>
            <Toggle
              label="無字幕時使用本機轉寫"
              checked={config.transcribe}
              onChange={(checked) => setConfig({ ...config, transcribe: checked })}
            />
            <Field label="Whisper 模型">
              <select
                value={config.asrModel}
                onChange={(event) =>
                  setConfig({ ...config, asrModel: event.target.value as MemberConfig["asrModel"] })
                }
              >
                {["tiny", "base", "small", "medium"].map((value) => (
                  <option key={value} value={value}>{value}</option>
                ))}
              </select>
            </Field>
          </div>
        </Panel>
      </div>
      <div className="sticky-actions">
        <button className="button primary" onClick={() => void save()} disabled={busy}>
          {busy ? tr(language, "saving") : tr(language, "save")}
        </button>
      </div>
    </div>
  );
}

function InteractionPage({
  status,
  language,
  saveConfig,
  stepUpToken,
  setStepUpToken,
  setToast,
}: {
  status: StatusPayload;
  language: UiLanguage;
  saveConfig: (patch: Partial<MemberConfig>, dangerous?: boolean) => Promise<boolean>;
  stepUpToken: string;
  setStepUpToken: (token: string) => void;
  setToast: (toast: Toast) => void;
}) {
  const [config, setConfig] = useState(status.config);
  const [password, setPassword] = useState("");
  const [totp, setTotp] = useState("");
  const [verifying, setVerifying] = useState(false);
  const [busy, setBusy] = useState(false);

  useEffect(() => setConfig(status.config), [status.config]);

  const verify = async () => {
    setVerifying(true);
    try {
      const result = await api<{ token: string }>("/api/auth/step-up", {
        method: "POST",
        body: JSON.stringify({ password, totp }),
      });
      setStepUpToken(result.token);
      setPassword("");
      setTotp("");
      setToast({ tone: "ok", text: "寫入已解鎖 5 分鐘" });
    } catch (reason) {
      setToast({ tone: "error", text: errorText(reason) });
    } finally {
      setVerifying(false);
    }
  };

  const save = async () => {
    setBusy(true);
    const dangerous =
      config.autoComment ||
      config.replyToComments ||
      config.dmWatch ||
      config.dmMode !== "off";
    await saveConfig(config, dangerous);
    setBusy(false);
  };

  return (
    <div className="stack large">
      <InlineState tone="warn">
        B 站上的每一個字都會算在你的帳號上。這些能力預設全關；啟用後排程仍會逐次檢查 Admin 全域開關與額度。
      </InlineState>
      <div className="split-grid">
        <Panel title={tr(language, "delivery")} description="私訊是同一個使用者自己收；公開評論會出現在影片下方。">
          <div className="choice-stack">
            {[
              ["off", "只寫日報", "不公开发言，最安全。"],
              ["only", "只發私信", "私訊合併成單條，最多 500 字。"],
              ["both", "私信加評論", "有設定 @ 名稱時，公開評論另發。"],
            ].map(([value, label, note]) => (
              <Choice
                key={value}
                active={config.dmMode === value}
                title={label}
                note={note}
                onClick={() => setConfig({ ...config, dmMode: value as MemberConfig["dmMode"] })}
              />
            ))}
          </div>
          <div className="form-grid two top-gap">
            <Field label="@ 名稱">
              <input
                value={config.mentionName}
                onChange={(event) => setConfig({ ...config, mentionName: event.target.value })}
                placeholder="你的 B 站暱稱"
              />
            </Field>
            <Field label="私信 UID">
              <input
                type="number"
                min={0}
                value={config.dmUid || 0}
                onChange={(event) => setConfig({ ...config, dmUid: Number(event.target.value) })}
              />
            </Field>
          </div>
        </Panel>
        <Panel title={tr(language, "autoComment")} description="每輪最多 20 條；同一影片只發一次。">
          <div className="stack">
            <Toggle
              label="啟用公開評論"
              checked={config.autoComment}
              onChange={(checked) => setConfig({ ...config, autoComment: checked })}
            />
            <Field label="每輪最多幾條">
              <input
                type="number"
                min={0}
                max={20}
                value={config.commentPerRound}
                onChange={(event) =>
                  setConfig({ ...config, commentPerRound: Number(event.target.value) })
                }
              />
            </Field>
            <Toggle
              label="回覆直接回覆我的評論"
              checked={config.replyToComments}
              onChange={(checked) => setConfig({ ...config, replyToComments: checked })}
            />
            <Field label="每輪最多回覆幾條">
              <input
                type="number"
                min={0}
                max={20}
                value={config.replyPerRound}
                onChange={(event) =>
                  setConfig({ ...config, replyPerRound: Number(event.target.value) })
                }
              />
            </Field>
          </div>
        </Panel>
      </div>
      <Panel title={tr(language, "dmWatch")} description="只監聽設定的主人 UID；一般輪詢不消耗模型 token。">
        <div className="form-grid three">
          <Toggle
            label="啟用私信監聽"
            checked={config.dmWatch}
            onChange={(checked) => setConfig({ ...config, dmWatch: checked })}
          />
          <Field label="檢查間隔（分鐘）">
            <input
              type="number"
              min={3}
              max={1440}
              value={config.dmWatchMinutes}
              onChange={(event) =>
                setConfig({ ...config, dmWatchMinutes: Number(event.target.value) })
              }
            />
          </Field>
          <Field label="每日最多回覆">
            <input
              type="number"
              min={0}
              max={200}
              value={config.dmReplyPerDay}
              onChange={(event) =>
                setConfig({ ...config, dmReplyPerDay: Number(event.target.value) })
              }
            />
          </Field>
        </div>
      </Panel>
      <Panel
        title={tr(language, "stepUp")}
        description={stepUpToken ? "已解鎖，最多維持五分鐘。" : "修改任何公開寫入設定前，需要再確認一次身分。"}
      >
        {stepUpToken ? (
          <InlineState tone="ok">寫入權限已驗證，可以儲存設定。</InlineState>
        ) : (
          <div className="form-grid three">
            <Field label={tr(language, "password")}>
              <input
                type="password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
              />
            </Field>
            <Field label={tr(language, "totp")}>
              <input
                inputMode="numeric"
                value={totp}
                onChange={(event) => setTotp(event.target.value)}
              />
            </Field>
            <div className="field align-end">
              <button className="button" disabled={verifying} onClick={() => void verify()}>
                {verifying ? "驗證中…" : "解鎖寫入"}
              </button>
            </div>
          </div>
        )}
      </Panel>
      <div className="sticky-actions">
        <button className="button primary" disabled={busy} onClick={() => void save()}>
          {busy ? tr(language, "saving") : tr(language, "save")}
        </button>
      </div>
    </div>
  );
}

function UsagePage({
  status,
  language,
  setToast,
}: {
  status: StatusPayload;
  language: UiLanguage;
  setToast: (toast: Toast) => void;
}) {
  const [days, setDays] = useState(30);
  const [usage, setUsage] = useState<UsagePayload | null>(null);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const result = await api<UsagePayload & { ok: boolean }>(
        `/api/me/usage?days=${days}`,
      );
      setUsage(result);
    } catch (reason) {
      setToast({ tone: "error", text: errorText(reason) });
    } finally {
      setLoading(false);
    }
  }, [days, setToast]);

  useEffect(() => {
    void load();
  }, [load]);

  const visibleDaily = (usage?.daily || []).slice(-14);
  const maxCost = Math.max(0.000001, ...visibleDaily.map((row) => row.cost_usd));
  const refreshBalance = async () => {
    try {
      await api("/api/me/balance/refresh", { method: "POST" });
      await load();
      setToast({ tone: "ok", text: "Provider 餘額已更新" });
    } catch (reason) {
      setToast({ tone: "error", text: errorText(reason) });
    }
  };
  return (
    <div className="stack large">
      <div className="usage-toolbar">
        <div>
          <span className="eyebrow">PROVIDER · TOKENS · COST · BALANCE</span>
          <p>服務 run 的實際使用量，加上 cc-switch 匯入歷史；餘額由各 provider 的 adapter 原生查詢。</p>
        </div>
        <div className="filter-row">
          {[7, 30, 90].map((value) => (
            <button
              key={value}
              className={`chip ${days === value ? "active" : ""}`}
              onClick={() => setDays(value)}
            >
              {value} 天
            </button>
          ))}
        </div>
      </div>
      <section className="metric-grid">
        <Metric
          label="總成本"
          value={usage ? `$${usage.totals.cost_usd.toFixed(3)}` : "—"}
          suffix="USD"
        />
        <Metric
          label="總請求"
          value={usage ? usage.totals.requests.toLocaleString() : "—"}
          suffix="requests"
        />
        <Metric
          label="真實消耗 Tokens"
          value={
            usage
              ? compactNumber(
                  usage.totals.input_tokens + usage.totals.output_tokens,
                )
              : "—"
          }
          suffix={usage ? `cache ${compactNumber(usage.totals.cache_read_tokens)}` : ""}
        />
        <Metric
          label="快取命中率"
          value={usage ? `${usage.totals.cache_hit_percent.toFixed(1)}%` : "—"}
          suffix="cache read"
          tone={usage && usage.totals.cache_hit_percent >= 70 ? "good" : undefined}
        />
      </section>
      <div className="split-grid usage-main">
        <Panel title="每日成本" description={`最近 ${days} 天 · ${usage?.since || ""} 起`}>
          {visibleDaily.length ? (
            <div className="bar-chart" aria-label="Daily model cost chart">
              {visibleDaily.map((item) => (
                <div className="bar-column" key={item.date} title={`${item.date} · $${item.cost_usd.toFixed(4)}`}>
                  <div
                    className="bar"
                    style={{ height: `${Math.max(4, (item.cost_usd / maxCost) * 100)}%` }}
                  />
                  <span>{item.date.slice(5)}</span>
                </div>
              ))}
            </div>
          ) : (
            <EmptyState
              title={loading ? "讀取中…" : "尚無匯入用量"}
              body="執行下方本機同步腳本，即可讀取 cc-switch 的 provider、model 與成本統計。"
            />
          )}
        </Panel>
        <Panel title="Provider 剩餘額度" description="由各 provider adapter 原生查詢；API key 由服務端加密保存。">
          <button className="button subtle full" onClick={() => void refreshBalance()}>
            重新整理餘額
          </button>
          {usage?.balances.length ? (
            <div className="balance-grid">
              {usage.balances.map((balance) => (
                <div className={`balance-card ${balance.low ? "low" : ""}`} key={`${balance.provider}-${balance.currency}`}>
                  <span className="eyebrow">{balance.provider} · {balance.currency}</span>
                  <strong>{balance.total.toFixed(2)}</strong>
                  <div>
                    <span>贈送 {balance.granted.toFixed(2)}</span>
                    <span>儲值 {balance.topped_up.toFixed(2)}</span>
                  </div>
                  <small>{new Date(balance.fetched_at).toLocaleString()}</small>
                </div>
              ))}
            </div>
          ) : (
            <EmptyState
              title="等待餘額快照"
              body="請由 Admin 在 Providers 設定保存 API key，然後按「重新整理餘額」。"
            />
          )}
        </Panel>
      </div>
      <Panel title="Provider / Model 明細" description="以匯入的 cc-switch 日彙總為準；同名 provider 可分別對照 Codex 與 Hermes。">
        <div className="usage-table-wrap">
          <table className="usage-table">
            <thead>
              <tr>
                <th>應用</th>
                <th>供應商</th>
                <th>模型</th>
                <th>請求</th>
                <th>輸入</th>
                <th>輸出</th>
                <th>快取</th>
                <th>成本</th>
              </tr>
            </thead>
            <tbody>
              {(usage?.providers || []).map((row) => (
                <tr key={`${row.app_type}-${row.provider}-${row.model}`}>
                  <td><span className="badge">{row.app_type}</span></td>
                  <td>{row.provider || "default"}</td>
                  <td><code>{row.model || "default"}</code></td>
                  <td>{row.requests.toLocaleString()}</td>
                  <td>{compactNumber(row.input_tokens)}</td>
                  <td>{compactNumber(row.output_tokens)}</td>
                  <td>{compactNumber(row.cache_read_tokens)}</td>
                  <td>${row.cost_usd.toFixed(4)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {!usage?.providers.length ? <p className="muted table-empty">尚未匯入 provider 用量。</p> : null}
        </div>
      </Panel>
      <div className="split-grid">
        <Panel title="目前 Runner 路由" description="VPS 使用服務自己的 Codex/Hermes 憑證；cc-switch 負責本機 agent 接入與用量來源。">
          <div className="route-card">
            <span className="eyebrow">ACTIVE RUNNER</span>
            <strong>{status.config.runner.toUpperCase()}</strong>
            <code>{status.runners[status.config.runner]?.command || "not configured"}</code>
            <div className="route-model">
              <span>{status.runner_route.provider || "cc-switch / runtime default"}</span>
              <span>{status.runner_route.model || "model follows local config"}</span>
            </div>
            <span className={`badge ${status.runners[status.config.runner]?.available ? "good" : "warn"}`}>
              {status.runners[status.config.runner]?.available ? "available" : "missing"}
            </span>
          </div>
        </Panel>
        <Panel title="cc-switch 歷史用量同步" description="只同步每日 provider/model/token/cost 彙總，不會上傳 provider 金鑰或對話內容。">
          <pre className="markdown-view command-view">{`set BILI_SCROLL_TOKEN=你的裝置權杖
set BILI_SCROLL_SERVER=${window.location.origin}

bin\\bili-scroll-usage-sync.exe --watch
`}</pre>
        </Panel>
      </div>
      <Panel title="Bili Scroll run 成本" description="從每次 agent 的 JSONL／usage file 擷取；缺少 usage 時顯示空白，不推測成本。">
        <div className="run-usage-list">
          {usage?.runs.length ? usage.runs.map((run) => (
            <div key={run.id}>
              <span className="badge">{run.runner}</span>
              <span className={`badge ${run.status === "succeeded" ? "good" : "warn"}`}>
                {run.status}
              </span>
              <code>{run.provider || "default"} / {run.model || "default"}</code>
              <span>{compactNumber(run.input_tokens)} in · {compactNumber(run.output_tokens)} out</span>
              <strong>${run.cost_usd.toFixed(4)}</strong>
              <time>{run.finished_at ? new Date(run.finished_at).toLocaleString() : ""}</time>
            </div>
          )) : <p className="muted">尚無成功的 run。</p>}
        </div>
      </Panel>
    </div>
  );
}

function AdvancedPage({
  status,
  language,
  reload,
  setToast,
}: {
  status: StatusPayload;
  language: UiLanguage;
  reload: () => Promise<void>;
  setToast: (toast: Toast) => void;
}) {
  const [qr, setQr] = useState("");
  const [loginKey, setLoginKey] = useState("");
  const [loginState, setLoginState] = useState("");
  const [tokens, setTokens] = useState<TokenRow[]>([]);
  const [newToken, setNewToken] = useState("");
  const [tokenLabel, setTokenLabel] = useState("My Codex / Hermes client");
  const [diagnostics, setDiagnostics] = useState("");
  const [deliveries, setDeliveries] = useState<DeliveryRow[]>([]);
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [passwordTOTP, setPasswordTOTP] = useState("");
  const [passwordBusy, setPasswordBusy] = useState(false);
  const timer = useRef<number | null>(null);

  const loadTokens = useCallback(async () => {
    try {
      const result = await api<{ tokens: TokenRow[] }>("/api/me/tokens");
      setTokens(result.tokens);
    } catch {
      setTokens([]);
    }
  }, []);

  const loadDeliveries = useCallback(async () => {
    try {
      const result = await api<{ rows: DeliveryRow[] }>("/api/me/deliveries");
      setDeliveries(result.rows);
    } catch {
      setDeliveries([]);
    }
  }, []);

  useEffect(() => {
    void loadTokens();
    void loadDeliveries();
    return () => {
      if (timer.current) window.clearInterval(timer.current);
    };
  }, [loadDeliveries, loadTokens]);

  const startLogin = async () => {
    try {
      const result = await api<{ key: string; qr: string }>("/api/me/login/start", {
        method: "POST",
      });
      setQr(result.qr);
      setLoginKey(result.key);
      setLoginState("請用 B 站手機 App 掃碼");
      if (timer.current) window.clearInterval(timer.current);
      timer.current = window.setInterval(async () => {
        try {
          const polled = await api<{ state: string; message?: string }>(
            "/api/me/login/poll",
            {
              method: "POST",
              body: JSON.stringify({ key: result.key }),
            },
          );
          if (polled.state === "ok") {
            if (timer.current) window.clearInterval(timer.current);
            timer.current = null;
            setQr("");
            setLoginState("登入成功");
            await reload();
          } else if (polled.state === "scanned") {
            setLoginState("已掃碼，請在手機確認");
          } else if (polled.state === "expired") {
            if (timer.current) window.clearInterval(timer.current);
            timer.current = null;
            setQr("");
            setLoginState("二維碼已過期");
          }
        } catch {
          // transient polling failures are ignored
        }
      }, 2000);
    } catch (reason) {
      setToast({ tone: "error", text: errorText(reason) });
    }
  };

  const createToken = async () => {
    try {
      const result = await api<{ token: string }>("/api/me/tokens", {
        method: "POST",
        body: JSON.stringify({ label: tokenLabel }),
      });
      setNewToken(result.token);
      await loadTokens();
    } catch (reason) {
      setToast({ tone: "error", text: errorText(reason) });
    }
  };

  const revokeToken = async (prefix: string) => {
    try {
      await api(`/api/me/tokens/${prefix}`, { method: "DELETE" });
      await loadTokens();
    } catch (reason) {
      setToast({ tone: "error", text: errorText(reason) });
    }
  };

  const diagnose = async () => {
    try {
      const result = await api<Record<string, unknown>>("/api/me/diagnostics");
      setDiagnostics(JSON.stringify(result, null, 2));
    } catch (reason) {
      setToast({ tone: "error", text: errorText(reason) });
    }
  };

  const changePassword = async () => {
    setPasswordBusy(true);
    try {
      await api("/api/auth/change-password", {
        method: "POST",
        body: JSON.stringify({
          current_password: currentPassword,
          new_password: newPassword,
          totp: passwordTOTP,
        }),
      });
      setToast({ tone: "ok", text: "密碼已更新，請重新登入" });
      window.setTimeout(() => window.location.reload(), 900);
    } catch (reason) {
      setToast({ tone: "error", text: errorText(reason) });
    } finally {
      setPasswordBusy(false);
    }
  };

  return (
    <div className="stack large">
      <div className="split-grid">
        <Panel title={tr(language, "account")} description="憑證以 AES-GCM 加密後保存，不會回傳到任何 AI 模型。">
          <div className="account-state">
            <div className={`status-orb ${status.bilibili.logged_in ? "good" : "warn"}`} />
            <div>
              <strong>
                {status.bilibili.logged_in
                  ? status.bilibili.nickname || "B 站已登入"
                  : tr(language, "notLoggedIn")}
              </strong>
              <p>{status.bilibili.uid ? `UID ${status.bilibili.uid}` : "掃碼後才能搜尋、讀字幕與投遞。"}</p>
            </div>
          </div>
          <div className="qr-row">
            {qr ? <img className="qr-image" src={qr} alt="Bilibili login QR" /> : null}
            <div className="stack">
              <button className="button" onClick={() => void startLogin()}>
                {tr(language, "scanLogin")}
              </button>
              {loginState ? <span className="muted">{loginState}</span> : null}
              {loginKey ? <code className="tiny-code">key {loginKey.slice(0, 12)}…</code> : null}
            </div>
          </div>
        </Panel>
        <Panel title={tr(language, "health")} description="Runner 與 B 站依賴必須在 VPS service account 內可用。">
          <div className="health-list">
            {Object.entries(status.runners).map(([name, info]) => (
              <div key={name}>
                <span className={`status-orb ${info.available ? "good" : "bad"}`} />
                <strong>{name}</strong>
                <code>{info.command}</code>
                <span className="muted">{info.available ? "available" : "missing"}</span>
              </div>
            ))}
          </div>
          <div className="button-row">
            <button className="button" onClick={() => void diagnose()}>生成診斷</button>
            <a className="button" href="/api/me/export">匯出 JSON</a>
            <a className="button" href="/api/health" target="_blank" rel="noreferrer">API 健康</a>
          </div>
          {diagnostics ? <pre className="markdown-view">{diagnostics}</pre> : null}
        </Panel>
      </div>

      <Panel title="變更密碼" description="需要目前密碼與 TOTP。成功後會撤銷所有 Web session。">
        <div className="form-grid three">
          <Field label="目前密碼">
            <input
              type="password"
              autoComplete="current-password"
              value={currentPassword}
              onChange={(event) => setCurrentPassword(event.target.value)}
            />
          </Field>
          <Field label="新密碼（至少 12 字元）">
            <input
              type="password"
              autoComplete="new-password"
              minLength={12}
              value={newPassword}
              onChange={(event) => setNewPassword(event.target.value)}
            />
          </Field>
          <Field label="目前 TOTP">
            <input
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={6}
              value={passwordTOTP}
              onChange={(event) =>
                setPasswordTOTP(event.target.value.replace(/\D/g, "").slice(0, 6))
              }
            />
          </Field>
        </div>
        <div className="button-row">
          <button
            className="button"
            disabled={
              passwordBusy ||
              !currentPassword ||
              newPassword.length < 12 ||
              passwordTOTP.length !== 6
            }
            onClick={() => void changePassword()}
          >
            {passwordBusy ? "更新中…" : "更新密碼"}
          </button>
        </div>
      </Panel>

      <Panel
        title={tr(language, "tokens")}
        description="同一組 MCP 工具可透過 cc-switch 同步到 Codex 與 Hermes。權杖建立後只顯示一次。"
      >
        <div className="token-create">
          <input value={tokenLabel} onChange={(event) => setTokenLabel(event.target.value)} />
          <button className="button primary" onClick={() => void createToken()}>
            {tr(language, "createToken")}
          </button>
        </div>
        {newToken ? (
          <div className="secret-panel">
            <span>複製並保存；關閉後無法再次查看。</span>
            <code>{newToken}</code>
          </div>
        ) : null}
        <div className="data-list">
          {tokens.map((token) => (
            <div key={token.id_hash}>
              <div>
                <strong>{token.label}</strong>
                <small>{token.id_hash} · 最後使用 {token.last_used_at ? new Date(token.last_used_at).toLocaleString() : "從未"}</small>
              </div>
              <button className="text-button danger-text" onClick={() => void revokeToken(token.id_hash)}>
                撤銷
              </button>
            </div>
          ))}
        </div>
      </Panel>

      <Panel title="投遞紀錄" description="回執只代表接口接受；公開可見性需另外複核。">
        <div className="data-list">
          {deliveries.length ? deliveries.map((row) => (
            <div key={row.id}>
              <div>
                <strong>
                  {row.kind === "comment"
                    ? "公開評論"
                    : row.kind === "reply"
                      ? "回覆評論"
                      : row.kind === "dm_reply"
                        ? "私信回覆"
                        : "私信"} · {row.status}
                </strong>
                <p>{row.content}</p>
                <small>{row.bvid || "—"} · {new Date(row.created_at).toLocaleString()}</small>
              </div>
              <span className={`badge ${row.visible === true ? "good" : row.visible === false ? "warn" : ""}`}>
                {row.visible === true ? "已見" : row.visible === false ? "待審/不可見" : "未核"}
              </span>
            </div>
          )) : <EmptyState title="尚無投遞" body="所有寫入預設關閉，因此這裡可能是空的。" />}
        </div>
      </Panel>
    </div>
  );
}

function AdminPage({
  language,
  setToast,
  runners,
}: {
  language: UiLanguage;
  setToast: (toast: Toast) => void;
  runners: Record<string, RunnerInfo>;
}) {
  const [users, setUsers] = useState<Record<string, unknown>[]>([]);
  const [invites, setInvites] = useState<Record<string, unknown>[]>([]);
  const [audit, setAudit] = useState<Record<string, unknown>[]>([]);
  const [settings, setSettings] = useState<Record<string, unknown>>({});
  const [username, setUsername] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [role, setRole] = useState("member");
  const [inviteUrl, setInviteUrl] = useState("");
  const [resetInfo, setResetInfo] = useState<{
    username: string;
    secret: string;
    otpauth_uri: string;
    otpauth_qr: string;
  } | null>(null);

  const load = useCallback(async () => {
    try {
      const [userResult, inviteResult, auditResult, settingResult] = await Promise.all([
        api<{ users: Record<string, unknown>[] }>("/api/admin/users"),
        api<{ invites: Record<string, unknown>[] }>("/api/admin/invites"),
        api<{ events: Record<string, unknown>[] }>("/api/admin/audit"),
        api<{ settings: Record<string, unknown> }>("/api/admin/settings"),
      ]);
      setUsers(userResult.users);
      setInvites(inviteResult.invites);
      setAudit(auditResult.events);
      setSettings(settingResult.settings);
    } catch (reason) {
      setToast({ tone: "error", text: errorText(reason) });
    }
  }, [setToast]);

  useEffect(() => {
    void load();
  }, [load]);

  const createInvite = async (event: FormEvent) => {
    event.preventDefault();
    try {
      const result = await api<{ invite_url: string }>("/api/admin/invites", {
        method: "POST",
        body: JSON.stringify({ username, display_name: displayName, role }),
      });
      setInviteUrl(result.invite_url);
      setUsername("");
      setDisplayName("");
      await load();
    } catch (reason) {
      setToast({ tone: "error", text: errorText(reason) });
    }
  };

  const saveSettings = async () => {
    try {
      await api("/api/admin/settings", {
        method: "PUT",
        body: JSON.stringify(settings),
      });
      setToast({ tone: "ok", text: "管理設定已儲存" });
      await load();
    } catch (reason) {
      setToast({ tone: "error", text: errorText(reason) });
    }
  };

  const patchUser = async (userId: string, patch: Record<string, unknown>) => {
    try {
      await api(`/api/admin/users/${userId}`, {
        method: "PATCH",
        body: JSON.stringify(patch),
      });
      await load();
    } catch (reason) {
      setToast({ tone: "error", text: errorText(reason) });
    }
  };

  const resetTotp = async (userId: string) => {
    try {
      const result = await api<{
        username: string;
        secret: string;
        otpauth_uri: string;
        otpauth_qr: string;
      }>(
        `/api/admin/users/${userId}/reset-totp`,
        { method: "POST" },
      );
      setResetInfo(result);
      await load();
    } catch (reason) {
      setToast({ tone: "error", text: errorText(reason) });
    }
  };

  return (
    <div className="stack large">
      <div className="metric-grid compact">
        <Metric label="使用者" value={String(users.length)} suffix="accounts" />
        <Metric label="邀請" value={String(invites.length)} suffix="links" />
        <Metric label="稽核事件" value={String(audit.length)} suffix="latest" />
      </div>
      <Panel title={tr(language, "invite")} description="邀請 24 小時內有效，每人只能使用一次。">
        <form className="form-grid four" onSubmit={createInvite}>
          <Field label="帳號">
            <input value={username} onChange={(event) => setUsername(event.target.value.toLowerCase())} required />
          </Field>
          <Field label="顯示名稱">
            <input value={displayName} onChange={(event) => setDisplayName(event.target.value)} required />
          </Field>
          <Field label="角色">
            <select value={role} onChange={(event) => setRole(event.target.value)}>
              <option value="member">Member</option>
              <option value="admin">Admin</option>
            </select>
          </Field>
          <div className="field align-end">
            <button className="button primary">建立邀請</button>
          </div>
        </form>
        {inviteUrl ? (
          <div className="secret-panel">
            <span>一次性邀請連結</span>
            <code>{inviteUrl}</code>
            <button className="text-button" onClick={() => void navigator.clipboard.writeText(inviteUrl)}>
              複製
            </button>
          </div>
        ) : null}
      </Panel>
      <div className="split-grid">
        <Panel title={tr(language, "users")}>
          <div className="data-list">
            {users.map((item) => (
              <div key={String(item.id)}>
                <div>
                  <strong>{String(item.display_name)} <span className="role-tag">{String(item.role)}</span></strong>
                  <small>@{String(item.username)} · {String(item.runner)} · {item.bilibili_logged_in ? "B 站已連" : "B 站未連"}</small>
                </div>
                <div className="user-actions">
                  <span className={`badge ${item.disabled ? "warn" : "good"}`}>
                    {item.disabled ? "停用" : "啟用"}
                  </span>
                  <button
                    className="text-button"
                    onClick={() => void patchUser(String(item.id), { disabled: !item.disabled })}
                  >
                    {item.disabled ? "恢復" : "停用"}
                  </button>
                  <button className="text-button" onClick={() => void resetTotp(String(item.id))}>
                    重設 TOTP
                  </button>
                </div>
              </div>
            ))}
          </div>
        </Panel>
        <Panel title="全域限制">
          <div className="stack">
            <Toggle
              label="允許所有使用者寫入 B 站"
              checked={Boolean(settings.outbound_enabled)}
              onChange={(checked) => setSettings({ ...settings, outbound_enabled: checked })}
            />
            <div className="form-grid two">
              {[
                ["default_runner", "預設 Runner"],
                ["global_concurrency", "全域並行"],
                ["manual_runs_per_day", "每人每日手動"],
                ["scheduled_runs_per_day", "每人每日排程"],
                ["retention_days", "資料保留天數"],
              ].map(([key, label]) => (
                <Field key={key} label={label}>
                  <input
                    value={String(settings[key] ?? "")}
                    onChange={(event) =>
                      setSettings({
                        ...settings,
                        [key]: key === "default_runner" ? event.target.value : Number(event.target.value),
                      })
                    }
                  />
                </Field>
              ))}
            </div>
            <p className="muted">
              Runner 路由（provider / model / reasoning）每人各自套用；留空表示沿用執行環境原本的設定。
            </p>
            {Object.keys(runners).map((runnerName) => {
              const routes = (settings.runner_routes as Record<string, Record<string, string>>) || {};
              const route = routes[runnerName] || {};
              const update = (patch: Record<string, string>) =>
                setSettings({
                  ...settings,
                  runner_routes: { ...routes, [runnerName]: { ...route, ...patch } },
                });
              return (
                <div className="route-grid" key={runnerName}>
                  <strong>{(runners[runnerName] as { label?: string })?.label || runnerName}</strong>
                  <input
                    placeholder="provider"
                    value={route.provider || ""}
                    onChange={(event) => update({ provider: event.target.value })}
                  />
                  <input
                    placeholder="model"
                    value={route.model || ""}
                    onChange={(event) => update({ model: event.target.value })}
                  />
                  <input
                    placeholder="reasoning"
                    value={route.reasoning || ""}
                    onChange={(event) => update({ reasoning: event.target.value })}
                  />
                </div>
              );
            })}
            <button className="button" onClick={() => void saveSettings()}>儲存全域設定</button>
            <button
              className="button subtle"
              onClick={() =>
                void api<{ path: string }>("/api/admin/backup", { method: "POST" })
                  .then((result) => setToast({ tone: "ok", text: `備份完成：${result.path}` }))
                  .catch((reason) => setToast({ tone: "error", text: errorText(reason) }))
              }
            >
              立即備份資料庫
            </button>
          </div>
        </Panel>
      </div>
      {resetInfo ? (
        <Panel title="新的 TOTP 設定" description="只顯示這次；讓使用者當面掃碼，或透過安全管道交付。">
          <TotpQrCard
            username={resetInfo.username}
            secret={resetInfo.secret}
            uri={resetInfo.otpauth_uri}
            qr={resetInfo.otpauth_qr}
          />
        </Panel>
      ) : null}
      <Panel title="邀請紀錄" description="未使用的邀請在到期前仍可接受。">
        <div className="data-list">
          {invites.length ? invites.map((item) => (
            <div key={String(item.id)}>
              <div>
                <strong>{String(item.display_name)} · @{String(item.username)}</strong>
                <small>{String(item.role)} · {item.used_at ? `已使用 ${new Date(String(item.used_at)).toLocaleString()}` : `到期 ${new Date(String(item.expires_at)).toLocaleString()}`}</small>
              </div>
              <span className={`badge ${item.used_at ? "" : "good"}`}>
                {item.used_at ? "已使用" : "待接受"}
              </span>
            </div>
          )) : <p className="muted">尚無邀請。</p>}
        </div>
      </Panel>
      <AdminProviders setToast={setToast} />
      <Panel title={tr(language, "audit")}>
        <div className="audit-list">
          {audit.slice(0, 80).map((item) => (
            <div key={String(item.id)}>
              <code>{String(item.event)}</code>
              <span>{String(item.target || item.user_id || "—")}</span>
              <time>{new Date(String(item.created_at)).toLocaleString()}</time>
            </div>
          ))}
        </div>
      </Panel>
    </div>
  );
}

function TotpGuide({
  language,
  compact = false,
}: {
  language: UiLanguage;
  compact?: boolean;
}) {
  const copy = {
    "zh-TW": {
      title: "第一次使用驗證器？",
      intro: "支援 Google Authenticator、Microsoft Authenticator、1Password 或任何 TOTP App。",
      steps: [
        "在驗證器中選「新增帳號」→「掃描 QR Code」。",
        "掃碼失敗時選「手動輸入設定金鑰」，貼上畫面上的密鑰。",
        "輸入 App 顯示的 6 位數；每 30 秒更新一次。",
      ],
      clock: "如果代碼一直被拒絕，先開啟手機的自動時間校正。",
    },
    "zh-CN": {
      title: "第一次使用验证器？",
      intro: "支持 Google Authenticator、Microsoft Authenticator、1Password 或任何 TOTP App。",
      steps: [
        "在验证器中选“添加账号”→“扫描 QR Code”。",
        "扫码失败时选“手动输入设置密钥”，粘贴页面上的密钥。",
        "输入 App 显示的 6 位数字；每 30 秒更新一次。",
      ],
      clock: "如果代码一直被拒绝，请先开启手机的自动时间校准。",
    },
    en: {
      title: "New to authenticator apps?",
      intro: "Works with Google Authenticator, Microsoft Authenticator, 1Password, or any TOTP app.",
      steps: [
        "Choose Add account → Scan QR code in your authenticator.",
        "If scanning fails, choose Enter setup key and paste the secret shown above.",
        "Enter the current 6-digit code. It rotates every 30 seconds.",
      ],
      clock: "If codes are rejected, enable automatic clock synchronization on your phone.",
    },
  }[language];

  return (
    <details className={`totp-guide ${compact ? "compact" : ""}`}>
      <summary>
        <Smartphone size={15} />
        <span>{copy.title}</span>
      </summary>
      <div className="totp-guide-body">
        <p>{copy.intro}</p>
        <ol>
          {copy.steps.map((step) => <li key={step}>{step}</li>)}
        </ol>
        <small>{copy.clock}</small>
      </div>
    </details>
  );
}

function TotpQrCard({
  username,
  secret,
  uri,
  qr,
}: {
  username: string;
  secret: string;
  uri: string;
  qr: string;
}) {
  return (
    <div className="totp-onboarding">
      <div className="totp-qr-panel">
        {qr ? (
          <img src={qr} alt={`${username || "Bili Scroll Agent"} 的 TOTP QR Code`} />
        ) : (
          <span className="totp-qr-placeholder"><QrCode size={42} /></span>
        )}
        <div>
          <strong><ScanLine size={14} /> 掃描加入驗證器</strong>
          <span>{username ? `Bili Scroll Agent · ${username}` : "Bili Scroll Agent"}</span>
        </div>
      </div>
      <div className="totp-secret-panel">
        <div className="secret-label">
          <span><KeyRound size={14} /> 無法掃碼時，手動輸入密鑰</span>
          <CopyButton value={secret} label="複製密鑰" />
        </div>
        <code>{secret}</code>
        <div className="totp-secret-actions">
          <span>類型：TOTP · 6 位數 · 30 秒</span>
          <CopyButton value={uri} label="複製設定連結" />
        </div>
      </div>
    </div>
  );
}

function CopyButton({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1600);
    } catch {
      setCopied(false);
    }
  };
  return (
    <button type="button" className="copy-button" onClick={() => void copy()}>
      {copied ? <Check size={13} /> : <Copy size={13} />}
      {copied ? "已複製" : label}
    </button>
  );
}

function BrandGlyph({ large = false }: { large?: boolean }) {
  return (
    <span className={`brand-mark ${large ? "large" : ""}`} aria-hidden="true">
      <svg viewBox="0 0 24 24" fill="none">
        <path
          d="M7.4 5.1 5.7 3.4M16.6 5.1l1.7-1.7M4.2 7.2h15.6c1 0 1.7.8 1.7 1.7v8.6c0 1-.8 1.7-1.7 1.7H4.2c-1 0-1.7-.8-1.7-1.7V8.9c0-1 .8-1.7 1.7-1.7Z"
          stroke="currentColor"
          strokeWidth="1.7"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
        <path
          d="m10.2 10.3 4.6 2.7-4.6 2.7v-5.4Z"
          fill="currentColor"
        />
      </svg>
    </span>
  );
}
