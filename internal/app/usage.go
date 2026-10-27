package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Miku0139oao/bili-scroll-agent/internal/model"
	"github.com/Miku0139oao/bili-scroll-agent/internal/store"
	"github.com/Miku0139oao/bili-scroll-agent/internal/version"
)

func (a *App) usage(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireWeb(w, r)
	if !ok {
		return
	}
	days := queryInt(r, "days", 30, 1, 365)
	payload, err := a.usageDashboard(r.Context(), identity.User.ID, days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load usage")
		return
	}
	payload["ok"] = true
	writeJSON(w, http.StatusOK, payload)
}

func (a *App) usageDashboard(ctx context.Context, userID string, days int) (map[string]any, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	rows, err := a.Store.DB.QueryContext(ctx, `
		SELECT date,app_type,provider_id,model,request_count,input_tokens,output_tokens,
			cache_read_tokens,total_cost_usd
		FROM usage_records WHERE user_id=? AND date>=? ORDER BY date`, userID, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type dailyRow struct {
		Requests  int64
		Input     int64
		Output    int64
		CacheRead int64
		Cost      float64
	}
	type providerRow struct {
		AppType   string  `json:"app_type"`
		Provider  string  `json:"provider"`
		Model     string  `json:"model"`
		Requests  int64   `json:"requests"`
		Input     int64   `json:"input_tokens"`
		Output    int64   `json:"output_tokens"`
		CacheRead int64   `json:"cache_read_tokens"`
		Cost      float64 `json:"cost_usd"`
	}
	daily := make(map[string]*dailyRow)
	providersMap := make(map[string]*providerRow)
	var totalRequests, totalInput, totalOutput, totalCache int64
	var totalCost float64
	for rows.Next() {
		var date, appType, provider, modelName string
		var requests, input, output, cacheRead int64
		var cost float64
		if err := rows.Scan(&date, &appType, &provider, &modelName, &requests,
			&input, &output, &cacheRead, &cost); err != nil {
			continue
		}
		item := daily[date]
		if item == nil {
			item = &dailyRow{}
			daily[date] = item
		}
		item.Requests += requests
		item.Input += input
		item.Output += output
		item.CacheRead += cacheRead
		item.Cost += cost
		key := appType + "\x00" + provider + "\x00" + modelName
		providerItem := providersMap[key]
		if providerItem == nil {
			providerItem = &providerRow{AppType: appType, Provider: provider, Model: modelName}
			providersMap[key] = providerItem
		}
		providerItem.Requests += requests
		providerItem.Input += input
		providerItem.Output += output
		providerItem.CacheRead += cacheRead
		providerItem.Cost += cost
		totalRequests += requests
		totalInput += input
		totalOutput += output
		totalCache += cacheRead
		totalCost += cost
	}
	dailyResult := make([]map[string]any, 0, len(daily))
	for date, item := range daily {
		dailyResult = append(dailyResult, map[string]any{
			"date": date, "requests": item.Requests, "input_tokens": item.Input,
			"output_tokens": item.Output, "cache_read_tokens": item.CacheRead,
			"cost_usd": item.Cost,
		})
	}
	sort.Slice(dailyResult, func(i, j int) bool {
		return dailyResult[i]["date"].(string) < dailyResult[j]["date"].(string)
	})
	providers := make([]*providerRow, 0, len(providersMap))
	for _, item := range providersMap {
		providers = append(providers, item)
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].Cost > providers[j].Cost })

	runRows, _ := a.Store.DB.QueryContext(ctx, `
		SELECT id,runner,status,COALESCE(finished_at,''),telemetry
		FROM runs WHERE user_id=? AND status IN ('succeeded','failed','cancelled')
		ORDER BY finished_at DESC LIMIT 50`, userID)
	runs := make([]map[string]any, 0)
	if runRows != nil {
		defer runRows.Close()
		for runRows.Next() {
			var id, runnerName, status, finished, telemetry string
			if runRows.Scan(&id, &runnerName, &status, &finished, &telemetry) != nil {
				continue
			}
			var data map[string]any
			_ = json.Unmarshal([]byte(telemetry), &data)
			usageData, _ := data["usage"].(map[string]any)
			runs = append(runs, map[string]any{
				"id": id, "runner": runnerName, "provider": stringFromAny(data["provider"]),
				"model": stringFromAny(data["model"]), "input_tokens": numberFromAny(usageData["input_tokens"]),
				"output_tokens":     numberFromAny(usageData["output_tokens"]),
				"cache_read_tokens": numberFromAny(usageData["cache_read_tokens"]),
				"cost_usd":          numberFromAny(usageData["cost_usd"]), "status": status, "finished_at": finished,
			})
		}
	}
	balanceRows, _ := a.Store.DB.QueryContext(ctx, `
		SELECT provider_id,currency,total_balance,granted_balance,topped_up_balance,
			is_available,low_balance,source,fetched_at
		FROM balance_snapshots WHERE user_id=? ORDER BY fetched_at DESC LIMIT 20`, userID)
	balances := make([]map[string]any, 0)
	seen := make(map[string]bool)
	if balanceRows != nil {
		defer balanceRows.Close()
		for balanceRows.Next() {
			var provider, currency, source, fetched string
			var totalValue, granted, topped float64
			var available, low int
			if balanceRows.Scan(&provider, &currency, &totalValue, &granted, &topped,
				&available, &low, &source, &fetched) != nil {
				continue
			}
			key := provider + "\x00" + currency
			if seen[key] {
				continue
			}
			seen[key] = true
			balances = append(balances, map[string]any{
				"provider": provider, "currency": currency, "total": totalValue,
				"granted": granted, "topped_up": topped, "available": available != 0,
				"low": low != 0, "source": source, "fetched_at": fetched,
			})
		}
	}
	cachePercent := 0.0
	inputTotal := totalInput + totalCache
	if inputTotal > 0 {
		cachePercent = float64(totalCache) / float64(inputTotal) * 100
	}
	return map[string]any{
		"days": days, "since": cutoff,
		"totals": map[string]any{
			"requests": totalRequests, "input_tokens": totalInput, "output_tokens": totalOutput,
			"cache_read_tokens": totalCache, "cost_usd": totalCost,
			"cache_hit_percent": cachePercent,
		},
		"daily": dailyResult, "providers": providers, "runs": runs, "balances": balances,
		"generated_at": store.Now(),
		"sources":      []string{"service runs", "cc-switch usage import", "DeepSeek balance dashboard import"},
	}, nil
}

