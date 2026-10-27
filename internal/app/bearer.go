package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Miku0139oao/bili-scroll-agent/internal/model"
	"github.com/Miku0139oao/bili-scroll-agent/internal/store"
)

func (a *App) requireBearer(w http.ResponseWriter, r *http.Request) (bearerContext, bool) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		writeError(w, http.StatusUnauthorized, "valid Bearer token required")
		return bearerContext{}, false
	}
	raw := strings.TrimSpace(header[7:])
	if raw == "" {
		writeError(w, http.StatusUnauthorized, "valid Bearer token required")
		return bearerContext{}, false
	}
	var userID, kind, scopes, expires, revoked, runID string
	err := a.Store.DB.QueryRowContext(r.Context(), `
		SELECT user_id,kind,scopes,COALESCE(expires_at,''),COALESCE(revoked_at,''),COALESCE(run_id,'')
		FROM api_tokens WHERE id_hash=?`, a.Cipher.TokenHash(raw)).
		Scan(&userID, &kind, &scopes, &expires, &revoked, &runID)
	if err != nil || revoked != "" || expired(expires) {
		writeError(w, http.StatusUnauthorized, "valid Bearer token required")
		return bearerContext{}, false
	}
	user, err := a.userByID(r.Context(), userID)
	if err != nil || user.Disabled || !user.TOTPConfirmed {
		writeError(w, http.StatusUnauthorized, "valid Bearer token required")
		return bearerContext{}, false
	}
	_, _ = a.Store.DB.ExecContext(r.Context(),
		`UPDATE api_tokens SET last_used_at=? WHERE id_hash=?`, store.Now(), a.Cipher.TokenHash(raw))
	var scopeValues []string
	_ = json.Unmarshal([]byte(scopes), &scopeValues)
	return bearerContext{
		User: user,
		Token: model.APIIdentifier{
			Hash: a.Cipher.TokenHash(raw), Kind: kind, RunID: runID, Scopes: scopeValues,
		},
	}, true
}

func requireScope(identity bearerContext, scope string) error {
	for _, item := range identity.Token.Scopes {
		if item == scope {
			return nil
		}
	}
	return errors.New("token lacks required scope: " + scope)
}

func expired(value string) bool {
	if value == "" {
		return false
	}
	parsed, ok := store.ParseTime(value)
	return !ok || !parsed.After(storeNow())
}

var storeNow = func() time.Time { return time.Now().UTC() }
