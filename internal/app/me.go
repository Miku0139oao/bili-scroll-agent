package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Miku0139oao/bili-scroll-agent/internal/model"
	"github.com/Miku0139oao/bili-scroll-agent/internal/runner"
	"github.com/Miku0139oao/bili-scroll-agent/internal/security"
	"github.com/Miku0139oao/bili-scroll-agent/internal/store"
)

func (a *App) status(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireWeb(w, r)
	if !ok {
		return
	}
	config, err := a.loadConfig(r.Context(), identity.User.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load config")
		return
	}
	schedule := config.Schedule
	var nextRun sql.NullString
	var enabled int
	_ = a.Store.DB.QueryRowContext(r.Context(), `
		SELECT enabled,COALESCE(next_run_at,'') FROM schedules WHERE user_id=?`,
		identity.User.ID).Scan(&enabled, &nextRun)
	schedule.Enabled = enabled != 0
	if nextRun.Valid {
		schedule.NextRunAt = nextRun.String
	}
	var uid sql.NullInt64
	var nickname sql.NullString
	err = a.Store.DB.QueryRowContext(r.Context(), `
		SELECT uid,nickname FROM bili_credentials WHERE user_id=?`, identity.User.ID).
		Scan(&uid, &nickname)
	loggedIn := err == nil
	pickedToday := a.pickedCount(r.Context(), identity.User.ID, LocalDay(config, time.Now()))
	admin := a.loadAdminSettings(r.Context())
	routeProvider := admin.CodexProvider
	routeModel := admin.CodexModel
	if config.Runner == "hermes" {
		routeProvider = admin.HermesProvider
		routeModel = admin.HermesModel
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "user": publicUserValue(identity.User), "config": config,
		"schedule": schedule,
		"bilibili": map[string]any{
			"logged_in": loggedIn, "uid": uid.Int64, "nickname": nickname.String,
		},
		"picked_today":    pickedToday,
		"remaining_today": max(0, config.PerDay-pickedToday),
		"runners":         runner.Status(a.Settings),
		"runner_route": map[string]any{
			"runner": config.Runner, "provider": routeProvider, "model": routeModel,
			"source": map[bool]string{true: "admin", false: "runtime-default"}[routeProvider != "" || routeModel != ""],
		},
		"active_run":     runJSON(a.activeRun(r.Context(), identity.User.ID)),
		"latest_run":     runJSON(a.latestRun(r.Context(), identity.User.ID)),
		"admin_settings": map[string]any{"outbound_enabled": admin.OutboundEnabled},
		"version":        "0.2.0-go",
	})
}

func (a *App) getConfig(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireWeb(w, r)
	if !ok {
		return
	}
	config, err := a.loadConfig(r.Context(), identity.User.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load config")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "config": config})
}

func (a *App) patchConfig(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireCSRF(w, r)
	if !ok {
		return
	}
	var patch map[string]any
	if err := decodeJSON(r, &patch); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid config")
		return
	}
	outbound := boolFromMap(patch, "autoComment") || boolFromMap(patch, "replyToComments") ||
		boolFromMap(patch, "dmWatch") || stringFromAny(patch["dmMode"]) == "only" ||
		stringFromAny(patch["dmMode"]) == "both" || intFromAny(patch["favFolderId"], 0) > 0
	if outbound && !a.consumeStepUp(r, identity.User.ID) {
		writeError(w, http.StatusForbidden, "password and TOTP step-up required")
		return
	}
	config, err := a.saveConfig(r.Context(), identity.User.ID, patch)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	keys := make([]string, 0, len(patch))
	for key := range patch {
		keys = append(keys, key)
	}
	a.auditDetail(r.Context(), "member.config_update", identity.User.ID, identity.User,
		map[string]any{"keys": keys}, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "config": config})
}

