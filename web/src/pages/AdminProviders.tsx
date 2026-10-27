import { useCallback, useEffect, useState } from "react";
import { api } from "../api";
import { Field, InlineState, Panel, Toggle, errorText, type Toast } from "../ui";
import type { ProviderAccount, ProviderKindInfo, ProviderListPayload } from "../types";

type Editor = {
  provider_id: string;
  display_name: string;
  kind: string;
  base_url: string;
  currency: string;
  low_balance_threshold: number;
  enabled: boolean;
  allow_hosts: string;
  script: string;
  api_key: string;
  clear_api_key: boolean;
  isNew: boolean;
};

const EMPTY_EDITOR: Editor = {
  provider_id: "",
  display_name: "",
  kind: "deepseek",
  base_url: "",
  currency: "",
  low_balance_threshold: 10,
  enabled: true,
  allow_hosts: "",
  script: "",
  api_key: "",
  clear_api_key: false,
  isNew: true,
};

function editorFor(provider: ProviderAccount): Editor {
  return {
    provider_id: provider.provider_id,
    display_name: provider.display_name,
    kind: provider.kind,
    base_url: provider.base_url,
    currency: provider.currency,
    low_balance_threshold: provider.low_balance_threshold,
    enabled: provider.enabled,
    allow_hosts: (provider.allow_hosts || []).join(", "),
    script: provider.script || "",
    api_key: "",
    clear_api_key: false,
    isNew: false,
  };
}

