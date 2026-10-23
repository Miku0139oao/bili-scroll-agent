package bilibili

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Miku0139oao/bili-scroll-agent/internal/model"
)

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/124.0 Safari/537.36"

const (
	qrGenerateURL = "https://passport.bilibili.com/x/passport-login/web/qrcode/generate?source=main-fe-header"
	qrPollURL     = "https://passport.bilibili.com/x/passport-login/web/qrcode/poll?qrcode_key=%s&source=main-fe-header"
)

type Credential struct {
	SESSDATA   string `json:"sessdata"`
	BiliJCT    string `json:"bili_jct"`
	Buvid3     string `json:"buvid3"`
	DedeUserID string `json:"dedeuserid"`
}

func (c Credential) Complete() bool {
	return c.SESSDATA != "" && c.BiliJCT != "" && c.DedeUserID != ""
}

func (c Credential) Cookie() string {
	return "SESSDATA=" + c.SESSDATA + "; bili_jct=" + c.BiliJCT + "; buvid3=" + c.Buvid3
}

type Client struct {
	HTTP       *http.Client
	mu         sync.Mutex
	last       time.Time
	anonMu     sync.Mutex
	anonReady  bool
	anonCookie string
	mixinKey   string
}

func New() *Client {
	return &Client{HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) StartLogin(ctx context.Context) (string, string, []byte, error) {
	jar, _ := cookiejar.New(nil)
	local := &http.Client{Timeout: 20 * time.Second, Jar: jar}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, qrGenerateURL, nil)
	request.Header.Set("User-Agent", userAgent)
	response, err := local.Do(request)
	if err != nil {
		return "", "", nil, err
	}
	defer response.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", "", nil, err
	}
	data := object(payload["data"])
	key := text(data["qrcode_key"])
	loginURL := text(data["url"])
	if key == "" || loginURL == "" {
		return "", "", nil, errors.New("could not generate Bilibili QR code")
	}
	return key, loginURL, nil, nil
}

type LoginState struct {
	State      string
	Code       int
	Message    string
	Credential Credential
}

func (c *Client) PollLogin(ctx context.Context, key string) (LoginState, error) {
	jar, _ := cookiejar.New(nil)
	local := &http.Client{Timeout: 20 * time.Second, Jar: jar}
	target := fmt.Sprintf(qrPollURL, url.QueryEscape(key))
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	request.Header.Set("User-Agent", userAgent)
	response, err := local.Do(request)
	if err != nil {
		return LoginState{}, err
	}
	defer response.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return LoginState{}, err
	}
	data := object(payload["data"])
	code := integer(data["code"])
	switch code {
	case 86101:
		return LoginState{State: "waiting", Code: code}, nil
	case 86090:
		return LoginState{State: "scanned", Code: code}, nil
	case 86038:
		return LoginState{State: "expired", Code: code}, nil
	case 0:
		credential := Credential{}
		parsed, _ := url.Parse("https://www.bilibili.com")
		for _, cookie := range jar.Cookies(parsed) {
			switch cookie.Name {
			case "SESSDATA":
				credential.SESSDATA = cookie.Value
			case "bili_jct":
				credential.BiliJCT = cookie.Value
			case "buvid3":
				credential.Buvid3 = cookie.Value
			case "DedeUserID":
				credential.DedeUserID = cookie.Value
			}
		}
		if credential.Buvid3 == "" {
			credential.Buvid3 = c.fetchBuvid3(ctx)
		}
		if !credential.Complete() {
			return LoginState{State: "unknown", Message: "login completed but cookies were missing"}, nil
		}
		return LoginState{State: "ok", Code: 0, Credential: credential}, nil
	default:
		return LoginState{State: "unknown", Code: code, Message: text(payload["message"])}, nil
	}
}

func (c *Client) fetchBuvid3(ctx context.Context) string {
	payload, _ := c.getPublicJSON(ctx, "https://api.bilibili.com/x/frontend/finger/spi", "")
	return text(object(payload["data"])["b_3"])
}

