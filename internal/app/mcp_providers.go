package app

import (
	"context"
	"strings"
)

// mcpRefreshBalances implements bili_balance_refresh.
//
// With no arguments it keeps the pre-0.3 behaviour: refresh DeepSeek when it is
// configured, otherwise the first enabled provider, and return the flat shape.
func (a *App) mcpRefreshBalances(ctx context.Context, userID string, args map[string]any) (map[string]any, error) {
	if name := strings.TrimSpace(stringArg(args, "provider")); name != "" {
		return a.refreshProviderBalance(ctx, userID, name)
	}
	if all, ok := args["all"].(bool); ok && all {
		results := a.refreshAllProviderBalances(ctx, userID)
		return map[string]any{"ok": true, "providers": results}, nil
	}
	id, err := a.defaultBalanceProvider(ctx)
	if err != nil {
		return nil, err
	}
	return a.refreshProviderBalance(ctx, userID, id)
}

// mcpProviders lists provider state without exposing base URLs, scripts, or
// keys: a run token is handed to a third-party model, which does not need to
// learn the deployment's internal endpoints.
func (a *App) mcpProviders(ctx context.Context) (map[string]any, error) {
	records, err := a.listProviders(ctx)
	if err != nil {
		return nil, err
	}
	balances, _ := a.savedBalanceRows(ctx)
	byProvider := map[string][]map[string]any{}
	for _, balance := range balances {
		id := stringFromAny(balance["provider"])
		byProvider[id] = append(byProvider[id], balance)
	}
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		if !record.Enabled {
			continue
		}
		items = append(items, map[string]any{
			"provider_id":      record.ProviderID,
			"display_name":     record.DisplayName,
			"kind":             record.Kind,
			"currency":         record.Currency,
			"has_api_key":      record.HasAPIKey,
			"supports_balance": a.Providers.SupportsBalance(record.Kind),
			"balances":         byProvider[record.ProviderID],
		})
	}
	return map[string]any{"ok": true, "providers": items}, nil
}
