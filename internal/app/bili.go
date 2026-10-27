package app

import (
	"context"
	"database/sql"
	"encoding/base64"
	"net/http"
	"strconv"
	"time"

	"github.com/Miku0139oao/bili-scroll-agent/internal/bilibili"
	"github.com/Miku0139oao/bili-scroll-agent/internal/store"
)

func (a *App) biliLoginStart(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireCSRF(w, r)
	if !ok {
		return
	}
	key, loginURL, _, err := a.Bili.StartLogin(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	raw, err := qrPNG(loginURL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not render QR code")
		return
	}
	a.broker.Claim(key, identity.User.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "key": key, "url": loginURL,
		"qr": "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw),
	})
}

func (a *App) biliLoginPoll(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireCSRF(w, r)
	if !ok {
		return
	}
	var body struct {
		Key string `json:"key"`
	}
	if err := decodeJSON(r, &body); err != nil || body.Key == "" {
		writeError(w, http.StatusUnprocessableEntity, "key is required")
		return
	}
	if owner := a.broker.Owner(body.Key); owner != identity.User.ID {
		writeError(w, http.StatusForbidden, "login session belongs to another user")
		return
	}
	state, err := a.Bili.PollLogin(r.Context(), body.Key)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if state.State == "expired" {
		a.broker.Forget(body.Key)
	}
	if state.State != "ok" {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": state.State != "unknown", "state": state.State, "code": state.Code,
			"message": state.Message,
		})
		return
	}
	if err := a.saveCredential(r.Context(), identity.User.ID, state.Credential); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save Bilibili credential")
		return
	}
	profile, profileErr := a.Bili.SelfInfo(r.Context(), state.Credential)
	uid, _ := strconv.ParseInt(state.Credential.DedeUserID, 10, 64)
	nickname := ""
	if profileErr == nil {
		uid = int64(numberFromAny(profile["uid"]))
		nickname = stringFromAny(profile["nickname"])
	}
	_, _ = a.Store.DB.ExecContext(r.Context(), `
		UPDATE bili_credentials SET uid=?,nickname=?,updated_at=? WHERE user_id=?`,
		uid, nickname, store.Now(), identity.User.ID)
	if config, err := a.loadConfig(r.Context(), identity.User.ID); err == nil && config.DMUID == 0 && uid > 0 {
		_, _ = a.saveConfig(r.Context(), identity.User.ID, map[string]any{"dmUid": uid})
	}
	a.broker.Forget(body.Key)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "state": "ok", "code": 0, "uid": uid, "nickname": nickname,
	})
}

func (a *App) saveCredential(
	ctx context.Context,
	userID string,
	credential bilibili.Credential,
) error {
	if !credential.Complete() {
		return errIncompleteCredential
	}
	encrypted, err := a.Cipher.EncryptJSON(map[string]string{
		"sessdata": credential.SESSDATA, "bili_jct": credential.BiliJCT,
		"buvid3": credential.Buvid3, "dedeuserid": credential.DedeUserID,
	}, "bili:"+userID)
	if err != nil {
		return err
	}
	uid, _ := strconv.ParseInt(credential.DedeUserID, 10, 64)
	_, err = a.Store.DB.ExecContext(ctx, `
		INSERT INTO bili_credentials(user_id,ciphertext,uid,updated_at) VALUES(?,?,?,?)
		ON CONFLICT(user_id) DO UPDATE SET ciphertext=excluded.ciphertext,
			uid=excluded.uid,updated_at=excluded.updated_at`,
		userID, encrypted, uid, store.Now())
	return err
}

func (a *App) credential(ctx context.Context, userID string) (bilibili.Credential, error) {
	var encrypted string
	err := a.Store.DB.QueryRowContext(ctx,
		`SELECT ciphertext FROM bili_credentials WHERE user_id=?`, userID).Scan(&encrypted)
	if err != nil {
		if err == sql.ErrNoRows {
			return bilibili.Credential{}, errBiliLoginRequired
		}
		return bilibili.Credential{}, err
	}
	var raw map[string]string
	if err := a.Cipher.DecryptJSON(encrypted, "bili:"+userID, &raw); err != nil {
		return bilibili.Credential{}, err
	}
	credential := bilibili.Credential{
		SESSDATA:   firstNonEmptyText(raw["sessdata"], raw["SESSDATA"]),
		BiliJCT:    raw["bili_jct"],
		Buvid3:     raw["buvid3"],
		DedeUserID: firstNonEmptyText(raw["dedeuserid"], raw["DedeUserID"]),
	}
	if !credential.Complete() {
		return bilibili.Credential{}, errBiliLoginRequired
	}
	return credential, nil
}

func (a *App) favoriteFolders(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireWeb(w, r)
	if !ok {
		return
	}
	credential, err := a.credential(r.Context(), identity.User.ID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": false, "error": err.Error(), "folders": []any{},
		})
		return
	}
	folders, err := a.Bili.FavoriteFolders(r.Context(), credential)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": false, "error": truncateText(err.Error(), 300), "folders": []any{},
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "folders": folders})
}