type usageImportRow struct {
	Date                string  `json:"date"`
	AppType             string  `json:"app_type"`
	ProviderID          string  `json:"provider_id"`
	Model               string  `json:"model"`
	RequestCount        int64   `json:"request_count"`
	SuccessCount        int64   `json:"success_count"`
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	TotalCostUSD        float64 `json:"total_cost_usd"`
	AvgLatencyMS        float64 `json:"avg_latency_ms"`
}

func (a *App) ingestUsage(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireBearer(w, r)
	if !ok {
		return
	}
	if err := requireScope(identity, "usage:import"); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	var body struct {
		Source string           `json:"source"`
		Rows   []usageImportRow `json:"rows"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid usage payload")
		return
	}
	if body.Source == "" {
		body.Source = "cc-switch"
	}
	updated := 0
	for _, row := range body.Rows {
		if row.AppType != "codex" && row.AppType != "hermes" {
			continue
		}
		_, err := a.Store.DB.ExecContext(r.Context(), `
			INSERT INTO usage_records(id,user_id,source,app_type,date,provider_id,model,
				request_count,success_count,input_tokens,output_tokens,cache_read_tokens,
				cache_creation_tokens,total_cost_usd,avg_latency_ms,imported_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(user_id,source,date,app_type,provider_id,model) DO UPDATE SET
				request_count=excluded.request_count,success_count=excluded.success_count,
				input_tokens=excluded.input_tokens,output_tokens=excluded.output_tokens,
				cache_read_tokens=excluded.cache_read_tokens,
				cache_creation_tokens=excluded.cache_creation_tokens,
				total_cost_usd=excluded.total_cost_usd,avg_latency_ms=excluded.avg_latency_ms`,
			model.NewID(), identity.User.ID, body.Source, row.AppType, row.Date,
			row.ProviderID, row.Model, row.RequestCount, row.SuccessCount, row.InputTokens,
			row.OutputTokens, row.CacheReadTokens, row.CacheCreationTokens, row.TotalCostUSD,
			row.AvgLatencyMS, store.Now())
		if err == nil {
			updated++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "updated": updated})
}

func (a *App) refreshBalance(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireCSRF(w, r)
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

func (a *App) refreshDeepSeekBalance(ctx context.Context, userID string) (map[string]any, error) {
	baseURL := strings.TrimRight(firstNonEmptyText(os.Getenv("DEEPSEEK_BASE_URL"), "https://api.deepseek.com"), "/")
	apiKey := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	threshold := 10.0
	var encrypted sql.NullString
	var dbURL sql.NullString
	var dbThreshold sql.NullFloat64
	err := a.Store.DB.QueryRowContext(ctx, `
		SELECT base_url,api_key_enc,low_balance_threshold
		FROM provider_accounts WHERE provider_id='deepseek'`).
		Scan(&dbURL, &encrypted, &dbThreshold)
	if err == nil {
		if apiKey == "" && encrypted.Valid && encrypted.String != "" {
			raw, decryptErr := a.Cipher.DecryptBytes(encrypted.String, "provider:deepseek")
			if decryptErr != nil {
				return nil, decryptErr
			}
			apiKey = string(raw)
		}
		if os.Getenv("DEEPSEEK_BASE_URL") == "" && dbURL.Valid && dbURL.String != "" {
			baseURL = strings.TrimRight(dbURL.String, "/")
		}
		if dbThreshold.Valid {
			threshold = dbThreshold.Float64
		}
	}
	if apiKey == "" {
		return nil, fmt.Errorf("DeepSeek API key is not configured")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, fmt.Errorf("invalid DeepSeek base URL")
	}
	if parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") {
		return nil, fmt.Errorf("DeepSeek base URL must not include credentials or a path")
	}
	if parsed.Scheme == "http" && parsed.Hostname() != "127.0.0.1" &&
		parsed.Hostname() != "localhost" && parsed.Hostname() != "::1" {
		return nil, fmt.Errorf("non-HTTPS DeepSeek base URL is only allowed on loopback")
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/user/balance", nil)
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", version.UserAgent())
	response, err := (&http.Client{Timeout: 20 * time.Second}).Do(request)
	if err != nil {
		return nil, fmt.Errorf("DeepSeek network error: %w", err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if response.StatusCode == 401 {
		return nil, fmt.Errorf("DeepSeek rejected the API key (HTTP 401)")
	}
	if response.StatusCode >= 400 {
		return nil, fmt.Errorf("DeepSeek balance request failed (HTTP %d)", response.StatusCode)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("DeepSeek returned invalid JSON")
	}
	infos, _ := payload["balance_infos"].([]any)
	now := store.Now()
	balances := make([]map[string]any, 0, len(infos))
	for _, rawItem := range infos {
		item, _ := rawItem.(map[string]any)
		currency := firstNonEmptyText(stringFromAny(item["currency"]), "CNY")
		totalValue := floatFromAny(item["total_balance"])
		granted := floatFromAny(item["granted_balance"])
		topped := floatFromAny(item["topped_up_balance"])
		available := true
		if value, ok := payload["is_available"].(bool); ok {
			available = value
		}
		_, _ = a.Store.DB.ExecContext(ctx, `
			INSERT INTO balance_snapshots(id,user_id,provider_id,currency,total_balance,
				granted_balance,topped_up_balance,is_available,low_balance,source,fetched_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			model.NewID(), userID, "deepseek", currency, totalValue, granted, topped,
			boolInt(available), boolInt(totalValue < threshold), "deepseek-native", now)
		balances = append(balances, map[string]any{
			"provider": "deepseek", "currency": currency, "total": totalValue,
			"granted": granted, "topped_up": topped, "available": available,
			"low": totalValue < threshold, "source": "deepseek-native", "fetched_at": now,
		})
	}
	return map[string]any{
		"provider": "deepseek", "base_url": baseURL, "threshold": threshold, "balances": balances,
	}, nil
}

func (a *App) ingestBalance(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireBearer(w, r)
	if !ok {
		return
	}
	if err := requireScope(identity, "usage:import"); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	var body struct {
		ProviderID   string  `json:"provider_id"`
		Available    bool    `json:"available"`
		FetchedAt    string  `json:"fetched_at"`
		LowThreshold float64 `json:"low_threshold"`
		Source       string  `json:"source"`
		Balances     []struct {
			Currency string  `json:"currency"`
			Total    float64 `json:"total"`
			Granted  float64 `json:"granted"`
			ToppedUp float64 `json:"topped_up"`
			Low      bool    `json:"low"`
		} `json:"balances"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid balance payload")
		return
	}
	if body.ProviderID == "" {
		body.ProviderID = "deepseek"
	}
	fetchedAt := body.FetchedAt
	if fetchedAt == "" {
		fetchedAt = store.Now()
	} else if parsed, ok := store.ParseTime(fetchedAt); ok {
		fetchedAt = parsed.Format(time.RFC3339Nano)
	}
	inserted := 0
	for _, item := range body.Balances {
		result, err := a.Store.DB.ExecContext(r.Context(), `
			INSERT OR IGNORE INTO balance_snapshots(id,user_id,provider_id,currency,total_balance,
				granted_balance,topped_up_balance,is_available,low_balance,source,fetched_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			model.NewID(), identity.User.ID, body.ProviderID, item.Currency, item.Total,
			item.Granted, item.ToppedUp, boolInt(body.Available),
			boolInt(item.Low || item.Total < body.LowThreshold), firstNonEmptyText(body.Source, "import"), fetchedAt)
		if err == nil {
			if affected, _ := result.RowsAffected(); affected > 0 {
				inserted++
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "inserted": inserted})
}

func numberFromAny(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int64:
		return float64(typed)
	case json.Number:
		result, _ := typed.Float64()
		return result
	case string:
		result, _ := strconv.ParseFloat(typed, 64)
		return result
	default:
		return 0
	}
}

func floatFromAny(value any) float64 {
	return numberFromAny(value)
}

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
