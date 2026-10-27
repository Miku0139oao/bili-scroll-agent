package app

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/Miku0139oao/bili-scroll-agent/internal/model"
	"github.com/Miku0139oao/bili-scroll-agent/internal/provider"
	"github.com/Miku0139oao/bili-scroll-agent/internal/store"
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
	// cc-switch imports arrive as daily rollups; runs recorded by this service
	// land in run_usage. Union both so the dashboard reflects what actually ran
	// without double counting either source.
	rows, err := a.Store.DB.QueryContext(ctx, `
		SELECT date,app_type,provider_id,model,request_count,input_tokens,output_tokens,
			cache_read_tokens,total_cost_usd
		FROM usage_records WHERE user_id=? AND date>=?
		UNION ALL
		SELECT substr(created_at,1,10) AS date,runner AS app_type,provider AS provider_id,
			model,request_count,input_tokens,output_tokens,cache_read_tokens,cost_usd
		FROM run_usage WHERE user_id=? AND substr(created_at,1,10)>=?
		ORDER BY date`, userID, cutoff, userID, cutoff)
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
	balances, _ := a.savedBalanceRows(ctx)
	catalogRows, _ := a.Store.DB.QueryContext(ctx, `
		SELECT provider_id,display_name,kind,enabled,currency
		FROM provider_accounts ORDER BY provider_id`)
	catalog := make([]map[string]any, 0)
	if catalogRows != nil {
		defer catalogRows.Close()
		for catalogRows.Next() {
			var providerID, displayName, kind, currency string
			var enabled int
			if catalogRows.Scan(&providerID, &displayName, &kind, &enabled, &currency) != nil {
				continue
			}
			catalog = append(catalog, map[string]any{
				"provider_id": providerID, "display_name": displayName, "kind": kind,
				"enabled": enabled != 0, "currency": currency,
				"supports_balance": a.Providers.SupportsBalance(kind),
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
		"generated_at":     store.Now(),
		"sources":          []string{"service runs", "cc-switch usage import", "provider balance checks"},
		"provider_catalog": catalog,
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
		if len(row.AppType) == 0 || len(row.AppType) > 32 {
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

// refreshBalance refreshes one provider, or every enabled provider when no
// provider is named.
func (a *App) refreshBalance(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireCSRF(w, r)
	if !ok {
		return
	}
	var body struct {
		Provider string `json:"provider"`
	}
	// The body is optional; an empty request refreshes everything.
	_ = decodeOptionalJSON(r, &body)
	response := map[string]any{"ok": true}
	if body.Provider != "" {
		payload, err := a.refreshProviderBalance(r.Context(), identity.User.ID, body.Provider)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		response["results"] = []map[string]any{payload}
		if body.Provider == provider.KindDeepSeek {
			// Preserve the pre-0.3 flat envelope for cached bundles.
			for key, value := range payload {
				response[key] = value
			}
		}
		writeJSON(w, http.StatusOK, response)
		return
	}
	results := a.refreshAllProviderBalances(r.Context(), identity.User.ID)
	response["results"] = results
	for _, item := range results {
		if stringFromAny(item["provider"]) == provider.KindDeepSeek && item["error"] == nil {
			for key, value := range item {
				if key != "provider" && key != "display_name" {
					response[key] = value
				}
			}
			break
		}
	}
	writeJSON(w, http.StatusOK, response)
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

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
