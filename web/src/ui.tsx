import type { ReactNode } from "react";
import { Inbox } from "lucide-react";
import { ApiError } from "./api";

export type Toast = { tone: "ok" | "warn" | "error"; text: string } | null;

export function errorText(error: unknown): string {
  if (error instanceof ApiError) return error.detail;
  if (error instanceof Error) return error.message;
  return String(error);
}

export function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="field">
      <span>{label}</span>
      {children}
    </label>
  );
}

export function Toggle({
  label,
  checked,
  onChange,
}: {
  label: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
}) {
  return (
    <label className="toggle-row">
      <span>{label}</span>
      <button
        type="button"
        className={`toggle ${checked ? "on" : ""}`}
        aria-pressed={checked}
        onClick={() => onChange(!checked)}
      >
        <i />
      </button>
    </label>
  );
}

export function Choice({
  active,
  title,
  note,
  onClick,
}: {
  active: boolean;
  title: string;
  note: string;
  onClick: () => void;
}) {
  return (
    <button className={`choice ${active ? "active" : ""}`} onClick={onClick}>
      <span className="radio" />
      <span>
        <strong>{title}</strong>
        <small>{note}</small>
      </span>
    </button>
  );
}

export function Panel({
  title,
  description,
  children,
}: {
  title: string;
  description?: string;
  children: ReactNode;
}) {
  return (
    <section className="panel">
      <div className="panel-heading">
        <h2>{title}</h2>
        {description ? <p>{description}</p> : null}
      </div>
      {children}
    </section>
  );
}

export function Metric({
  label,
  value,
  suffix,
  tone,
}: {
  label: string;
  value: string;
  suffix?: string;
  tone?: "good" | "warning" | "danger";
}) {
  return (
    <div className={`metric ${tone || ""}`}>
      <span>{label}</span>
      <strong>{value}</strong>
      {suffix ? <small>{suffix}</small> : null}
    </div>
  );
}

export function InlineState({
  tone,
  children,
}: {
  tone: "ok" | "warn" | "error";
  children: ReactNode;
}) {
  return <div className={`inline-state ${tone}`}>{children}</div>;
}

export function EmptyState({ title, body }: { title: string; body: string }) {
  return (
    <div className="empty-state">
      <span><Inbox size={30} /></span>
      <strong>{title}</strong>
      <p>{body}</p>
    </div>
  );
}

export function compactNumber(value: number): string {
  if (!Number.isFinite(value)) return "0";
  if (value >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(1)}B`;
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
  return String(Math.round(value));
}
