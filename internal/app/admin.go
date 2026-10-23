package app

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Miku0139oao/bili-scroll-agent/internal/config"
	"github.com/Miku0139oao/bili-scroll-agent/internal/model"
	"github.com/Miku0139oao/bili-scroll-agent/internal/runner"
	"github.com/Miku0139oao/bili-scroll-agent/internal/security"
	"github.com/Miku0139oao/bili-scroll-agent/internal/store"
)

func (a *App) adminUsers(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	_ = identity
	rows, err := a.Store.DB.QueryContext(r.Context(), `
		SELECT id,username,display_name,role,disabled,totp_confirmed,created_at,
			COALESCE(last_login_at,'')
		FROM users ORDER BY created_at`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load users")
		return
	}
	defer rows.Close()
	result := make([]map[string]any, 0)
	for rows.Next() {
		var id, username, displayName, role, created, lastLogin string
		var disabled, confirmed int
		if rows.Scan(&id, &username, &displayName, &role, &disabled, &confirmed,
			&created, &lastLogin) != nil {
			continue
		}
		config, _ := a.loadConfig(r.Context(), id)
		var credentialCount int
		_ = a.Store.DB.QueryRowContext(r.Context(),
			`SELECT COUNT(*) FROM bili_credentials WHERE user_id=?`, id).Scan(&credentialCount)
		var enabled int
		var clock, timezone string
		_ = a.Store.DB.QueryRowContext(r.Context(),
			`SELECT enabled,local_time,timezone FROM schedules WHERE user_id=?`, id).
			Scan(&enabled, &clock, &timezone)
		result = append(result, map[string]any{
			"id": id, "username": username, "display_name": displayName, "role": role,
			"disabled": disabled != 0, "totp_confirmed": confirmed != 0,
			"created_at": created, "last_login_at": nullIfEmpty(lastLogin),
			"runner": config.Runner, "bilibili_logged_in": credentialCount > 0,
			"schedule": map[string]any{"enabled": enabled != 0, "time": clock, "timezone": timezone},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "users": result})
}

func (a *App) adminCreateInvite(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var body struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid invite")
		return
	}
	body.Username = strings.ToLower(strings.TrimSpace(body.Username))
	body.DisplayName = strings.TrimSpace(body.DisplayName)
	if body.DisplayName == "" {
		body.DisplayName = body.Username
	}
	if body.Role != "admin" && body.Role != "member" {
		writeError(w, http.StatusUnprocessableEntity, "role must be admin or member")
		return
	}
	if len(body.Username) < 2 || !validUsername(body.Username) {
		writeError(w, http.StatusUnprocessableEntity, "invalid username")
		return
	}
	if _, err := a.userByUsername(r.Context(), body.Username); err == nil {
		writeError(w, http.StatusConflict, "username already exists")
		return
	}
	raw, _ := security.NewSecret("bili_invite", 32)
	now := time.Now().UTC()
	inviteID := model.NewID()
	_, err := a.Store.DB.ExecContext(r.Context(), `
		INSERT INTO invites(id,token_hash,username,display_name,role,created_by,created_at,expires_at)
		VALUES(?,?,?,?,?,?,?,?)`,
		inviteID, a.Cipher.TokenHash(raw), body.Username, body.DisplayName, body.Role,
		identity.User.ID, now.Format(time.RFC3339Nano),
		now.Add(a.Settings.InviteDuration).Format(time.RFC3339Nano))
	if err != nil {
		writeError(w, http.StatusConflict, "could not create invite")
		return
	}
	a.auditDetail(r.Context(), "admin.invite_create", inviteID, identity.User,
		map[string]any{"role": body.Role}, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "id": inviteID, "username": body.Username,
		"expires_at": now.Add(a.Settings.InviteDuration),
		"invite_url": strings.TrimRight(a.Settings.PublicURL, "/") + "/invite/" + raw,
	})
}

