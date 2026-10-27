package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/Miku0139oao/bili-scroll-agent/internal/model"
	"github.com/Miku0139oao/bili-scroll-agent/internal/provider"
	"github.com/Miku0139oao/bili-scroll-agent/internal/store"
)

// providerIDPattern constrains provider ids because they appear in route paths
// and in the encryption AAD ("provider:<id>").
var providerIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// errProviderNotFound reports a missing provider account.
var errProviderNotFound = errors.New("provider is not configured")

// reservedProviderIDs collide with /api/admin/provider-kinds or with the
// sub-paths below /api/admin/providers/{id}.
var reservedProviderIDs = map[string]bool{
	"kinds": true, "check": true, "validate": true, "script": true,
}

// providerRecord is the persisted shape of a provider account.
type providerRecord struct {
	ProviderID          string
	Kind                string
	DisplayName         string
	BaseURL             string
	AuthStyle           string
	Currency            string
	LowBalanceThreshold float64
	Enabled             bool
	AllowHosts          []string
	Settings            map[string]any
	Script              string
	ScriptVersion       int
	HasAPIKey           bool
	APIKey              string // decrypted; never persisted or returned
	CreatedAt           string
	UpdatedAt           string
}

func validProviderID(id string) bool {
	return providerIDPattern.MatchString(id) && !reservedProviderIDs[id]
}

// providerEnvKey maps a provider id to its environment variable infix.
func providerEnvKey(id string) string {
	upper := strings.ToUpper(id)
	return strings.NewReplacer("-", "_", ".", "_").Replace(upper)
}

// loadProvider reads one provider row and decrypts its key.
func (a *App) loadProvider(ctx context.Context, id string) (providerRecord, error) {
	var record providerRecord
	var encrypted, allowHosts, settings sql.NullString
	var enabled, version int
	err := a.Store.DB.QueryRowContext(ctx, `
		SELECT provider_id,kind,display_name,base_url,auth_style,currency,
		       api_key_enc,low_balance_threshold,enabled,allow_hosts,settings,
		       script,script_version,created_at,updated_at
		FROM provider_accounts WHERE provider_id=?`, id).
		Scan(&record.ProviderID, &record.Kind, &record.DisplayName, &record.BaseURL,
			&record.AuthStyle, &record.Currency, &encrypted, &record.LowBalanceThreshold,
			&enabled, &allowHosts, &settings, &record.Script, &version,
			&record.CreatedAt, &record.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return providerRecord{}, fmt.Errorf("%w: %s", errProviderNotFound, id)
	}
	if err != nil {
		return providerRecord{}, err
	}
	record.Enabled = enabled != 0
	record.ScriptVersion = version
	record.AllowHosts = decodeStringList(allowHosts.String)
	record.Settings = store.DecodeJSON[map[string]any](settings.String)
	if record.Kind == "" {
		record.Kind = provider.KindDeepSeek
	}
	record.HasAPIKey = encrypted.Valid && encrypted.String != ""
	if record.HasAPIKey {
		raw, err := a.Cipher.DecryptBytes(encrypted.String, "provider:"+id)
		if err != nil {
			return providerRecord{}, fmt.Errorf("could not decrypt provider %q key: %w", id, err)
		}
		record.APIKey = string(raw)
	}
	// Environment overrides win, matching the previous DEEPSEEK_* precedence.
	envKey := providerEnvKey(id)
	if value := strings.TrimSpace(os.Getenv("BILI_SCROLL_PROVIDER_" + envKey + "_API_KEY")); value != "" {
		record.APIKey = value
		record.HasAPIKey = true
	}
	if value := strings.TrimSpace(os.Getenv("BILI_SCROLL_PROVIDER_" + envKey + "_BASE_URL")); value != "" {
		record.BaseURL = value
	}
	if id == provider.KindDeepSeek {
		if value := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")); value != "" && record.APIKey == "" {
			record.APIKey = value
			record.HasAPIKey = true
		}
		if value := strings.TrimSpace(os.Getenv("DEEPSEEK_BASE_URL")); value != "" {
			record.BaseURL = value
		}
	}
	return record, nil
}

// providerConfig adapts a record to the provider package contract.
func (r providerRecord) providerConfig() provider.Config {
	return provider.Config{
		ProviderID: r.ProviderID,
		Kind:       r.Kind,
		BaseURL:    r.BaseURL,
		APIKey:     r.APIKey,
		Currency:   r.Currency,
		AllowHosts: r.AllowHosts,
		Settings:   r.Settings,
		Script:     r.Script,
	}
}

