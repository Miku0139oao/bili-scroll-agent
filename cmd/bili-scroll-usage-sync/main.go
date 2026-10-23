package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type usageRow struct {
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

func main() {
	home, _ := os.UserHomeDir()
	server := flag.String("server", env("BILI_SCROLL_SERVER", "http://127.0.0.1:8788"), "Bili Scroll server")
	token := flag.String("token", os.Getenv("BILI_SCROLL_TOKEN"), "device token")
	dbPath := flag.String("cc-switch-db", filepath.Join(home, ".cc-switch", "cc-switch.db"), "cc-switch database")
	days := flag.Int("days", 30, "days to import")
	watch := flag.Bool("watch", false, "keep syncing")
	interval := flag.Duration("interval", 60*time.Second, "watch interval")
	flag.Parse()
	if *token == "" {
		fmt.Fprintln(os.Stderr, "Missing BILI_SCROLL_TOKEN. Create a device token in the Bili Scroll WebUI.")
		os.Exit(2)
	}
	if *days < 1 || *days > 365 {
		fmt.Fprintln(os.Stderr, "--days must be between 1 and 365")
		os.Exit(2)
	}
	if *interval < 10*time.Second {
		*interval = 10 * time.Second
	}
	for {
		rows, err := readRows(*dbPath, *days)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Sync failed:", err)
			if !*watch {
				os.Exit(1)
			}
		} else if err := upload(*server, *token, rows); err != nil {
			fmt.Fprintln(os.Stderr, "Sync failed:", err)
			if !*watch {
				os.Exit(1)
			}
		} else {
			fmt.Printf("cc-switch usage: %d rows\n", len(rows))
		}
		if !*watch {
			return
		}
		time.Sleep(*interval)
	}
}

func readRows(path string, days int) ([]usageRow, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(absolute); err != nil {
		return nil, fmt.Errorf("cc-switch database not found: %s", absolute)
	}
	dsn := "file:" + filepath.ToSlash(absolute) + "?mode=ro&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	since := time.Now().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	rows, err := db.Query(`
		SELECT date,app_type,COALESCE(provider_id,''),COALESCE(model,''),
			COALESCE(request_count,0),COALESCE(success_count,0),COALESCE(input_tokens,0),
			COALESCE(output_tokens,0),COALESCE(cache_read_tokens,0),
			COALESCE(cache_creation_tokens,0),COALESCE(total_cost_usd,0),
			COALESCE(avg_latency_ms,0)
		FROM usage_daily_rollups
		WHERE date>=? AND app_type IN ('codex','hermes')
		ORDER BY date`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]usageRow, 0)
	for rows.Next() {
		var row usageRow
		if err := rows.Scan(
			&row.Date, &row.AppType, &row.ProviderID, &row.Model, &row.RequestCount,
			&row.SuccessCount, &row.InputTokens, &row.OutputTokens, &row.CacheReadTokens,
			&row.CacheCreationTokens, &row.TotalCostUSD, &row.AvgLatencyMS,
		); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func upload(server, token string, rows []usageRow) error {
	payload, _ := json.Marshal(map[string]any{"source": "cc-switch", "rows": rows})
	request, err := http.NewRequest(http.MethodPost,
		trimRight(server)+"/api/ingest/usage", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "bili-scroll-go-usage-sync/0.2")
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", response.StatusCode, string(raw))
	}
	return nil
}

func trimRight(value string) string {
	for len(value) > 0 && value[len(value)-1] == '/' {
		value = value[:len(value)-1]
	}
	return value
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