func (c *Client) Search(
	ctx context.Context,
	keyword string,
	num int,
	order string,
	days int,
) ([]model.Video, error) {
	c.throttle(ctx)
	if err := c.prepareAnonymous(ctx); err != nil {
		return nil, err
	}
	if num < 1 {
		num = 10
	}
	if num > 50 {
		num = 50
	}
	orderValue := map[string]string{"pubdate": "pubdate", "click": "click", "dm": "dm", "totalrank": "totalrank"}[order]
	if orderValue == "" {
		orderValue = "pubdate"
	}
	query := url.Values{
		"search_type": {"video"},
		"keyword":     {keyword},
		"page":        {"1"},
		"order":       {orderValue},
	}
	signed := c.signWBI(query)
	payload, err := c.getPublicJSON(ctx,
		"https://api.bilibili.com/x/web-interface/wbi/search/type?"+signed, c.anonCookie)
	if err != nil {
		return nil, err
	}
	if code := integer(payload["code"]); code != 0 {
		return nil, fmt.Errorf("Bilibili search failed: %s", text(payload["message"]))
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	items := slice(object(payload["data"])["result"])
	result := make([]model.Video, 0, num)
	tagPattern := regexp.MustCompile(`<[^>]+>`)
	for _, raw := range items {
		item := object(raw)
		pubdate := timestamp(item["pubdate"])
		if pubdate.IsZero() {
			pubdate = timestamp(item["senddate"])
		}
		if days > 0 && !pubdate.IsZero() && pubdate.Before(cutoff) {
			continue
		}
		bvid := text(item["bvid"])
		result = append(result, model.Video{
			BVID:        bvid,
			AID:         int64(integer(item["aid"])),
			Title:       html.UnescapeString(tagPattern.ReplaceAllString(text(item["title"]), "")),
			Author:      text(item["author"]),
			Play:        int64(integer(item["play"])),
			Review:      int64(integer(item["review"])),
			Danmaku:     int64(integer(item["video_review"])),
			Duration:    item["duration"],
			Description: truncate(text(item["description"]), 300),
			PubDate:     timeString(pubdate),
			Cover:       firstText(item["pic"], item["cover"]),
			URL:         "https://www.bilibili.com/video/" + bvid,
		})
		if len(result) >= num {
			break
		}
	}
	return result, nil
}

var mixinKeyEncTab = []int{
	46, 47, 18, 2, 53, 8, 23, 32, 15, 50, 10, 31, 58, 3, 45, 35,
	27, 43, 5, 49, 33, 9, 42, 19, 29, 28, 14, 39, 12, 38, 41, 13,
	37, 48, 7, 16, 24, 55, 40, 61, 26, 17, 0, 1, 60, 51, 30, 4,
	22, 25, 54, 21, 56, 59, 6, 63, 57, 62, 11, 36, 20, 34, 44, 52,
}

func (c *Client) prepareAnonymous(ctx context.Context) error {
	c.anonMu.Lock()
	defer c.anonMu.Unlock()
	if c.anonReady && c.mixinKey != "" && c.anonCookie != "" {
		return nil
	}
	spi, err := c.getPublicJSON(ctx, "https://api.bilibili.com/x/frontend/finger/spi", "")
	if err != nil {
		return err
	}
	spiData := object(spi["data"])
	buvid3 := text(spiData["b_3"])
	buvid4 := text(spiData["b_4"])
	if buvid3 == "" {
		return errors.New("Bilibili did not issue an anonymous buvid3 cookie")
	}
	c.anonCookie = "buvid3=" + buvid3
	if buvid4 != "" {
		c.anonCookie += "; buvid4=" + buvid4
	}
	nav, err := c.getJSON(ctx, "https://api.bilibili.com/x/web-interface/nav",
		c.anonCookie, "https://www.bilibili.com/")
	if err != nil {
		return err
	}
	wbi := object(object(nav["data"])["wbi_img"])
	imgKey := filenameWithoutExtension(text(wbi["img_url"]))
	subKey := filenameWithoutExtension(text(wbi["sub_url"]))
	if len(imgKey)+len(subKey) < 64 {
		return errors.New("Bilibili WBI keys are unavailable")
	}
	raw := []byte(imgKey + subKey)
	var builder strings.Builder
	for _, index := range mixinKeyEncTab {
		builder.WriteByte(raw[index])
	}
	c.mixinKey = builder.String()[:32]
	c.anonReady = true
	return nil
}

func (c *Client) signWBI(query url.Values) string {
	query.Set("wts", strconv.FormatInt(time.Now().Unix(), 10))
	keys := make([]string, 0, len(query))
	for key := range query {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := strings.Map(func(char rune) rune {
			if strings.ContainsRune("!'()*", char) {
				return -1
			}
			return char
		}, query.Get(key))
		parts = append(parts, url.QueryEscape(key)+"="+url.QueryEscape(value))
	}
	base := strings.Join(parts, "&")
	sum := md5.Sum([]byte(base + c.mixinKey))
	return base + "&w_rid=" + fmt.Sprintf("%x", sum)
}

func filenameWithoutExtension(value string) string {
	pathValue := strings.Split(value, "?")[0]
	name := pathValue[strings.LastIndex(pathValue, "/")+1:]
	if index := strings.LastIndex(name, "."); index >= 0 {
		name = name[:index]
	}
	return name
}

func (c *Client) VideoInfo(ctx context.Context, credential Credential, bvid string) (map[string]any, error) {
	c.throttle(ctx)
	payload, err := c.getJSON(ctx,
		"https://api.bilibili.com/x/web-interface/view?bvid="+url.QueryEscape(bvid),
		credential.Cookie(), "https://www.bilibili.com/video/"+bvid)
	if err != nil {
		return nil, err
	}
	if integer(payload["code"]) != 0 {
		return nil, fmt.Errorf("Bilibili video lookup failed: %s", text(payload["message"]))
	}
	return object(payload["data"]), nil
}

func (c *Client) VideoSummary(ctx context.Context, credential Credential, bvid string) (model.Video, error) {
	data, err := c.VideoInfo(ctx, credential, bvid)
	if err != nil {
		return model.Video{}, err
	}
	owner := object(data["owner"])
	stat := object(data["stat"])
	return model.Video{
		BVID:        bvid,
		AID:         int64(integer(data["aid"])),
		Title:       text(data["title"]),
		Author:      text(owner["name"]),
		URL:         "https://www.bilibili.com/video/" + bvid,
		PubDate:     timeString(timestamp(data["pubdate"])),
		Description: text(data["desc"]),
		Cover:       text(data["pic"]),
		Duration:    data["duration"],
		Play:        int64(integer(stat["view"])),
		Review:      int64(integer(stat["reply"])),
		Danmaku:     int64(integer(stat["danmaku"])),
	}, nil
}

func (c *Client) Subtitle(ctx context.Context, credential Credential, bvid string) (map[string]any, error) {
	c.throttle(ctx)
	info, err := c.VideoInfo(ctx, credential, bvid)
	if err != nil {
		return nil, err
	}
	cid := integer(info["cid"])
	if cid == 0 {
		pages := slice(info["pages"])
		if len(pages) > 0 {
			cid = integer(object(pages[0])["cid"])
		}
	}
	if cid == 0 {
		return map[string]any{"bvid": bvid, "empty": true, "message": "missing cid"}, nil
	}
	query := url.Values{"bvid": {bvid}, "cid": {strconv.Itoa(cid)}}
	payload, err := c.getJSON(ctx,
		"https://api.bilibili.com/x/player/v2?"+query.Encode(),
		credential.Cookie(), "https://www.bilibili.com/video/"+bvid)
	if err != nil {
		return nil, err
	}
	if integer(payload["code"]) != 0 {
		return nil, fmt.Errorf("Bilibili subtitle lookup failed: %s", text(payload["message"]))
	}
	subtitle := object(object(payload["data"])["subtitle"])
	subtitles := slice(subtitle["subtitles"])
	if len(subtitles) == 0 {
		return map[string]any{"bvid": bvid, "empty": true, "message": "该视频没有字幕"}, nil
	}
	chosen := object(subtitles[0])
	for _, raw := range subtitles {
		item := object(raw)
		language := text(item["lan"])
		if language == "ai-zh" || language == "zh-CN" || language == "zh" {
			chosen = item
			break
		}
	}
	subtitleURL := text(chosen["subtitle_url"])
	if strings.HasPrefix(subtitleURL, "//") {
		subtitleURL = "https:" + subtitleURL
	}
	subtitlePayload, err := c.getPublicJSON(ctx, subtitleURL, credential.Cookie())
	if err != nil {
		return nil, err
	}
	parts := make([]string, 0)
	for _, raw := range slice(subtitlePayload["body"]) {
		value := strings.TrimSpace(text(object(raw)["content"]))
		if value != "" {
			parts = append(parts, value)
		}
	}
	content := strings.Join(parts, "\n")
	return map[string]any{
		"bvid": bvid, "title": text(info["title"]), "language": text(chosen["lan_doc"]),
		"segments": len(parts), "text": content, "empty": content == "" || subtitleByline(content),
	}, nil
}

func (c *Client) AudioURL(ctx context.Context, credential Credential, bvid string) (string, error) {
	info, err := c.VideoInfo(ctx, credential, bvid)
	if err != nil {
		return "", err
	}
	cid := integer(info["cid"])
	if cid == 0 {
		pages := slice(info["pages"])
		if len(pages) > 0 {
			cid = integer(object(pages[0])["cid"])
		}
	}
	if cid == 0 {
		return "", errors.New("video has no cid")
	}
	query := url.Values{
		"bvid": {bvid}, "cid": {strconv.Itoa(cid)}, "fnval": {"16"}, "fourk": {"1"},
	}
	payload, err := c.getJSON(ctx,
		"https://api.bilibili.com/x/player/playurl?"+query.Encode(),
		credential.Cookie(), "https://www.bilibili.com/video/"+bvid)
	if err != nil {
		return "", err
	}
	if integer(payload["code"]) != 0 {
		return "", fmt.Errorf("Bilibili audio lookup failed: %s", text(payload["message"]))
	}
	audio := slice(object(object(payload["data"])["dash"])["audio"])
	if len(audio) == 0 {
		return "", errors.New("no audio stream available")
	}
	return firstText(object(audio[0])["baseUrl"], object(audio[0])["base_url"]), nil
}

func (c *Client) Comments(
	ctx context.Context,
	credential Credential,
	bvid string,
	num int,
) ([]map[string]any, error) {
	c.throttle(ctx)
	info, err := c.VideoInfo(ctx, credential, bvid)
	if err != nil {
		return nil, err
	}
	aid := integer(info["aid"])
	if num < 1 {
		num = 30
	}
	if num > 100 {
		num = 100
	}
	query := url.Values{
		"type": {"1"}, "oid": {strconv.Itoa(aid)}, "pn": {"1"},
		"ps": {strconv.Itoa(num)}, "sort": {"2"},
	}
	payload, err := c.getJSON(ctx,
		"https://api.bilibili.com/x/v2/reply?"+query.Encode(),
		credential.Cookie(), "https://www.bilibili.com/video/"+bvid)
	if err != nil {
		return nil, err
	}
	if integer(payload["code"]) != 0 {
		return nil, fmt.Errorf("Bilibili comments failed: %s", text(payload["message"]))
	}
	data := object(payload["data"])
	result := make([]map[string]any, 0, num)
	for _, raw := range slice(data["replies"]) {
		reply := object(raw)
		member := object(reply["member"])
		content := object(reply["content"])
		children := make([]map[string]any, 0)
		for _, childRaw := range slice(reply["replies"]) {
			if len(children) >= 5 {
				break
			}
			child := object(childRaw)
			children = append(children, map[string]any{
				"rpid": child["rpid"], "author": text(object(child["member"])["uname"]),
				"content": text(object(child["content"])["message"]),
			})
		}
		result = append(result, map[string]any{
			"rpid": reply["rpid"], "author": text(member["uname"]), "uid": member["mid"],
			"content": text(content["message"]), "like": integer(reply["like"]),
			"reply_count": integer(reply["rcount"]), "children": children,
		})
		if len(result) >= num {
			break
		}
	}
	return result, nil
}

func (c *Client) FavoriteFolders(ctx context.Context, credential Credential) ([]map[string]any, error) {
	uid := credential.DedeUserID
	payload, err := c.getJSON(ctx,
		"https://api.bilibili.com/x/v3/fav/folder/created/list-all?up_mid="+url.QueryEscape(uid),
		credential.Cookie(), "https://www.bilibili.com/")
	if err != nil {
		return nil, err
	}
	if integer(payload["code"]) != 0 {
		return nil, fmt.Errorf("favorite folders failed: %s", text(payload["message"]))
	}
	rows := make([]map[string]any, 0)
	for _, raw := range slice(object(payload["data"])["list"]) {
		item := object(raw)
		rows = append(rows, map[string]any{
			"id": item["id"], "title": item["title"], "count": item["media_count"],
		})
	}
	return rows, nil
}

func (c *Client) FavoriteAdd(ctx context.Context, credential Credential, aid int64, folderID int64) error {
	form := url.Values{
		"rid": {strconv.FormatInt(aid, 10)}, "type": {"2"},
		"add_media_ids": {strconv.FormatInt(folderID, 10)},
		"csrf":          {credential.BiliJCT},
	}
	payload, err := c.postForm(ctx,
		"https://api.bilibili.com/x/v3/fav/resource/deal", credential, form, "https://www.bilibili.com/")
	if err != nil {
		return err
	}
	code := integer(payload["code"])
	if code != 0 && code != 11201 {
		return fmt.Errorf("favorite add failed: %s", text(payload["message"]))
	}
	return nil
}

func (c *Client) SendComment(
	ctx context.Context,
	credential Credential,
	aid int64,
	textValue string,
	parentID, rootID int64,
) (map[string]any, error) {
	form := url.Values{
		"oid": {strconv.FormatInt(aid, 10)}, "type": {"1"},
		"message": {strings.TrimSpace(textValue)}, "plat": {"1"},
		"csrf": {credential.BiliJCT},
	}
	if parentID > 0 {
		form.Set("parent", strconv.FormatInt(parentID, 10))
		form.Set("root", strconv.FormatInt(firstNonZero(rootID, parentID), 10))
	}
	payload, err := c.postForm(ctx,
		"https://api.bilibili.com/x/v2/reply/add", credential, form,
		"https://www.bilibili.com/")
	if err != nil {
		return nil, err
	}
	if integer(payload["code"]) != 0 {
		return nil, fmt.Errorf("comment rejected: %s", text(payload["message"]))
	}
	data := object(payload["data"])
	return map[string]any{
		"success": true, "message": "评论发送成功", "rpid": data["rpid"],
		"root": data["root"], "parent": data["parent"],
	}, nil
}

func (c *Client) SelfInfo(ctx context.Context, credential Credential) (map[string]any, error) {
	payload, err := c.getJSON(ctx,
		"https://api.bilibili.com/x/web-interface/nav", credential.Cookie(), "https://www.bilibili.com/")
	if err != nil {
		return nil, err
	}
	if integer(payload["code"]) != 0 {
		return nil, fmt.Errorf("credential check failed: %s", text(payload["message"]))
	}
	data := object(payload["data"])
	return map[string]any{
		"ok": true, "uid": data["mid"], "nickname": data["uname"], "avatar": data["face"],
	}, nil
}

func (c *Client) DMSessions(ctx context.Context, credential Credential) ([]map[string]any, error) {
	payload, err := c.getJSON(ctx,
		"https://api.vc.bilibili.com/session_svr/v1/session_svr/get_sessions?session_type=1&group_id=0&Build=0&mobi_app=web",
		credential.Cookie(), "https://message.bilibili.com/")
	if err != nil {
		return nil, err
	}
	if integer(payload["code"]) != 0 {
		return nil, fmt.Errorf("DM sessions failed: %s", text(payload["message"]))
	}
	rows := make([]map[string]any, 0)
	for _, raw := range slice(object(payload["data"])["session_list"]) {
		item := object(raw)
		last := object(item["last_msg"])
		var content map[string]any
		_ = json.Unmarshal([]byte(text(last["content"])), &content)
		rows = append(rows, map[string]any{
			"uid": integer(item["talker_id"]), "unread": integer(item["unread_count"]),
			"last_ts": integer(last["timestamp"]), "preview": truncate(firstText(content["content"], content["text"]), 80),
		})
	}
	return rows, nil
}

func (c *Client) DMMessages(
	ctx context.Context,
	credential Credential,
	talkerID int64,
	size int,
) ([]map[string]any, error) {
	if size < 1 {
		size = 10
	}
	if size > 50 {
		size = 50
	}
	target := fmt.Sprintf(
		"https://api.vc.bilibili.com/svr_sync/v1/svr_sync/fetch_session_msgs?talker_id=%d&session_type=1&size=%d&build=0&mobi_app=web",
		talkerID, size)
	payload, err := c.getJSON(ctx, target, credential.Cookie(), "https://message.bilibili.com/")
	if err != nil {
		return nil, err
	}
	if integer(payload["code"]) != 0 {
		return nil, fmt.Errorf("DM messages failed: %s", text(payload["message"]))
	}
	rows := make([]map[string]any, 0)
	messages := slice(object(payload["data"])["messages"])
	for index := len(messages) - 1; index >= 0; index-- {
		item := object(messages[index])
		var content map[string]any
		_ = json.Unmarshal([]byte(text(item["content"])), &content)
		rows = append(rows, map[string]any{
			"key": text(item["msg_key"]), "ts": integer(item["timestamp"]),
			"from_me": integer(item["sender_uid"]) == integer(credential.DedeUserID),
			"text":    truncate(firstText(content["content"], content["text"]), 500),
		})
	}
	return rows, nil
}

func (c *Client) SendDM(ctx context.Context, credential Credential, receiverUID int64, value string) (map[string]any, error) {
	content, _ := json.Marshal(map[string]any{"content": strings.TrimSpace(value)})
	form := url.Values{
		"msg[sender_uid]":    {credential.DedeUserID},
		"msg[receiver_id]":   {strconv.FormatInt(receiverUID, 10)},
		"msg[receiver_type]": {"1"},
		"msg[msg_type]":      {"1"},
		"msg[msg_status]":    {"0"},
		"msg[content]":       {string(content)},
		"msg[timestamp]":     {strconv.FormatInt(time.Now().Unix(), 10)},
		"msg[dev_id]":        {"372DB126-7C6B-4F65-A519-EE4D2CCB6279"},
		"csrf":               {credential.BiliJCT},
		"csrf_token":         {credential.BiliJCT},
	}
	payload, err := c.postForm(ctx,
		"https://api.vc.bilibili.com/web_im/v1/web_im/send_msg", credential, form,
		"https://message.bilibili.com/")
	if err != nil {
		return nil, err
	}
	if integer(payload["code"]) != 0 {
		return nil, fmt.Errorf("DM rejected: %s", text(payload["message"]))
	}
	data := object(payload["data"])
	key := text(data["msg_key"])
	if key == "" {
		return nil, errors.New("DM returned success without msg_key")
	}
	return map[string]any{"success": true, "message": "私信已投递", "msg_key": key}, nil
}

func (c *Client) throttle(ctx context.Context) {
	c.mu.Lock()
	delay := 340*time.Millisecond - time.Since(c.last)
	c.mu.Unlock()
	if delay > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
	c.mu.Lock()
	c.last = time.Now()
	c.mu.Unlock()
}

func (c *Client) getPublicJSON(ctx context.Context, target, cookie string) (map[string]any, error) {
	return c.getJSON(ctx, target, cookie, "https://www.bilibili.com/")
}

func (c *Client) getJSON(
	ctx context.Context,
	target, cookie, referer string,
) (map[string]any, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Accept", "application/json")
	if cookie != "" {
		request.Header.Set("Cookie", cookie)
	}
	if referer != "" {
		request.Header.Set("Referer", referer)
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("Bilibili returned non-JSON HTTP %d", response.StatusCode)
	}
	return payload, nil
}

func (c *Client) postForm(
	ctx context.Context,
	target string,
	credential Credential,
	form url.Values,
	referer string,
) (map[string]any, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Cookie", credential.Cookie())
	request.Header.Set("Referer", referer)
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func object(value any) map[string]any {
	if result, ok := value.(map[string]any); ok {
		return result
	}
	return map[string]any{}
}

func slice(value any) []any {
	if result, ok := value.([]any); ok {
		return result
	}
	return []any{}
}

func text(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case nil:
		return ""
	default:
		return fmt.Sprint(typed)
	}
}

func integer(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case json.Number:
		result, _ := typed.Int64()
		return int(result)
	case int:
		return typed
	case int64:
		return int(typed)
	case string:
		result, _ := strconv.Atoi(typed)
		return result
	default:
		return 0
	}
}

func firstText(values ...any) string {
	for _, value := range values {
		if result := text(value); result != "" {
			return result
		}
	}
	return ""
}

func firstNonZero(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func timestamp(value any) time.Time {
	stamp := int64(integer(value))
	if stamp <= 0 {
		return time.Time{}
	}
	return time.Unix(stamp, 0).UTC()
}

func timeString(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func subtitleByline(value string) bool {
	compact := strings.ToLower(strings.Join(strings.Fields(value), ""))
	return len(compact) < 16 && (strings.Contains(compact, "字幕") || strings.Contains(compact, "subtitle"))
}