func (a *App) watched(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireWeb(w, r)
	if !ok {
		return
	}
	offset := queryInt(r, "offset", 0, 0, 1_000_000)
	limit := queryInt(r, "limit", 30, 1, 100)
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	tier := strings.TrimSpace(r.URL.Query().Get("tier"))
	where := []string{"user_id=?"}
	args := []any{identity.User.ID}
	if state != "" {
		where = append(where, "state=?")
		args = append(args, state)
	}
	if tier != "" {
		where = append(where, "tier=?")
		args = append(args, tier)
	}
	clause := strings.Join(where, " AND ")
	var total int
	_ = a.Store.DB.QueryRowContext(r.Context(),
		"SELECT COUNT(*) FROM video_records WHERE "+clause, args...).Scan(&total)
	queryArgs := append(append([]any(nil), args...), limit, offset)
	rows, err := a.Store.DB.QueryContext(r.Context(), `
		SELECT bvid,day,title,author,url,state,COALESCE(tier,''),COALESCE(reason,''),
			COALESCE(one_liner,''),delivered,metadata_json
		FROM video_records WHERE `+clause+` ORDER BY seen_at DESC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load watched videos")
		return
	}
	defer rows.Close()
	result := make([]map[string]any, 0)
	for rows.Next() {
		var bvid, day, title, author, link, stateValue, tierValue, reason, oneLiner, metadata string
		var delivered int
		if err := rows.Scan(&bvid, &day, &title, &author, &link, &stateValue, &tierValue,
			&reason, &oneLiner, &delivered, &metadata); err != nil {
			continue
		}
		var meta map[string]any
		_ = json.Unmarshal([]byte(metadata), &meta)
		result = append(result, map[string]any{
			"bvid": bvid, "day": day, "title": title, "up": author, "url": link,
			"picked": stateValue == "picked", "state": stateValue, "tier": tierValue,
			"why": reason, "one_liner": oneLiner, "delivered": delivered != 0,
			"cover": "/api/me/cover?bvid=" + url.QueryEscape(bvid),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "rows": result, "total": total})
}

func (a *App) report(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireWeb(w, r)
	if !ok {
		return
	}
	config, _ := a.loadConfig(r.Context(), identity.User.ID)
	day := LocalDay(config, time.Now())
	var markdown, summary string
	err := a.Store.DB.QueryRowContext(r.Context(), `
		SELECT markdown,summary FROM reports WHERE user_id=? AND day=?`,
		identity.User.ID, day).Scan(&markdown, &summary)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "exists": false, "day": day})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "exists": true, "day": day, "markdown": markdown, "summary": summary,
	})
}

func (a *App) deliveries(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireWeb(w, r)
	if !ok {
		return
	}
	rows, err := a.Store.DB.QueryContext(r.Context(), `
		SELECT id,kind,bvid,content,status,COALESCE(remote_id,''),visible,created_at
		FROM deliveries WHERE user_id=? ORDER BY created_at DESC LIMIT 100`, identity.User.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load deliveries")
		return
	}
	defer rows.Close()
	result := make([]map[string]any, 0)
	for rows.Next() {
		var id, kind, bvid, content, status, remote, created string
		var visible sql.NullInt64
		if rows.Scan(&id, &kind, &bvid, &content, &status, &remote, &visible, &created) == nil {
			item := map[string]any{
				"id": id, "kind": kind, "bvid": bvid, "content": content,
				"status": status, "remote_id": remote, "created_at": created,
			}
			if visible.Valid {
				item["visible"] = visible.Int64 != 0
			}
			result = append(result, item)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "rows": result})
}

func (a *App) listTokens(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireWeb(w, r)
	if !ok {
		return
	}
	rows, err := a.Store.DB.QueryContext(r.Context(), `
		SELECT substr(id_hash,1,12),label,created_at,COALESCE(last_used_at,'')
		FROM api_tokens WHERE user_id=? AND kind='device' AND revoked_at IS NULL
		ORDER BY created_at DESC`, identity.User.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load tokens")
		return
	}
	defer rows.Close()
	result := make([]map[string]any, 0)
	for rows.Next() {
		var prefix, label, created, lastUsed string
		if rows.Scan(&prefix, &label, &created, &lastUsed) == nil {
			result = append(result, map[string]any{
				"id_hash": prefix, "label": label, "created_at": created,
				"last_used_at": nullIfEmpty(lastUsed),
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "tokens": result})
}

func (a *App) createToken(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireCSRF(w, r)
	if !ok {
		return
	}
	var body struct {
		Label string `json:"label"`
	}
	if err := decodeJSON(r, &body); err != nil || strings.TrimSpace(body.Label) == "" {
		writeError(w, http.StatusUnprocessableEntity, "token label is required")
		return
	}
	raw, _ := security.NewSecret("bili_device", 32)
	scopes := []string{
		"bili:read", "bili:config", "bili:login", "bili:write", "bili:asr",
		"run:begin", "run:finish", "run:request", "usage:import",
	}
	_, err := a.Store.DB.ExecContext(r.Context(), `
		INSERT INTO api_tokens(id_hash,user_id,label,kind,scopes,created_at)
		VALUES(?,?,?,?,?,?)`,
		a.Cipher.TokenHash(raw), identity.User.ID, strings.TrimSpace(body.Label),
		"device", store.JSON(scopes), store.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "token": raw, "label": body.Label})
}

func (a *App) revokeToken(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireCSRF(w, r)
	if !ok {
		return
	}
	prefix := r.PathValue("prefix")
	if len(prefix) < 8 {
		writeError(w, http.StatusUnprocessableEntity, "token prefix too short")
		return
	}
	result, err := a.Store.DB.ExecContext(r.Context(), `
		UPDATE api_tokens SET revoked_at=?
		WHERE user_id=? AND kind='device' AND revoked_at IS NULL AND substr(id_hash,1,?)=?`,
		store.Now(), identity.User.ID, len(prefix), prefix)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not revoke token")
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		writeError(w, http.StatusNotFound, "token not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) diagnostics(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireWeb(w, r)
	if !ok {
		return
	}
	config, _ := a.loadConfig(r.Context(), identity.User.ID)
	var credentialCount int
	_ = a.Store.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM bili_credentials WHERE user_id=?`, identity.User.ID).Scan(&credentialCount)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "version": "0.2.0-go", "user_id": identity.User.ID,
		"runner": config.Runner, "runners": runner.Status(a.Settings),
		"bilibili_logged_in": credentialCount > 0,
		"outbound": map[string]any{
			"comment": config.AutoComment, "dm_mode": config.DMMode, "dm_watch": config.DMWatch,
		},
		"note": "Secrets and Bilibili cookies are intentionally excluded.",
	})
}