func (a *App) adminInvites(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	rows, err := a.Store.DB.QueryContext(r.Context(), `
		SELECT id,username,display_name,role,expires_at,used_at
		FROM invites ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load invites")
		return
	}
	defer rows.Close()
	result := make([]map[string]any, 0)
	for rows.Next() {
		var id, username, displayName, role, expires string
		var used sql.NullString
		if rows.Scan(&id, &username, &displayName, &role, &expires, &used) == nil {
			result = append(result, map[string]any{
				"id": id, "username": username, "display_name": displayName, "role": role,
				"expires_at": expires, "used_at": nullTime(used),
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "invites": result})
}

func (a *App) adminPatchUser(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	userID := r.PathValue("userID")
	user, err := a.userByID(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	var body struct {
		Disabled *bool   `json:"disabled"`
		Role     *string `json:"role"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid user patch")
		return
	}
	if userID == identity.User.ID &&
		((body.Disabled != nil && *body.Disabled) || (body.Role != nil && *body.Role == "member")) {
		writeError(w, http.StatusConflict, "cannot disable or demote yourself")
		return
	}
	if user.Role == "admin" &&
		((body.Disabled != nil && *body.Disabled) || (body.Role != nil && *body.Role == "member")) {
		var admins int
		_ = a.Store.DB.QueryRowContext(r.Context(),
			`SELECT COUNT(*) FROM users WHERE role='admin' AND disabled=0`).Scan(&admins)
		if admins <= 1 {
			writeError(w, http.StatusConflict, "cannot remove the last admin")
			return
		}
	}
	if body.Disabled != nil {
		_, _ = a.Store.DB.ExecContext(r.Context(), `UPDATE users SET disabled=? WHERE id=?`,
			boolInt(*body.Disabled), userID)
		if *body.Disabled {
			_, _ = a.Store.DB.ExecContext(r.Context(), `
				UPDATE auth_sessions SET revoked_at=? WHERE user_id=? AND revoked_at IS NULL`,
				store.Now(), userID)
		}
	}
	if body.Role != nil {
		if *body.Role != "admin" && *body.Role != "member" {
			writeError(w, http.StatusUnprocessableEntity, "invalid role")
			return
		}
		_, _ = a.Store.DB.ExecContext(r.Context(), `UPDATE users SET role=? WHERE id=?`, *body.Role, userID)
	}
	a.auditDetail(r.Context(), "admin.user_update", userID, identity.User,
		map[string]any{"disabled": body.Disabled, "role": body.Role}, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) adminResetTOTP(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	user, err := a.userByID(r.Context(), r.PathValue("userID"))
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	secret, _ := security.NewTOTPSecret()
	encrypted, err := a.Cipher.EncryptBytes([]byte(secret), "totp:"+user.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not protect TOTP")
		return
	}
	now := store.Now()
	_, err = a.Store.DB.ExecContext(r.Context(), `
		UPDATE users SET totp_secret_enc=?,totp_confirmed=1 WHERE id=?`,
		encrypted, user.ID)
	if err == nil {
		_, _ = a.Store.DB.ExecContext(r.Context(), `
			UPDATE auth_sessions SET revoked_at=? WHERE user_id=? AND revoked_at IS NULL`, now, user.ID)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not reset TOTP")
		return
	}
	uri := security.TOTPURI(secret, user.Username, "Bili Scroll Agent")
	a.auditDetail(r.Context(), "admin.totp_reset", user.ID, identity.User, map[string]any{}, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "username": user.Username, "secret": secret,
		"otpauth_uri": uri, "otpauth_qr": qrDataURI(uri),
	})
}

func (a *App) adminGetSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "settings": a.loadAdminSettings(r.Context()),
		"runners": runnerStatus(a.Settings),
	})
}

func (a *App) adminPutSettings(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var patch map[string]any
	if err := decodeJSON(r, &patch); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid settings")
		return
	}
	settings, err := a.saveAdminSettings(r.Context(), patch)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	a.auditDetail(r.Context(), "admin.settings_update", identity.User.ID, identity.User, patch, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "settings": settings})
}