// listProviders returns every configured provider, ordered by id.
func (a *App) listProviders(ctx context.Context) ([]providerRecord, error) {
	rows, err := a.Store.DB.QueryContext(ctx, `
		SELECT provider_id FROM provider_accounts ORDER BY provider_id`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	result := make([]providerRecord, 0, len(ids))
	for _, id := range ids {
		record, err := a.loadProvider(ctx, id)
		if err != nil {
			a.Logger.Warn("skip provider", "provider", id, "error", err)
			continue
		}
		result = append(result, record)
	}
	return result, nil
}

// ensureDeepSeekProvider materialises the implicit DeepSeek row so the list and
// the legacy envelope always have something to report.
func (a *App) ensureDeepSeekProvider(ctx context.Context) error {
	var count int
	if err := a.Store.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM provider_accounts`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	baseURL := strings.TrimSpace(os.Getenv("DEEPSEEK_BASE_URL"))
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}
	now := store.Now()
	_, err := a.Store.DB.ExecContext(ctx, `
		INSERT INTO provider_accounts(provider_id,kind,display_name,base_url,auth_style,
			currency,low_balance_threshold,enabled,allow_hosts,settings,created_at,updated_at)
		VALUES('deepseek','deepseek','DeepSeek',?,'bearer','CNY',10,1,'[]','{}',?,?)
		ON CONFLICT(provider_id) DO NOTHING`, strings.TrimRight(baseURL, "/"), now, now)
	return err
}

// refreshProviderBalance fetches one provider's balance, persists the global
// current value plus a per-user history row, and returns the response payload.
func (a *App) refreshProviderBalance(
	ctx context.Context,
	userID, providerID string,
) (map[string]any, error) {
	record, err := a.loadProvider(ctx, providerID)
	if err != nil {
		return nil, err
	}
	if !record.Enabled {
		return nil, fmt.Errorf("%w: %s", provider.ErrDisabled, providerID)
	}
	cfg := record.providerConfig()
	balances, err := a.Providers.Fetch(ctx, cfg)
	if err != nil {
		if errors.Is(err, provider.ErrNoBalanceAPI) {
			return map[string]any{
				"provider": providerID, "supports_balance": false,
				"balances": []map[string]any{}, "threshold": record.LowBalanceThreshold,
				"base_url": record.BaseURL,
			}, err
		}
		return nil, err
	}
	now := store.Now()
	source := record.Kind + "-native"
	result := make([]map[string]any, 0, len(balances))
	for _, balance := range balances {
		low := balance.Total < record.LowBalanceThreshold
		_, _ = a.Store.DB.ExecContext(ctx, `
			INSERT INTO provider_balances(provider_id,currency,total_balance,granted_balance,
				topped_up_balance,is_available,low_balance,source,fetched_at)
			VALUES(?,?,?,?,?,?,?,?,?)
			ON CONFLICT(provider_id,currency) DO UPDATE SET
				total_balance=excluded.total_balance,
				granted_balance=excluded.granted_balance,
				topped_up_balance=excluded.topped_up_balance,
				is_available=excluded.is_available,
				low_balance=excluded.low_balance,
				source=excluded.source,fetched_at=excluded.fetched_at`,
			providerID, balance.Currency, balance.Total, balance.Granted,
			balance.ToppedUp, boolInt(balance.Available), boolInt(low), source, now)
		_, _ = a.Store.DB.ExecContext(ctx, `
			INSERT OR IGNORE INTO balance_snapshots(id,user_id,provider_id,currency,
				total_balance,granted_balance,topped_up_balance,is_available,low_balance,
				source,fetched_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			model.NewID(), userID, providerID, balance.Currency, balance.Total,
			balance.Granted, balance.ToppedUp, boolInt(balance.Available),
			boolInt(low), source, now)
		result = append(result, map[string]any{
			"provider": providerID, "currency": balance.Currency, "total": balance.Total,
			"granted": balance.Granted, "topped_up": balance.ToppedUp,
			"available": balance.Available, "low": low, "source": source, "fetched_at": now,
		})
	}
	return map[string]any{
		"provider": providerID, "base_url": record.BaseURL,
		"threshold": record.LowBalanceThreshold, "balances": result,
		"supports_balance": true,
	}, nil
}

// refreshAllProviderBalances refreshes every enabled provider and reports
// per-provider errors instead of failing the whole batch.
func (a *App) refreshAllProviderBalances(ctx context.Context, userID string) []map[string]any {
	records, err := a.listProviders(ctx)
	if err != nil {
		return []map[string]any{{"error": err.Error()}}
	}
	results := make([]map[string]any, 0, len(records))
	for _, record := range records {
		if !record.Enabled {
			continue
		}
		payload, err := a.refreshProviderBalance(ctx, userID, record.ProviderID)
		item := map[string]any{
			"provider": record.ProviderID, "display_name": record.DisplayName,
			"balances": []map[string]any{},
		}
		if payload != nil {
			for key, value := range payload {
				if key != "provider" {
					item[key] = value
				}
			}
		}
		if err != nil {
			item["error"] = err.Error()
			item["supports_balance"] = !errors.Is(err, provider.ErrNoBalanceAPI)
		}
		results = append(results, item)
	}
	return results
}

// defaultBalanceProvider picks the provider a legacy caller means when it does
// not name one: DeepSeek when configured, otherwise the first enabled provider.
func (a *App) defaultBalanceProvider(ctx context.Context) (string, error) {
	var id string
	err := a.Store.DB.QueryRowContext(ctx, `
		SELECT provider_id FROM provider_accounts
		WHERE enabled=1 ORDER BY (provider_id='deepseek') DESC, provider_id LIMIT 1`).Scan(&id)
	if err != nil {
		return "", errors.New("no provider is configured")
	}
	return id, nil
}

// savedBalanceRows returns the canonical current balances per provider.
func (a *App) savedBalanceRows(ctx context.Context) ([]map[string]any, error) {
	rows, err := a.Store.DB.QueryContext(ctx, `
		SELECT provider_id,currency,total_balance,granted_balance,topped_up_balance,
		       is_available,low_balance,source,fetched_at
		FROM provider_balances ORDER BY provider_id,currency`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]map[string]any, 0)
	for rows.Next() {
		var providerID, currency, source, fetchedAt string
		var total, granted, toppedUp float64
		var available, low int
		if err := rows.Scan(&providerID, &currency, &total, &granted, &toppedUp,
			&available, &low, &source, &fetchedAt); err != nil {
			continue
		}
		result = append(result, map[string]any{
			"provider": providerID, "currency": currency, "total": total,
			"granted": granted, "topped_up": toppedUp, "available": available != 0,
			"low": low != 0, "source": source, "fetched_at": fetchedAt,
		})
	}
	return result, rows.Err()
}

func decodeStringList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	list := store.DecodeJSON[[]string](raw)
	result := make([]string, 0, len(list))
	for _, item := range list {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	sort.Strings(result)
	return result
}
