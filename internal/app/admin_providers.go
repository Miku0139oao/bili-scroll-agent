package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Miku0139oao/bili-scroll-agent/internal/model"
	"github.com/Miku0139oao/bili-scroll-agent/internal/provider"
	"github.com/Miku0139oao/bili-scroll-agent/internal/store"
)

// adminProviders lists every configured provider.
//
// The response carries both the generic `providers` array and, when DeepSeek is
// configured, the pre-0.3 `deepseek` object. The SPA is served with a one-hour
// cache, so an older bundle can still be running against a new server.
func (a *App) adminProviders(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	if err := a.ensureDeepSeekProvider(r.Context()); err != nil {
		a.Logger.Warn("could not seed the deepseek provider", "error", err)
	}
	records, err := a.listProviders(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list providers")
		return
	}
	balances, _ := a.savedBalanceRows(r.Context())
	latest := map[string]map[string]any{}
	for _, balance := range balances {
		latest[stringFromAny(balance["provider"])] = balance
	}
	items := make([]map[string]any, 0, len(records))
	var legacy map[string]any
	for _, record := range records {
		item := a.providerPayload(record, latest[record.ProviderID])
		items = append(items, item)
		if record.ProviderID == provider.KindDeepSeek {
			legacy = legacyProviderEnvelope(item)
		}
	}
	response := map[string]any{"ok": true, "providers": items}
	if legacy != nil {
		response[provider.KindDeepSeek] = legacy
	}
	writeJSON(w, http.StatusOK, response)
}

// legacyProviderEnvelope mirrors the DeepSeek-only response shape from 0.2.
func legacyProviderEnvelope(item map[string]any) map[string]any {
	return map[string]any{
		"provider_id":           item["provider_id"],
		"base_url":              item["base_url"],
		"has_api_key":           item["has_api_key"],
		"low_balance_threshold": item["low_balance_threshold"],
		"enabled":               item["enabled"],
		"updated_at":            item["updated_at"],
	}
}

// providerPayload renders one provider without the key or the script body.
func (a *App) providerPayload(record providerRecord, balance map[string]any) map[string]any {
	return map[string]any{
		"provider_id":           record.ProviderID,
		"display_name":          record.DisplayName,
		"kind":                  record.Kind,
		"base_url":              record.BaseURL,
		"auth_style":            record.AuthStyle,
		"currency":              record.Currency,
		"has_api_key":           record.HasAPIKey,
		"low_balance_threshold": record.LowBalanceThreshold,
		"enabled":               record.Enabled,
		"allow_hosts":           record.AllowHosts,
		"settings":              record.Settings,
		"script_version":        record.ScriptVersion,
		"supports_balance":      a.Providers.SupportsBalance(record.Kind),
		"created_at":            record.CreatedAt,
		"updated_at":            record.UpdatedAt,
		"last_balance":          balance,
	}
}

func (a *App) adminProviderKinds(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "kinds": a.Providers.Kinds()})
}

func (a *App) adminGetProvider(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	record, err := a.loadProvider(r.Context(), r.PathValue("providerID"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	item := a.providerPayload(record, nil)
	// The editor needs the script; it is code, so it is returned as-is while the
	// key remains write-only.
	item["script"] = record.Script
	item["script_hash"] = scriptDigest(record.Script)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "provider": item})
}