func (a *App) adminAudit(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	rows, err := a.Store.DB.QueryContext(r.Context(), `
		SELECT id,COALESCE(user_id,''),COALESCE(actor_user_id,''),event,COALESCE(target,''),
			detail,COALESCE(ip,''),created_at
		FROM audit_events ORDER BY created_at DESC LIMIT 500`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load audit events")
		return
	}
	defer rows.Close()
	result := make([]map[string]any, 0)
	for rows.Next() {
		var id, userID, actorID, event, target, detail, ip, created string
		if rows.Scan(&id, &userID, &actorID, &event, &target, &detail, &ip, &created) == nil {
			result = append(result, map[string]any{
				"id": id, "user_id": userID, "actor_user_id": actorID, "event": event,
				"target": target, "detail": store.DecodeJSON[map[string]any](detail),
				"ip": ip, "created_at": created,
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "events": result})
}

func (a *App) adminHealth(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	info, _ := os.Stat(a.Settings.DatabasePath)
	var users, runs, reports int
	_ = a.Store.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users`).Scan(&users)
	_ = a.Store.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM runs`).Scan(&runs)
	_ = a.Store.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM reports`).Scan(&reports)
	var size int64
	if info != nil {
		size = info.Size()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "version": "0.2.0-go",
		"database":  map[string]any{"path": a.Settings.DatabasePath, "size_bytes": size},
		"scheduler": map[string]any{"running": true},
		"runners":   runner.Status(a.Settings),
		"counts":    map[string]any{"users": users, "runs": runs, "reports": reports},
	})
}

func (a *App) adminBackup(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	targetDir := filepath.Join(a.Settings.DataDir, "backups")
	_ = os.MkdirAll(targetDir, 0o700)
	target := filepath.Join(targetDir, "bili-scroll-"+time.Now().UTC().Format("20060102-150405")+".db")
	escaped, _ := json.Marshal(target)
	_, err := a.Store.DB.ExecContext(r.Context(), "VACUUM INTO "+string(escaped))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "backup failed: "+err.Error())
		return
	}
	a.auditDetail(r.Context(), "admin.backup", identity.User.ID, identity.User,
		map[string]any{"path": target}, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": target})
}

func (a *App) adminProviders(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	result := map[string]any{
		"provider_id": "deepseek", "base_url": "https://api.deepseek.com",
		"has_api_key": os.Getenv("DEEPSEEK_API_KEY") != "", "low_balance_threshold": 10,
		"enabled": true, "updated_at": nil,
	}
	var baseURL string
	var encrypted sql.NullString
	var threshold float64
	var enabled int
	var updated string
	err := a.Store.DB.QueryRowContext(r.Context(), `
		SELECT base_url,api_key_enc,low_balance_threshold,enabled,updated_at
		FROM provider_accounts WHERE provider_id='deepseek'`).
		Scan(&baseURL, &encrypted, &threshold, &enabled, &updated)
	if err == nil {
		result["base_url"] = baseURL
		result["has_api_key"] = encrypted.Valid
		result["low_balance_threshold"] = threshold
		result["enabled"] = enabled != 0
		result["updated_at"] = updated
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deepseek": result})
}

func (a *App) adminUpdateProvider(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var body struct {
		BaseURL             string  `json:"base_url"`
		APIKey              *string `json:"api_key"`
		LowBalanceThreshold float64 `json:"low_balance_threshold"`
		Enabled             bool    `json:"enabled"`
	}
	if err := decodeJSON(r, &body); err != nil || body.BaseURL == "" {
		writeError(w, http.StatusUnprocessableEntity, "invalid provider")
		return
	}
	encrypted := ""
	var existing sql.NullString
	_ = a.Store.DB.QueryRowContext(r.Context(),
		`SELECT api_key_enc FROM provider_accounts WHERE provider_id='deepseek'`).Scan(&existing)
	if existing.Valid {
		encrypted = existing.String
	}
	if body.APIKey != nil && strings.TrimSpace(*body.APIKey) != "" {
		var err error
		encrypted, err = a.Cipher.EncryptBytes([]byte(strings.TrimSpace(*body.APIKey)), "provider:deepseek")
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not protect provider key")
			return
		}
	}
	_, err := a.Store.DB.ExecContext(r.Context(), `
		INSERT INTO provider_accounts(provider_id,base_url,api_key_enc,low_balance_threshold,enabled,updated_at)
		VALUES('deepseek',?,?,?,?,?)
		ON CONFLICT(provider_id) DO UPDATE SET base_url=excluded.base_url,
			api_key_enc=excluded.api_key_enc,low_balance_threshold=excluded.low_balance_threshold,
			enabled=excluded.enabled,updated_at=excluded.updated_at`,
		strings.TrimRight(body.BaseURL, "/"), nullIfEmpty(encrypted),
		body.LowBalanceThreshold, boolInt(body.Enabled), store.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not save provider")
		return
	}
	a.auditDetail(r.Context(), "admin.provider_update", identity.User.ID, identity.User,
		map[string]any{"provider": "deepseek", "has_api_key": encrypted != ""}, clientIP(r))
	a.adminProviders(w, r)
}

func (a *App) adminCheckBalance(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	result, err := a.refreshDeepSeekBalance(r.Context(), identity.User.ID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	result["ok"] = true
	writeJSON(w, http.StatusOK, result)
}

func validUsername(value string) bool {
	if len(value) < 2 || len(value) > 64 {
		return false
	}
	for index, char := range value {
		if index == 0 && !((char >= 'a' && char <= 'z') || (char >= '0' && char <= '9')) {
			return false
		}
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') ||
			char == '.' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func runnerStatus(settings *config.Settings) map[string]runner.Info {
	return runner.Status(settings)
}

var _ = base64.RawURLEncoding