func (a *App) exportData(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireWeb(w, r)
	if !ok {
		return
	}
	config, _ := a.loadConfig(r.Context(), identity.User.ID)
	reports := make([]map[string]any, 0)
	if rows, err := a.Store.DB.QueryContext(r.Context(),
		`SELECT day,markdown,summary FROM reports WHERE user_id=? ORDER BY day`, identity.User.ID); err == nil {
		for rows.Next() {
			var day, markdown, summary string
			if rows.Scan(&day, &markdown, &summary) == nil {
				reports = append(reports, map[string]any{
					"day": day, "markdown": markdown, "summary": summary,
				})
			}
		}
		rows.Close()
	}
	videos := make([]map[string]any, 0)
	if rows, err := a.Store.DB.QueryContext(r.Context(), `
		SELECT bvid,day,title,author,url,state,COALESCE(tier,''),COALESCE(reason,'')
		FROM video_records WHERE user_id=? ORDER BY seen_at`, identity.User.ID); err == nil {
		for rows.Next() {
			var bvid, day, title, author, link, stateValue, tier, reason string
			if rows.Scan(&bvid, &day, &title, &author, &link, &stateValue, &tier, &reason) == nil {
				videos = append(videos, map[string]any{
					"bvid": bvid, "day": day, "title": title, "author": author, "url": link,
					"state": stateValue, "tier": tier, "reason": reason,
				})
			}
		}
		rows.Close()
	}
	w.Header().Set("Content-Disposition", `attachment; filename="bili-scroll-`+identity.User.Username+`.json"`)
	writeJSON(w, http.StatusOK, map[string]any{
		"exported_at": store.Now(), "user": publicUserValue(identity.User), "config": config,
		"reports": reports, "videos": videos,
	})
}

