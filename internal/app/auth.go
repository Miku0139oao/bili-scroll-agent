package app

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Miku0139oao/bili-scroll-agent/internal/model"
	"github.com/Miku0139oao/bili-scroll-agent/internal/security"
	"github.com/Miku0139oao/bili-scroll-agent/internal/store"
)

type authContext struct {
	User    model.User
	Session model.Session
}

type bearerContext struct {
	User  model.User
	Token model.APIIdentifier
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		TOTP     string `json:"totp"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid request")
		return
	}
	key := "login:" + clientIP(r) + ":" + strings.ToLower(body.Username)
	if !a.limiter.Allow(key, a.Settings.LoginRatePerMinute, time.Minute) {
		writeError(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	user, err := a.userByUsername(r.Context(), strings.ToLower(strings.TrimSpace(body.Username)))
	if err != nil || user.Disabled || !user.TOTPConfirmed ||
		!security.VerifyPassword(user.PasswordHash, body.Password) ||
		!a.verifyUserTOTP(user, body.TOTP) {
		a.audit(r.Context(), "auth.login_failed", "", user.ID, model.User{}, clientIP(r))
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	rawSession, rawCSRF, err := a.createSession(r.Context(), user, clientIP(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	a.setSessionCookie(w, rawSession)
	_, _ = a.Store.DB.ExecContext(r.Context(), `UPDATE users SET last_login_at=? WHERE id=?`, store.Now(), user.ID)
	a.audit(r.Context(), "auth.login", user.ID, user.ID, user, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "user": publicUserValue(user), "csrf": rawCSRF,
	})
}

func (a *App) me(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireWeb(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "user": publicUserValue(identity.User), "session_expires": identity.Session.ExpiresAt,
	})
}

func (a *App) rotateCSRF(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireWeb(w, r)
	if !ok {
		return
	}
	raw, err := security.NewSecret("bili_csrf", 24)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not rotate CSRF token")
		return
	}
	_, err = a.Store.DB.ExecContext(
		r.Context(),
		`UPDATE auth_sessions SET csrf_hash=? WHERE id_hash=?`,
		a.Cipher.TokenHash(raw),
		identity.Session.IDHash,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not rotate CSRF token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "csrf": raw})
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireCSRF(w, r)
	if !ok {
		return
	}
	_, _ = a.Store.DB.ExecContext(
		r.Context(),
		`UPDATE auth_sessions SET revoked_at=? WHERE id_hash=?`,
		store.Now(),
		identity.Session.IDHash,
	)
	http.SetCookie(w, &http.Cookie{
		Name:     "bili_session",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   a.Settings.SecureCookies,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) inviteInfo(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	var username, displayName, expiresAt string
	var usedAt sql.NullString
	err := a.Store.DB.QueryRowContext(r.Context(), `
		SELECT username,display_name,expires_at,used_at
		FROM invites WHERE token_hash=?`, a.Cipher.TokenHash(token)).
		Scan(&username, &displayName, &expiresAt, &usedAt)
	if err != nil || usedAt.Valid || inviteExpired(expiresAt) {
		writeError(w, http.StatusNotFound, "invite is invalid or expired")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "username": username, "display_name": displayName, "expires_at": expiresAt,
	})
}

func (a *App) inviteAccept(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid request")
		return
	}
	if len(body.Password) < 12 {
		writeError(w, http.StatusBadRequest, "password must contain at least 12 characters")
		return
	}
	var inviteID, username, displayName, role, expiresAt string
	var usedAt sql.NullString
	err := a.Store.DB.QueryRowContext(r.Context(), `
		SELECT id,username,display_name,role,expires_at,used_at
		FROM invites WHERE token_hash=?`, a.Cipher.TokenHash(body.Token)).
		Scan(&inviteID, &username, &displayName, &role, &expiresAt, &usedAt)
	if err != nil || usedAt.Valid || inviteExpired(expiresAt) {
		writeError(w, http.StatusBadRequest, "invite is invalid or expired")
		return
	}
	if _, err := a.userByUsername(r.Context(), username); err == nil {
		writeError(w, http.StatusBadRequest, "username already exists")
		return
	}
	passwordHash, err := security.HashPassword(body.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	secret, err := security.NewTOTPSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create TOTP secret")
		return
	}
	encryptedSecret, err := a.Cipher.EncryptBytes([]byte(secret), "totp:"+username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not protect TOTP secret")
		return
	}
	userID := model.NewID()
	setupToken, _ := security.NewSecret("bili_setup", 32)
	now := time.Now().UTC()
	err = a.Store.WithTx(r.Context(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`
			INSERT INTO users(id,username,display_name,role,password_hash,totp_secret_enc,
				totp_confirmed,disabled,created_at)
			VALUES(?,?,?,?,?,?,0,0,?)`,
			userID, username, displayName, role, passwordHash, encryptedSecret, now.Format(time.RFC3339Nano),
		); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO user_configs(user_id,payload,updated_at) VALUES(?,?,?)`,
			userID, store.JSON(model.DefaultMemberConfig()), now.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		if _, err := tx.Exec(`
			INSERT INTO schedules(user_id,enabled,local_time,timezone,updated_at)
			VALUES(?,0,'20:00','Asia/Shanghai',?)`, userID, now.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		if _, err := tx.Exec(`
			INSERT INTO api_tokens(id_hash,user_id,label,kind,scopes,created_at,expires_at)
			VALUES(?,?,?,?,?,?,?)`,
			a.Cipher.TokenHash(setupToken), userID, "invite-totp-setup", "setup",
			store.JSON([]string{"totp:confirm"}), now.Format(time.RFC3339Nano),
			now.Add(20*time.Minute).Format(time.RFC3339Nano)); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE invites SET used_at=? WHERE id=?`, now.Format(time.RFC3339Nano), inviteID)
		return err
	})
	if err != nil {
		writeError(w, http.StatusConflict, "invite could not be accepted")
		return
	}
	uri := security.TOTPURI(secret, username, "Bili Scroll Agent")
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "setup_token": setupToken, "secret": secret,
		"otpauth_uri": uri, "otpauth_qr": qrDataURI(uri),
	})
}

func (a *App) inviteVerify(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SetupToken string `json:"setup_token"`
		TOTP       string `json:"totp"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid request")
		return
	}
	var userID, username, secretEnc, expiresAt string
	var revokedAt sql.NullString
	err := a.Store.DB.QueryRowContext(r.Context(), `
		SELECT u.id,u.username,u.totp_secret_enc,t.expires_at,t.revoked_at
		FROM api_tokens t JOIN users u ON u.id=t.user_id
		WHERE t.id_hash=? AND t.kind='setup'`, a.Cipher.TokenHash(body.SetupToken)).
		Scan(&userID, &username, &secretEnc, &expiresAt, &revokedAt)
	if err != nil || revokedAt.Valid || inviteExpired(expiresAt) ||
		!a.verifyEncryptedTOTP(secretEnc, username, body.TOTP) {
		writeError(w, http.StatusBadRequest, "invalid TOTP code")
		return
	}
	now := store.Now()
	tx, err := a.Store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not complete setup")
		return
	}
	_, err = tx.Exec(`UPDATE users SET totp_confirmed=1 WHERE id=?`, userID)
	if err == nil {
		_, err = tx.Exec(`UPDATE api_tokens SET revoked_at=? WHERE id_hash=?`,
			now, a.Cipher.TokenHash(body.SetupToken))
	}
	if err == nil {
		err = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not complete setup")
		return
	}
	user, err := a.userByID(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "user not found")
		return
	}
	rawSession, rawCSRF, err := a.createSession(r.Context(), user, clientIP(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	a.setSessionCookie(w, rawSession)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "user": publicUserValue(user), "csrf": rawCSRF,
	})
}

