package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// deepSeekAdapter ports the original GET /user/balance integration.
type deepSeekAdapter struct{}

func (deepSeekAdapter) Kind() string          { return KindDeepSeek }
func (deepSeekAdapter) SupportsBalance() bool { return true }

func (deepSeekAdapter) Fetch(ctx context.Context, cfg Config, env *FetchEnv) ([]Balance, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("API key is not configured")
	}
	baseURL := defaultBaseURL(cfg.BaseURL, "https://api.deepseek.com")
	if _, err := ValidateBaseURL(baseURL); err != nil {
		return nil, err
	}
	response, err := env.HTTP(ctx, fetchOptions{
		Method:  http.MethodGet,
		URL:     baseURL + "/user/balance",
		Headers: bearer(cfg.APIKey),
	})
	if err != nil {
		return nil, err
	}
	return parseDeepSeekBalance(response)
}

func parseDeepSeekBalance(response HTTPResponse) ([]Balance, error) {
	switch {
	case response.Status == http.StatusUnauthorized:
		return nil, errors.New("the provider rejected the API key (HTTP 401)")
	case response.Status >= 400:
		return nil, fmt.Errorf("balance request failed (HTTP %d)", response.Status)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
		return nil, errors.New("the provider returned invalid JSON")
	}
	available := true
	if value, ok := payload["is_available"].(bool); ok {
		available = value
	}
	infos, _ := payload["balance_infos"].([]any)
	result := make([]Balance, 0, len(infos))
	for _, rawItem := range infos {
		item, _ := rawItem.(map[string]any)
		currency := strings.TrimSpace(stringValue(item["currency"]))
		if currency == "" {
			currency = "CNY"
		}
		result = append(result, Balance{
			Currency:  currency,
			Total:     floatValue(item["total_balance"]),
			Granted:   floatValue(item["granted_balance"]),
			ToppedUp:  floatValue(item["topped_up_balance"]),
			Available: available,
		})
	}
	return result, nil
}

// openRouterAdapter reads GET /credits. OpenRouter reports purchased credit and
// lifetime usage, so the remaining balance is the difference.
type openRouterAdapter struct{}

func (openRouterAdapter) Kind() string          { return KindOpenRouter }
func (openRouterAdapter) SupportsBalance() bool { return true }

func (openRouterAdapter) Fetch(ctx context.Context, cfg Config, env *FetchEnv) ([]Balance, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("API key is not configured")
	}
	baseURL := defaultBaseURL(cfg.BaseURL, "https://openrouter.ai/api/v1")
	if _, err := ValidateBaseURL(baseURL); err != nil {
		return nil, err
	}
	response, err := env.HTTP(ctx, fetchOptions{
		Method:  http.MethodGet,
		URL:     baseURL + "/credits",
		Headers: bearer(cfg.APIKey),
	})
	if err != nil {
		return nil, err
	}
	return parseOpenRouterCredits(response)
}

func parseOpenRouterCredits(response HTTPResponse) ([]Balance, error) {
	switch {
	case response.Status == http.StatusUnauthorized:
		return nil, errors.New("the provider rejected the API key (HTTP 401)")
	case response.Status >= 400:
		return nil, fmt.Errorf("credits request failed (HTTP %d)", response.Status)
	}
	var payload struct {
		Data struct {
			TotalCredits float64 `json:"total_credits"`
			TotalUsage   float64 `json:"total_usage"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
		return nil, errors.New("the provider returned invalid JSON")
	}
	remaining := payload.Data.TotalCredits - payload.Data.TotalUsage
	if remaining < 0 {
		remaining = 0
	}
	return []Balance{{
		Currency:  "USD",
		Total:     remaining,
		Granted:   0,
		ToppedUp:  payload.Data.TotalCredits,
		Available: true,
	}}, nil
}

// noBalanceAdapter represents providers with no public balance endpoint.
type noBalanceAdapter struct{ kind string }

func (a noBalanceAdapter) Kind() string          { return a.kind }
func (a noBalanceAdapter) SupportsBalance() bool { return false }
func (noBalanceAdapter) Fetch(context.Context, Config, *FetchEnv) ([]Balance, error) {
	return nil, ErrNoBalanceAPI
}

func defaultBaseURL(configured, fallback string) string {
	if value := strings.TrimRight(strings.TrimSpace(configured), "/"); value != "" {
		return value
	}
	return fallback
}

func bearer(apiKey string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + strings.TrimSpace(apiKey)}
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func floatValue(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		result, _ := typed.Float64()
		return result
	case string:
		result, _ := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return result
	default:
		return 0
	}
}