func (a *App) favoriteToday(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireCSRF(w, r)
	if !ok {
		return
	}
	config, _ := a.loadConfig(r.Context(), identity.User.ID)
	if config.FavFolderID <= 0 {
		writeError(w, http.StatusUnprocessableEntity, "favorite folder is not configured")
		return
	}
	credential, err := a.credential(r.Context(), identity.User.ID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	day := LocalDay(config, time.Now())
	rows, _ := a.Store.DB.QueryContext(r.Context(), `
		SELECT bvid FROM video_records WHERE user_id=? AND day=? AND state='picked'`,
		identity.User.ID, day)
	added, total := 0, 0
	failures := make([]map[string]any, 0)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var bvid string
			if rows.Scan(&bvid) != nil {
				continue
			}
			total++
			video, err := a.Bili.VideoSummary(r.Context(), credential, bvid)
			if err == nil {
				err = a.Bili.FavoriteAdd(r.Context(), credential, video.AID, config.FavFolderID)
			}
			if err != nil {
				failures = append(failures, map[string]any{
					"bvid": bvid, "error": truncateText(err.Error(), 300),
				})
			} else {
				added++
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": len(failures) == 0, "added": added, "total": total, "failures": failures,
	})
}

func (a *App) verifyDeliveries(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireCSRF(w, r)
	if !ok {
		return
	}
	checked, err := a.verifyRecentDeliveries(r.Context(), identity.User.ID, 20)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "checked": checked})
}

func (a *App) verifyRecentDeliveries(
	ctx context.Context,
	userID string,
	maxItems int,
) ([]map[string]any, error) {
	credential, err := a.credential(ctx, userID)
	if err != nil {
		return nil, err
	}
	rows, err := a.Store.DB.QueryContext(ctx, `
		SELECT id,bvid,COALESCE(remote_id,''),content FROM deliveries
		WHERE user_id=? AND kind='comment' AND status='sent' AND verified_at IS NULL
		ORDER BY sent_at LIMIT ?`, userID, maxItems)
	if err != nil {
		return nil, err
	}
	type target struct {
		ID, BVID, RemoteID, Content string
	}
	targets := make([]target, 0)
	if rows != nil {
		for rows.Next() {
			var item target
			if rows.Scan(&item.ID, &item.BVID, &item.RemoteID, &item.Content) == nil {
				targets = append(targets, item)
			}
		}
		rows.Close()
	}
	checked := make([]map[string]any, 0)
	for _, item := range targets {
		comments, err := a.Bili.Comments(ctx, credential, item.BVID, 20)
		if err != nil {
			continue
		}
		visible := false
		for _, comment := range comments {
			if strconv.FormatInt(int64(numberFromAny(comment["rpid"])), 10) == item.RemoteID ||
				stringFromAny(comment["content"]) == item.Content {
				visible = true
				break
			}
		}
		_, _ = a.Store.DB.ExecContext(ctx, `
			UPDATE deliveries SET verified_at=?,visible=? WHERE id=?`,
			store.Now(), boolInt(visible), item.ID)
		checked = append(checked, map[string]any{
			"id": item.ID, "bvid": item.BVID, "visible": visible,
		})
	}
	return checked, nil
}

func (a *App) verifyRecentDMs(
	ctx context.Context,
	userID string,
	maxItems int,
) ([]map[string]any, error) {
	credential, err := a.credential(ctx, userID)
	if err != nil {
		return nil, err
	}
	rows, err := a.Store.DB.QueryContext(ctx, `
		SELECT id,COALESCE(remote_id,''),COALESCE(root_id,'') FROM deliveries
		WHERE user_id=? AND kind='dm' AND status='sent' AND verified_at IS NULL
		ORDER BY sent_at LIMIT ?`, userID, maxItems)
	if err != nil {
		return nil, err
	}
	type target struct {
		ID, RemoteID, ReceiverUID string
	}
	targets := make([]target, 0)
	if rows != nil {
		for rows.Next() {
			var item target
			if rows.Scan(&item.ID, &item.RemoteID, &item.ReceiverUID) == nil {
				targets = append(targets, item)
			}
		}
		rows.Close()
	}
	checked := make([]map[string]any, 0)
	for _, item := range targets {
		result := map[string]any{"id": item.ID, "kind": "dm", "receiver_uid": item.ReceiverUID}
		receiverUID, parseErr := strconv.ParseInt(item.ReceiverUID, 10, 64)
		if parseErr != nil || receiverUID <= 0 {
			result["visible"] = false
			result["error"] = "DM receiver UID is missing"
			checked = append(checked, result)
			continue
		}
		messages, messageErr := a.Bili.DMMessages(ctx, credential, receiverUID, 50)
		if messageErr != nil {
			result["visible"] = false
			result["error"] = messageErr.Error()
			checked = append(checked, result)
			continue
		}
		visible := false
		for _, message := range messages {
			if item.RemoteID != "" && stringFromAny(message["key"]) == item.RemoteID {
				visible = true
				break
			}
		}
		sessionPresent := false
		if sessions, sessionErr := a.Bili.DMSessions(ctx, credential); sessionErr == nil {
			for _, session := range sessions {
				if int64FromAny(session["uid"]) == receiverUID {
					sessionPresent = true
					break
				}
			}
		} else {
			result["session_error"] = sessionErr.Error()
		}
		_, _ = a.Store.DB.ExecContext(ctx, `
			UPDATE deliveries SET verified_at=?,visible=? WHERE id=?`,
			store.Now(), boolInt(visible), item.ID)
		result["visible"] = visible
		result["session_present"] = sessionPresent
		checked = append(checked, result)
	}
	return checked, nil
}

var (
	errIncompleteCredential = &credentialError{"Bilibili credential is incomplete"}
	errBiliLoginRequired    = &credentialError{"Bilibili login is required"}
)

type credentialError struct{ message string }

func (e *credentialError) Error() string { return e.message }