func (a *App) stepUp(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireCSRF(w, r)
	if !ok {
		return
	}
	var body struct {
		Password string `json:"password"`
		TOTP     string `json:"totp"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid request")
		return
	}
	if !security.VerifyPassword(identity.User.PasswordHash, body.Password) ||
		!a.verifyUserTOTP(identity.User, body.TOTP) {
		writeError(w, http.StatusBadRequest, "invalid password or TOTP")
		return
	}
	raw, _ := security.NewSecret("bili_stepup", 32)
	now := time.Now().UTC()
	_, err := a.Store.DB.ExecContext(r.Context(), `
		INSERT INTO api_tokens(id_hash,user_id,label,kind,scopes,created_at,expires_at)
		VALUES(?,?,?,?,?,?,?)`,
		a.Cipher.TokenHash(raw), identity.User.ID, "outbound-enable", "stepup",
		store.JSON([]string{"outbound:enable"}), now.Format(time.RFC3339Nano),
		now.Add(5*time.Minute).Format(time.RFC3339Nano))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create step-up token")
		return
	}
	a.audit(r.Context(), "auth.step_up", identity.User.ID, identity.User.ID, identity.User, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "token": raw, "expires_in": 300})
}

func (a *App) changePassword(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireCSRF(w, r)
	if !ok {
		return
	}
	var body struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
		TOTP            string `json:"totp"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid request")
		return
	}
	if !security.VerifyPassword(identity.User.PasswordHash, body.CurrentPassword) ||
		!a.verifyUserTOTP(identity.User, body.TOTP) {
		writeError(w, http.StatusBadRequest, "invalid current password or TOTP")
		return
	}
	passwordHash, err := security.HashPassword(body.NewPassword)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	now := store.Now()
	err = a.Store.WithTx(r.Context(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE users SET password_hash=? WHERE id=?`,
			passwordHash, identity.User.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(`
			UPDATE auth_sessions SET revoked_at=? WHERE user_id=? AND revoked_at IS NULL`,
			now, identity.User.ID); err != nil {
			return err
		}
		_, err := tx.Exec(`
			UPDATE api_tokens SET revoked_at=?
			WHERE user_id=? AND kind IN ('stepup','setup') AND revoked_at IS NULL`,
			now, identity.User.ID)
		return err
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not change password")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: "bili_session", Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: a.Settings.SecureCookies, SameSite: http.SameSiteStrictMode,
	})
	a.audit(r.Context(), "auth.password_change", identity.User.ID, identity.User.ID, identity.User, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "message": "password changed; sign in again",
	})
}

func (a *App) requireWeb(w http.ResponseWriter, r *http.Request) (authContext, bool) {
	identity, err := a.currentSession(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return authContext{}, false
	}
	return identity, true
}

func (a *App) requireCSRF(w http.ResponseWriter, r *http.Request) (authContext, bool) {
	identity, ok := a.requireWeb(w, r)
	if !ok {
		return authContext{}, false
	}
	if !strings.EqualFold(r.Method, http.MethodGet) && !strings.EqualFold(r.Method, http.MethodHead) {
		candidate := r.Header.Get("X-CSRF-Token")
		if candidate == "" ||
			!hmac.Equal([]byte(a.Cipher.TokenHash(candidate)), []byte(identity.Session.CSRFHash)) {
			writeError(w, http.StatusForbidden, "invalid CSRF token")
			return authContext{}, false
		}
	}
	return identity, true
}

func (a *App) requireAdmin(w http.ResponseWriter, r *http.Request) (authContext, bool) {
	identity, ok := a.requireCSRF(w, r)
	if !ok {
		return authContext{}, false
	}
	if identity.User.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin role required")
		return authContext{}, false
	}
	return identity, true
}

func (a *App) currentSession(r *http.Request) (authContext, error) {
	cookie, err := r.Cookie("bili_session")
	if err != nil || cookie.Value == "" {
		return authContext{}, errors.New("missing session")
	}
	var row model.Session
	var created, expires, idle string
	var revoked sql.NullString
	err = a.Store.DB.QueryRowContext(r.Context(), `
		SELECT id_hash,user_id,csrf_hash,created_at,expires_at,idle_expires_at,revoked_at,ip_hint
		FROM auth_sessions WHERE id_hash=? AND revoked_at IS NULL`,
		a.Cipher.TokenHash(cookie.Value)).
		Scan(&row.IDHash, &row.UserID, &row.CSRFHash, &created, &expires, &idle, &revoked, &row.IPHint)
	if err != nil {
		return authContext{}, err
	}
	now := time.Now().UTC()
	expiresTime, okExpires := store.ParseTime(expires)
	idleTime, okIdle := store.ParseTime(idle)
	createdTime, _ := store.ParseTime(created)
	if !okExpires || !okIdle || !expiresTime.After(now) || !idleTime.After(now) {
		_, _ = a.Store.DB.ExecContext(r.Context(),
			`UPDATE auth_sessions SET revoked_at=? WHERE id_hash=?`, store.Now(), row.IDHash)
		return authContext{}, errors.New("session expired")
	}
	row.CreatedAt = createdTime
	row.ExpiresAt = expiresTime
	row.IdleExpiresAt = idleTime
	user, err := a.userByID(r.Context(), row.UserID)
	if err != nil || user.Disabled || !user.TOTPConfirmed {
		return authContext{}, errors.New("user unavailable")
	}
	newIdle := now.Add(a.Settings.SessionIdleDuration)
	row.IdleExpiresAt = newIdle
	_, _ = a.Store.DB.ExecContext(r.Context(),
		`UPDATE auth_sessions SET idle_expires_at=? WHERE id_hash=?`,
		newIdle.Format(time.RFC3339Nano), row.IDHash)
	return authContext{User: user, Session: row}, nil
}

func (a *App) createSession(ctx context.Context, user model.User, ip string) (string, string, error) {
	rawSession, err := security.NewSecret("bili_sess", 32)
	if err != nil {
		return "", "", err
	}
	rawCSRF, err := security.NewSecret("bili_csrf", 24)
	if err != nil {
		return "", "", err
	}
	now := time.Now().UTC()
	_, err = a.Store.DB.ExecContext(ctx, `
		INSERT INTO auth_sessions(id_hash,user_id,csrf_hash,created_at,expires_at,idle_expires_at,ip_hint)
		VALUES(?,?,?,?,?,?,?)`,
		a.Cipher.TokenHash(rawSession), user.ID, a.Cipher.TokenHash(rawCSRF),
		now.Format(time.RFC3339Nano),
		now.Add(a.Settings.SessionDuration).Format(time.RFC3339Nano),
		now.Add(a.Settings.SessionIdleDuration).Format(time.RFC3339Nano),
		ip)
	return rawSession, rawCSRF, err
}

func (a *App) setSessionCookie(w http.ResponseWriter, raw string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "bili_session",
		Value:    raw,
		Path:     "/",
		MaxAge:   int(a.Settings.SessionDuration.Seconds()),
		HttpOnly: true,
		Secure:   a.Settings.SecureCookies,
		SameSite: http.SameSiteStrictMode,
	})
}

func (a *App) userByUsername(ctx context.Context, username string) (model.User, error) {
	return a.scanUser(a.Store.DB.QueryRowContext(ctx, `
		SELECT id,username,display_name,role,COALESCE(password_hash,''),
			COALESCE(totp_secret_enc,''),totp_confirmed,disabled,created_at,
			COALESCE(last_login_at,'')
		FROM users WHERE username=?`, username))
}

func (a *App) userByID(ctx context.Context, userID string) (model.User, error) {
	return a.scanUser(a.Store.DB.QueryRowContext(ctx, `
		SELECT id,username,display_name,role,COALESCE(password_hash,''),
			COALESCE(totp_secret_enc,''),totp_confirmed,disabled,created_at,
			COALESCE(last_login_at,'')
		FROM users WHERE id=?`, userID))
}

type scanner interface {
	Scan(dest ...any) error
}

func (a *App) scanUser(row scanner) (model.User, error) {
	var user model.User
	var confirmed, disabled int64
	err := row.Scan(
		&user.ID, &user.Username, &user.DisplayName, &user.Role, &user.PasswordHash,
		&user.TOTPSecretEnc, &confirmed, &disabled, &user.CreatedAt, &user.LastLoginAt,
	)
	user.TOTPConfirmed = confirmed != 0
	user.Disabled = disabled != 0
	return user, err
}

func (a *App) verifyUserTOTP(user model.User, code string) bool {
	return a.verifyEncryptedTOTP(user.TOTPSecretEnc, user.Username, code)
}

func (a *App) verifyEncryptedTOTP(encrypted, username, code string) bool {
	raw, err := a.Cipher.DecryptBytes(encrypted, "totp:"+username)
	if err != nil {
		return false
	}
	return security.VerifyTOTP(string(raw), code, time.Now())
}

func (a *App) audit(ctx context.Context, event, userID, actorID string, actor model.User, ip string) {
	actorValue := actorID
	if actor.ID != "" {
		actorValue = actor.ID
	}
	_, _ = a.Store.DB.ExecContext(ctx, `
		INSERT INTO audit_events(id,user_id,actor_user_id,event,target,detail,ip,created_at)
		VALUES(?,?,?,?,?,?,?,?)`,
		model.NewID(), nullIfEmpty(userID), nullIfEmpty(actorValue), event, nil, "{}", ip, store.Now())
}

func publicUserValue(user model.User) map[string]any {
	return map[string]any{
		"id": user.ID, "username": user.Username, "display_name": user.DisplayName, "role": user.Role,
	}
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func inviteExpired(value string) bool {
	expires, ok := store.ParseTime(value)
	return !ok || !expires.After(time.Now().UTC())
}

func qrDataURI(value string) string {
	raw, err := qrPNG(value)
	if err != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)
}