func (a *App) adminCreateProvider(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var body providerBody
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid provider")
		return
	}
	if !validProviderID(body.ProviderID) {
		writeError(w, http.StatusUnprocessableEntity, "invalid provider id")
		return
	}
	var count int
	_ = a.Store.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM provider_accounts WHERE provider_id=?`, body.ProviderID).Scan(&count)
	if count > 0 {
		writeError(w, http.StatusConflict, "provider already exists")
		return
	}
	if err := a.saveProvider(r.Context(), body.ProviderID, body, true); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	a.auditDetail(r.Context(), "admin.provider_create", identity.User.ID, identity.User,
		map[string]any{"provider": body.ProviderID, "kind": body.Kind}, clientIP(r))
	a.adminProviders(w, r)
}

func (a *App) adminUpdateProvider(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	providerID := r.PathValue("providerID")
	if !validProviderID(providerID) {
		writeError(w, http.StatusUnprocessableEntity, "invalid provider id")
		return
	}
	var body providerBody
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid provider")
		return
	}
	if err := a.saveProvider(r.Context(), providerID, body, false); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	detail := map[string]any{
		"provider":    providerID,
		"kind":        body.Kind,
		"has_api_key": body.APIKey != nil && strings.TrimSpace(*body.APIKey) != "",
		"allow_hosts": body.AllowHosts,
	}
	if body.Script != "" {
		detail["script_hash"] = scriptDigest(body.Script)
	}
	a.auditDetail(r.Context(), "admin.provider_update", identity.User.ID, identity.User,
		detail, clientIP(r))
	a.adminProviders(w, r)
}

func (a *App) adminDeleteProvider(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	providerID := r.PathValue("providerID")
	result, err := a.Store.DB.ExecContext(r.Context(),
		`DELETE FROM provider_accounts WHERE provider_id=?`, providerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete provider")
		return
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		writeError(w, http.StatusNotFound, "provider not found")
		return
	}
	_, _ = a.Store.DB.ExecContext(r.Context(),
		`DELETE FROM provider_script_versions WHERE provider_id=?`, providerID)
	a.auditDetail(r.Context(), "admin.provider_delete", identity.User.ID, identity.User,
		map[string]any{"provider": providerID}, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// providerBody is the accepted shape for create and update. On update an absent
// or empty optional field keeps the stored value; api_key is write-only.
type providerBody struct {
	ProviderID          string         `json:"provider_id"`
	DisplayName         string         `json:"display_name"`
	Kind                string         `json:"kind"`
	BaseURL             string         `json:"base_url"`
	APIKey              *string        `json:"api_key"`
	ClearAPIKey         bool           `json:"clear_api_key"`
	LowBalanceThreshold *float64       `json:"low_balance_threshold"`
	Currency            string         `json:"currency"`
	Enabled             *bool          `json:"enabled"`
	AllowHosts          []string       `json:"allow_hosts"`
	Settings            map[string]any `json:"settings"`
	Script              string         `json:"script"`
}

// saveProvider validates and persists a provider account.
func (a *App) saveProvider(ctx context.Context, providerID string, body providerBody, create bool) error {
	record, err := a.loadProvider(ctx, providerID)
	if errors.Is(err, errProviderNotFound) {
		// Both create and update can initialise a missing row; the defaults
		// below fill in anything the request leaves out.
		record = providerRecord{ProviderID: providerID, Kind: provider.KindDeepSeek}
	} else if err != nil {
		return err
	}

	kind := strings.TrimSpace(body.Kind)
	if kind == "" {
		kind = firstNonEmptyText(record.Kind, provider.KindDeepSeek)
	}
	if !a.Providers.HasKind(kind) {
		return fmt.Errorf("unknown provider kind %q", kind)
	}
	baseURL := strings.TrimRight(strings.TrimSpace(body.BaseURL), "/")
	if baseURL == "" {
		baseURL = record.BaseURL
	}
	needsBaseURL := kind != provider.KindJS && kind != provider.KindNone
	if needsBaseURL {
		if baseURL == "" {
			return errors.New("base URL is required")
		}
		if _, err := provider.ValidateBaseURL(baseURL); err != nil {
			return err
		}
	} else if baseURL != "" {
		if _, err := provider.ValidateBaseURL(baseURL); err != nil {
			return err
		}
	}

	script := body.Script
	if script == "" && !create && kind == provider.KindJS {
		script = record.Script
	}
	script = strings.TrimSpace(script)
	if kind == provider.KindJS {
		if script == "" {
			return errors.New("a javascript adapter requires a script")
		}
		if _, err := a.Providers.ValidateScript(script); err != nil {
			return err
		}
	} else if script != "" {
		return errors.New("only a javascript adapter may carry a script")
	}

	if len(body.AllowHosts) > 8 {
		return errors.New("at most 8 allow_hosts entries are supported")
	}
	allowHosts := body.AllowHosts
	if len(allowHosts) == 0 && !create {
		allowHosts = record.AllowHosts
	}
	allowHosts = normalizeHosts(allowHosts)

	threshold := record.LowBalanceThreshold
	if body.LowBalanceThreshold != nil {
		if *body.LowBalanceThreshold < 0 {
			return errors.New("low balance threshold must not be negative")
		}
		threshold = *body.LowBalanceThreshold
	} else if create && threshold == 0 {
		threshold = 10
	}

	enabled := record.Enabled
	if body.Enabled != nil {
		enabled = *body.Enabled
	} else if create {
		enabled = true
	}

	displayName := strings.TrimSpace(body.DisplayName)
	if displayName == "" {
		displayName = firstNonEmptyText(record.DisplayName, kind)
	}
	currency := strings.TrimSpace(body.Currency)
	if currency == "" {
		currency = record.Currency
	}
	if len(currency) > 8 {
		return errors.New("currency must be at most 8 characters")
	}
	settings := body.Settings
	if settings == nil && !create {
		settings = record.Settings
	}
	if len(store.JSON(settings)) > 16<<10 {
		return errors.New("provider settings exceed 16 KiB")
	}
	authStyle := firstNonEmptyText(record.AuthStyle, "bearer")

	// Preserve the existing ciphertext unless a new key is supplied or the
	// caller explicitly clears it.
	var ciphertext sql.NullString
	if record.HasAPIKey {
		_ = a.Store.DB.QueryRowContext(ctx,
			`SELECT api_key_enc FROM provider_accounts WHERE provider_id=?`, providerID).Scan(&ciphertext)
	}
	if body.ClearAPIKey {
		ciphertext = sql.NullString{}
	} else if body.APIKey != nil && strings.TrimSpace(*body.APIKey) != "" {
		value, err := a.Cipher.EncryptBytes([]byte(strings.TrimSpace(*body.APIKey)), "provider:"+providerID)
		if err != nil {
			return errors.New("could not protect the provider key")
		}
		ciphertext = sql.NullString{String: value, Valid: true}
	}

	version := record.ScriptVersion
	if kind == provider.KindJS && script != record.Script {
		version++
	}
	now := store.Now()
	createdAt := firstNonEmptyText(record.CreatedAt, now)
	_, err = a.Store.DB.ExecContext(ctx, `
		INSERT INTO provider_accounts(provider_id,kind,display_name,base_url,auth_style,
			currency,api_key_enc,low_balance_threshold,enabled,allow_hosts,settings,
			script,script_version,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(provider_id) DO UPDATE SET
			kind=excluded.kind,display_name=excluded.display_name,base_url=excluded.base_url,
			auth_style=excluded.auth_style,currency=excluded.currency,
			api_key_enc=excluded.api_key_enc,low_balance_threshold=excluded.low_balance_threshold,
			enabled=excluded.enabled,allow_hosts=excluded.allow_hosts,settings=excluded.settings,
			script=excluded.script,script_version=excluded.script_version,
			updated_at=excluded.updated_at`,
		providerID, kind, displayName, baseURL, authStyle, currency,
		nullIfEmpty(ciphertext.String), threshold, boolInt(enabled),
		store.JSON(allowHosts), store.JSON(settings), script, version, createdAt, now)
	if err != nil {
		return errors.New("could not save the provider")
	}
	if kind == provider.KindJS && script != "" && version != record.ScriptVersion {
		_, _ = a.Store.DB.ExecContext(ctx, `
			INSERT OR IGNORE INTO provider_script_versions(id,provider_id,version,script,created_at)
			VALUES(?,?,?,?,?)`, model.NewID(), providerID, version, script, now)
	}
	return nil
}

// adminValidateProviderScript compiles an adapter without any network access.
func (a *App) adminValidateProviderScript(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	var body struct {
		Script string `json:"script"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid script payload")
		return
	}
	check, err := a.Providers.ValidateScript(body.Script)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "compiled": check.Compiled, "functions": check.Functions,
		"script_hash": check.Hash, "elapsed_ms": check.ElapsedMS,
	})
}

func (a *App) adminCheckBalance(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	providerID := r.PathValue("providerID")
	result, err := a.refreshProviderBalance(r.Context(), identity.User.ID, providerID)
	if err != nil {
		if errors.Is(err, provider.ErrNoBalanceAPI) {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	result["ok"] = true
	if result["supports_balance"] == true {
		a.auditDetail(r.Context(), "admin.provider_check", identity.User.ID, identity.User,
			map[string]any{"provider": providerID}, clientIP(r))
	}
	writeJSON(w, http.StatusOK, result)
}

func normalizeHosts(hosts []string) []string {
	result := make([]string, 0, len(hosts))
	seen := map[string]bool{}
	for _, host := range hosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		result = append(result, host)
	}
	return result
}

func scriptDigest(script string) string {
	trimmed := strings.TrimSpace(script)
	if trimmed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(trimmed))
	return hex.EncodeToString(sum[:])
}