func (a *App) streamEvents(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireWeb(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	ch, unsubscribe := a.events.Subscribe(identity.User.ID)
	defer unsubscribe()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-ch:
			if !open {
				return
			}
			raw, _ := json.Marshal(event.Data)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Name, raw)
			flusher.Flush()
		}
	}
}

func (a *App) cover(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireWeb(w, r)
	if !ok {
		return
	}
	bvid := r.URL.Query().Get("bvid")
	var metadata string
	_ = a.Store.DB.QueryRowContext(r.Context(), `
		SELECT metadata_json FROM video_records WHERE user_id=? AND bvid=?`,
		identity.User.ID, bvid).Scan(&metadata)
	var video model.Video
	_ = json.Unmarshal([]byte(metadata), &video)
	source := video.Cover
	if source == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"detail": "cover unavailable"})
		return
	}
	parsed, err := url.Parse(source)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") ||
		!strings.HasSuffix(parsed.Hostname(), ".hdslb.com") {
		writeError(w, http.StatusBadRequest, "unsupported cover URL")
		return
	}
	request, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, source, nil)
	request.Header.Set("User-Agent", "bili-scroll-agent/0.2")
	response, err := http.DefaultClient.Do(request)
	if err != nil || response.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway, "cover download failed")
		return
	}
	defer response.Body.Close()
	w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
	w.Header().Set("Cache-Control", "private,max-age=86400")
	_, _ = io.Copy(w, response.Body)
}

func (a *App) pickedCount(ctx context.Context, userID, day string) int {
	var count int
	_ = a.Store.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM video_records WHERE user_id=? AND day=? AND state='picked'`,
		userID, day).Scan(&count)
	return count
}

func (a *App) consumeStepUp(r *http.Request, userID string) bool {
	raw := r.Header.Get("X-Step-Up-Token")
	if raw == "" {
		return false
	}
	var expires sql.NullString
	var scopes string
	err := a.Store.DB.QueryRowContext(r.Context(), `
		SELECT scopes,expires_at FROM api_tokens
		WHERE id_hash=? AND user_id=? AND kind='stepup' AND revoked_at IS NULL`,
		a.Cipher.TokenHash(raw), userID).Scan(&scopes, &expires)
	if err != nil {
		return false
	}
	expiry, ok := store.ParseTime(expires.String)
	if !ok || !expiry.After(time.Now().UTC()) {
		return false
	}
	var scopeValues []string
	_ = json.Unmarshal([]byte(scopes), &scopeValues)
	found := false
	for _, scope := range scopeValues {
		if scope == "outbound:enable" {
			found = true
			break
		}
	}
	if !found {
		return false
	}
	_, _ = a.Store.DB.ExecContext(r.Context(), `
		UPDATE api_tokens SET revoked_at=? WHERE id_hash=?`,
		store.Now(), a.Cipher.TokenHash(raw))
	return true
}

func (a *App) auditDetail(
	ctx context.Context,
	event, userID string,
	actor model.User,
	detail map[string]any,
	ip string,
) {
	_, _ = a.Store.DB.ExecContext(ctx, `
		INSERT INTO audit_events(id,user_id,actor_user_id,event,target,detail,ip,created_at)
		VALUES(?,?,?,?,?,?,?,?)`,
		model.NewID(), userID, actor.ID, event, nil, store.JSON(detail), ip, store.Now())
}

func boolFromMap(value map[string]any, key string) bool {
	result, _ := value[key].(bool)
	return result
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
