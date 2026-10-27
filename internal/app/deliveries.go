package app

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"github.com/Miku0139oao/bili-scroll-agent/internal/bilibili"
	"github.com/Miku0139oao/bili-scroll-agent/internal/model"
	"github.com/Miku0139oao/bili-scroll-agent/internal/store"
)

func (a *App) processDeliveries(
	ctx context.Context,
	user model.User,
	runID string,
	config model.MemberConfig,
	admin model.AdminSettings,
	picked []model.PickedItem,
	drafts []model.DeliveryDraft,
) []map[string]any {
	if !a.Settings.BindOutboundEnabled || !admin.OutboundEnabled {
		return []map[string]any{}
	}
	credential, err := a.credential(ctx, user.ID)
	if err != nil {
		return []map[string]any{{
			"ok": false, "error": "Bilibili credential unavailable before delivery",
		}}
	}
	result := make([]map[string]any, 0)
	if config.FavFolderID > 0 {
		for _, item := range picked {
			video, lookupErr := a.Bili.VideoSummary(ctx, credential, item.BVID)
			if lookupErr != nil {
				result = append(result, map[string]any{
					"kind": "favorite", "bvid": item.BVID, "ok": false, "error": lookupErr.Error(),
				})
				continue
			}
			err := a.Bili.FavoriteAdd(ctx, credential, video.AID, config.FavFolderID)
			result = append(result, map[string]any{
				"kind": "favorite", "bvid": item.BVID, "ok": err == nil, "error": errorText(err),
			})
		}
	}
	commentText := map[string]string{}
	for _, item := range picked {
		if textValue := strings.TrimSpace(item.Comment); textValue != "" {
			commentText[item.BVID] = textValue
		}
	}
	hasDMDraft := false
	for _, draft := range drafts {
		kind := strings.ToLower(strings.TrimSpace(draft.Kind))
		switch kind {
		case "comment":
			if config.AutoComment && config.DMMode != "only" {
				if _, exists := commentText[draft.BVID]; !exists {
					commentText[draft.BVID] = draft.Content
				}
			}
		case "dm", "private_message":
			hasDMDraft = true
			if config.DMMode != "only" && config.DMMode != "both" {
				continue
			}
			target := config.DMUID
			if draft.Target != "" {
				if parsed, parseErr := strconv.ParseInt(draft.Target, 10, 64); parseErr == nil {
					target = parsed
				}
			}
			item := a.deliverDM(ctx, credential, user.ID, runID, target, draft.Content)
			result = append(result, item)
		}
	}
	if !hasDMDraft && len(picked) > 0 && (config.DMMode == "only" || config.DMMode == "both") {
		result = append(result, a.deliverDM(ctx, credential, user.ID, runID, config.DMUID,
			buildPickedDigestDM(picked)))
	}
	commentLimit := config.CommentPerRound
	if commentLimit < 0 {
		commentLimit = 0
	}
	if commentLimit > 20 {
		commentLimit = 20
	}
	sentComments := 0
	if config.AutoComment && config.DMMode != "only" {
		for _, pickedItem := range picked {
			if sentComments >= commentLimit {
				break
			}
			textValue, exists := commentText[pickedItem.BVID]
			if !exists {
				continue
			}
			item := a.deliverComment(ctx, credential, user.ID, runID, config, pickedItem.BVID, textValue)
			result = append(result, item)
			sentComments++
		}
	}
	return result
}

func buildPickedDigestDM(picked []model.PickedItem) string {
	var builder strings.Builder
	builder.WriteString("今日 B 站精選\n")
	for index, item := range picked {
		if index >= 10 {
			break
		}
		builder.WriteString(strconv.Itoa(index + 1))
		builder.WriteString(". ")
		builder.WriteString(strings.TrimSpace(item.Title))
		if item.BVID != "" {
			builder.WriteString("｜")
			builder.WriteString(item.BVID)
		}
		if line := strings.TrimSpace(item.OneLiner); line != "" {
			builder.WriteString("｜")
			builder.WriteString(line)
		}
		builder.WriteString("\n")
	}
	runes := []rune(strings.TrimSpace(builder.String()))
	if len(runes) > 500 {
		return string(runes[:499]) + "…"
	}
	return string(runes)
}

func (a *App) deliverComment(
	ctx context.Context,
	credential bilibili.Credential,
	userID, runID string,
	config model.MemberConfig,
	bvid, textValue string,
) map[string]any {
	var existing int
	_ = a.Store.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM deliveries
		WHERE user_id=? AND bvid=? AND kind='comment' AND status IN ('sent','verified')`,
		userID, bvid).Scan(&existing)
	if existing > 0 {
		return map[string]any{"kind": "comment", "bvid": bvid, "ok": true, "duplicate": true}
	}
	video, err := a.Bili.VideoSummary(ctx, credential, bvid)
	if err != nil {
		return map[string]any{"kind": "comment", "bvid": bvid, "ok": false, "error": err.Error()}
	}
	textValue = strings.TrimSpace(textValue)
	if config.MentionName != "" && !strings.Contains(textValue, "@"+config.MentionName) {
		textValue = "@" + config.MentionName + " " + textValue
	}
	textValue = truncateText(textValue, 150)
	deliveryID := model.NewID()
	insertDelivery := func(status, remoteID, errorMessage string) {
		_, _ = a.Store.DB.ExecContext(ctx, `
			INSERT OR IGNORE INTO deliveries(id,user_id,run_id,bvid,kind,content,status,remote_id,
				root_id,attempts,error,created_at,sent_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			deliveryID, userID, runID, bvid, "comment", textValue, status, remoteID,
			"", 1, nullIfEmpty(errorMessage), store.Now(), store.Now())
	}
	mentionUID := int64(0)
	if config.MentionName != "" {
		mentionUID = config.DMUID
	}
	receipt, err := a.Bili.SendComment(
		ctx, credential, video.AID, textValue, 0, 0, config.MentionName, mentionUID,
	)
	if err != nil {
		insertDelivery("failed", "", err.Error())
		return map[string]any{"kind": "comment", "bvid": bvid, "ok": false, "error": err.Error()}
	}
	remoteID := stringFromAny(receipt["rpid"])
	insertDelivery("sent", remoteID, "")
	return map[string]any{
		"kind": "comment", "bvid": bvid, "ok": true, "remote_id": remoteID, "text": textValue,
	}
}