export function AdminProviders({ setToast }: { setToast: (toast: Toast) => void }) {
  const [providers, setProviders] = useState<ProviderAccount[]>([]);
  const [kinds, setKinds] = useState<ProviderKindInfo[]>([]);
  const [editor, setEditor] = useState<Editor | null>(null);
  const [validation, setValidation] = useState<{ ok: boolean; text: string } | null>(null);
  const [checkResult, setCheckResult] = useState<Record<string, unknown> | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      const [list, kindList] = await Promise.all([
        api<ProviderListPayload>("/api/admin/providers"),
        api<{ kinds: ProviderKindInfo[] }>("/api/admin/provider-kinds"),
      ]);
      // A server older than 0.3 only returns the legacy `deepseek` envelope.
      const items = list.providers ?? (list.deepseek ? [list.deepseek] : []);
      setProviders(items);
      setKinds(kindList.kinds || []);
    } catch (reason) {
      setToast({ tone: "error", text: errorText(reason) });
    }
  }, [setToast]);

  useEffect(() => {
    void load();
  }, [load]);

  const selectedKind = kinds.find((item) => item.kind === editor?.kind);

  const edit = async (provider: ProviderAccount) => {
    try {
      const detail = await api<{ provider: ProviderAccount }>(
        `/api/admin/providers/${encodeURIComponent(provider.provider_id)}`,
      );
      setEditor(editorFor(detail.provider));
      setValidation(null);
      setCheckResult(null);
    } catch (reason) {
      setToast({ tone: "error", text: errorText(reason) });
    }
  };

  const validateScript = async () => {
    if (!editor) return;
    try {
      const result = await api<{ compiled: boolean; functions: string[]; elapsed_ms: number }>(
        `/api/admin/providers/${encodeURIComponent(editor.provider_id || "new")}/validate`,
        { method: "POST", body: JSON.stringify({ script: editor.script }) },
      );
      setValidation({
        ok: true,
        text: `編譯成功（${result.functions.join(", ")}，${result.elapsed_ms}ms）`,
      });
    } catch (reason) {
      setValidation({ ok: false, text: errorText(reason) });
    }
  };

  const save = async () => {
    if (!editor) return;
    setBusy(true);
    try {
      const payload = {
        display_name: editor.display_name,
        kind: editor.kind,
        base_url: editor.base_url,
        currency: editor.currency,
        low_balance_threshold: Number(editor.low_balance_threshold),
        enabled: editor.enabled,
        allow_hosts: editor.allow_hosts
          .split(",")
          .map((item) => item.trim())
          .filter(Boolean),
        script: editor.kind === "js" ? editor.script : "",
        clear_api_key: editor.clear_api_key,
        // Never send an empty key: the server treats absence as "keep".
        api_key: editor.api_key ? editor.api_key : null,
      };
      if (editor.isNew) {
        await api("/api/admin/providers", {
          method: "POST",
          body: JSON.stringify({ ...payload, provider_id: editor.provider_id }),
        });
      } else {
        await api(`/api/admin/providers/${encodeURIComponent(editor.provider_id)}`, {
          method: "PUT",
          body: JSON.stringify(payload),
        });
      }
      setToast({ tone: "ok", text: "Provider 已儲存" });
      setEditor(null);
      await load();
    } catch (reason) {
      setToast({ tone: "error", text: errorText(reason) });
    } finally {
      setBusy(false);
    }
  };

  const check = async (providerId: string) => {
    try {
      const result = await api<Record<string, unknown>>(
        `/api/admin/providers/${encodeURIComponent(providerId)}/check`,
        { method: "POST" },
      );
      setCheckResult(result);
      setToast({ tone: "ok", text: `${providerId} 餘額查詢成功` });
      await load();
    } catch (reason) {
      setCheckResult({ error: errorText(reason) });
      setToast({ tone: "error", text: errorText(reason) });
    }
  };

  const remove = async (providerId: string) => {
    if (!window.confirm(`確定刪除 provider ${providerId}？`)) return;
    try {
      await api(`/api/admin/providers/${encodeURIComponent(providerId)}`, { method: "DELETE" });
      setToast({ tone: "warn", text: `${providerId} 已刪除` });
      await load();
    } catch (reason) {
      setToast({ tone: "error", text: errorText(reason) });
    }
  };

  return (
    <Panel
      title="Providers 與餘額"
      description="每個 provider 對應一種餘額查詢轉接器（adapter）；API key 只寫不讀。"
    >
      <div className="provider-table">
        <table>
          <thead>
            <tr>
              <th>Provider</th>
              <th>Kind</th>
              <th>餘額</th>
              <th>狀態</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {providers.map((provider) => {
              const balance = provider.last_balance;
              return (
                <tr key={provider.provider_id}>
                  <td>
                    <strong>{provider.display_name || provider.provider_id}</strong>
                    <small>{provider.provider_id}</small>
                  </td>
                  <td><code className="kind-badge">{provider.kind}</code></td>
                  <td>
                    {balance
                      ? `${balance.total} ${balance.currency}${balance.low ? "（偏低）" : ""}`
                      : provider.supports_balance
                        ? "—"
                        : "無餘額 API"}
                  </td>
                  <td>{provider.enabled ? "啟用" : "停用"}</td>
                  <td className="row-actions">
                    <button className="button" onClick={() => void edit(provider)}>編輯</button>
                    <button
                      className="button"
                      disabled={!provider.supports_balance}
                      onClick={() => void check(provider.provider_id)}
                    >
                      測試餘額
                    </button>
                    <button className="button danger" onClick={() => void remove(provider.provider_id)}>
                      刪除
                    </button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      {checkResult ? (
        <InlineState tone={checkResult.error ? "error" : "ok"}>
          <pre className="adapter-output">{JSON.stringify(checkResult, null, 2)}</pre>
        </InlineState>
      ) : null}

      <div className="button-row">
        <button
          className="button primary"
          onClick={() => {
            setEditor({ ...EMPTY_EDITOR });
            setValidation(null);
            setCheckResult(null);
          }}
        >
          新增 Provider
        </button>
      </div>

      {editor ? (
        <div className="provider-editor">
          <div className="form-grid two">
            <Field label="Provider ID">
              <input
                value={editor.provider_id}
                disabled={!editor.isNew}
                placeholder="openrouter"
                onChange={(event) => setEditor({ ...editor, provider_id: event.target.value })}
              />
            </Field>
            <Field label="顯示名稱">
              <input
                value={editor.display_name}
                onChange={(event) => setEditor({ ...editor, display_name: event.target.value })}
              />
            </Field>
            <Field label="Adapter Kind">
              <select
                value={editor.kind}
                onChange={(event) => setEditor({ ...editor, kind: event.target.value })}
              >
                {kinds.map((kind) => (
                  <option key={kind.kind} value={kind.kind}>
                    {kind.label}
                  </option>
                ))}
              </select>
            </Field>
            <Field label="Base URL">
              <input
                value={editor.base_url}
                placeholder={selectedKind?.requires_base_url ? "https://api.example.com" : "（可留空）"}
                onChange={(event) => setEditor({ ...editor, base_url: event.target.value })}
              />
            </Field>
            <Field label={editor.isNew || !providerHasKey(providers, editor.provider_id) ? "API Key" : "API Key（未設定＝保留）"}>
              <input
                type="password"
                value={editor.api_key}
                placeholder="sk-…"
                onChange={(event) => setEditor({ ...editor, api_key: event.target.value })}
              />
            </Field>
            <Field label="低餘額門檻">
              <input
                type="number"
                min={0}
                value={String(editor.low_balance_threshold)}
                onChange={(event) =>
                  setEditor({ ...editor, low_balance_threshold: Number(event.target.value) })
                }
              />
            </Field>
            <Field label="幣別">
              <input
                value={editor.currency}
                placeholder="CNY / USD"
                onChange={(event) => setEditor({ ...editor, currency: event.target.value })}
              />
            </Field>
            <Field label="Allow hosts（逗號分隔）">
              <input
                value={editor.allow_hosts}
                placeholder="api.example.com"
                onChange={(event) => setEditor({ ...editor, allow_hosts: event.target.value })}
              />
            </Field>
          </div>
          <Toggle
            label="啟用"
            checked={editor.enabled}
            onChange={(checked) => setEditor({ ...editor, enabled: checked })}
          />
          {selectedKind?.requires_script ? (
            <div className="stack">
              <Field label="JavaScript adapter">
                <textarea
                  className="code-editor"
                  spellCheck={false}
                  value={editor.script}
                  onChange={(event) => {
                    setEditor({ ...editor, script: event.target.value });
                    setValidation(null);
                  }}
                  placeholder={"function fetchBalance(ctx) {\n  const res = ctx.http.get(ctx.baseUrl + \"/balance\", {\n    headers: { Authorization: \"Bearer \" + ctx.apiKey }\n  });\n  return [{ currency: \"USD\", total: res.json.total }];\n}"}
                />
              </Field>
              <div className="button-row">
                <button className="button" onClick={() => void validateScript()}>驗證腳本</button>
              </div>
              {validation ? (
                <InlineState tone={validation.ok ? "ok" : "error"}>{validation.text}</InlineState>
              ) : null}
              <InlineState tone="warn">
                腳本由管理員撰寫，因此視為受信任程式碼。它會拿到 API key，但只能連線到
                Allow hosts 列出的主機，且沒有檔案、環境變數或行程存取權。
              </InlineState>
            </div>
          ) : null}
          <div className="button-row">
            <button className="button primary" disabled={busy} onClick={() => void save()}>
              {busy ? "儲存中…" : "儲存 Provider"}
            </button>
            <button className="button" onClick={() => setEditor(null)}>取消</button>
          </div>
        </div>
      ) : null}
    </Panel>
  );
}

function providerHasKey(providers: ProviderAccount[], id: string): boolean {
  return providers.some((item) => item.provider_id === id && item.has_api_key);
}

export default AdminProviders;