func (a *App) deliverDM(
	ctx context.Context,
	credential bilibili.Credential,
	userID, runID string,
	receiverUID int64,
	textValue string,
) map[string]any {
	if receiverUID <= 0 {
		return map[string]any{"kind": "dm", "ok": false, "error": "DM recipient UID is missing"}
	}
	textValue = strings.TrimSpace(textValue)
	if textValue == "" {
		return map[string]any{"kind": "dm", "ok": false, "error": "DM content is empty"}
	}
	textValue = truncateWithEllipsis(textValue, 500)
	var existing int
	_ = a.Store.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM deliveries
		WHERE user_id=? AND kind='dm' AND content=? AND status IN ('sent','verified')`,
		userID, textValue).Scan(&existing)
	if existing > 0 {
		return map[string]any{"kind": "dm", "ok": true, "duplicate": true}
	}
	receipt, err := a.Bili.SendDM(ctx, credential, receiverUID, textValue)
	status := "sent"
	errorMessage := ""
	remoteID := ""
	if err != nil {
		status = "failed"
		errorMessage = err.Error()
	} else {
		remoteID = stringFromAny(receipt["msg_key"])
	}
	_, _ = a.Store.DB.ExecContext(ctx, `
		INSERT OR IGNORE INTO deliveries(id,user_id,run_id,bvid,kind,content,status,remote_id,
			root_id,attempts,error,created_at,sent_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		model.NewID(), userID, runID, "", "dm", textValue, status, remoteID,
		strconv.FormatInt(receiverUID, 10), 1, nullIfEmpty(errorMessage), store.Now(), store.Now())
	if err != nil {
		return map[string]any{"kind": "dm", "ok": false, "error": err.Error()}
	}
	return map[string]any{
		"kind": "dm", "ok": true, "remote_id": remoteID, "text": textValue,
	}
}

func (a *App) recentCommentTargets(ctx context.Context, userID string) ([]map[string]any, error) {
	rows, err := a.Store.DB.QueryContext(ctx, `
		SELECT bvid,COALESCE(remote_id,''),content,sent_at
		FROM deliveries
		WHERE user_id=? AND kind='comment' AND status IN ('sent','verified')
		ORDER BY sent_at DESC LIMIT 20`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]map[string]any, 0)
	for rows.Next() {
		var bvid, remoteID, content string
		var sentAt sql.NullString
		if rows.Scan(&bvid, &remoteID, &content, &sentAt) == nil {
			result = append(result, map[string]any{
				"bvid": bvid, "remote_id": remoteID, "content": content, "sent_at": nullTime(sentAt),
			})
		}
	}
	return result, rows.Err()
}

func (a *App) pendingCommentReplies(ctx context.Context, userID string) ([]map[string]any, error) {
	credential, err := a.credential(ctx, userID)
	if err != nil {
		return nil, err
	}
	targets, err := a.recentCommentTargets(ctx, userID)
	if err != nil {
		return nil, err
	}
	answered := make(map[string]bool)
	rows, err := a.Store.DB.QueryContext(ctx, `
		SELECT root_id FROM deliveries WHERE user_id=? AND kind='reply'`, userID)
	if err == nil {
		for rows.Next() {
			var root string
			if rows.Scan(&root) == nil {
				answered[root] = true
			}
		}
		rows.Close()
	}
	result := make([]map[string]any, 0)
	for _, target := range targets {
		bvid := stringFromAny(target["bvid"])
		remoteID := stringFromAny(target["remote_id"])
		comments, err := a.Bili.Comments(ctx, credential, bvid, 20)
		if err != nil {
			continue
		}
		for _, comment := range comments {
			if strconv.FormatInt(int64(numberFromAny(comment["rpid"])), 10) != remoteID {
				continue
			}
			children, _ := comment["children"].([]map[string]any)
			if children == nil {
				if rawChildren, ok := comment["children"].([]any); ok {
					for _, rawChild := range rawChildren {
						if child, ok := rawChild.(map[string]any); ok {
							children = append(children, child)
						}
					}
				}
			}
			for _, child := range children {
				rpid := strconv.FormatInt(int64(numberFromAny(child["rpid"])), 10)
				if rpid == "0" || answered[rpid] {
					continue
				}
				result = append(result, map[string]any{
					"bvid": bvid, "parent_rpid": remoteID, "rpid": rpid,
					"root": remoteID, "author": stringFromAny(child["author"]),
					"text":             stringFromAny(child["content"]),
					"original_comment": stringFromAny(target["content"]),
				})
				if len(result) >= 20 {
					return result, nil
				}
			}
		}
	}
	return result, nil
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
