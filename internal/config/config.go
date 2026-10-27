package config

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Settings struct {
	AppName               string
	Environment           string
	Host                  string
	Port                  int
	PublicURL             string
	SecureCookies         bool
	DataDir               string
	DatabasePath          string
	MasterKey             string
	KeyBytes              []byte
	SessionDuration       time.Duration
	SessionIdleDuration   time.Duration
	InviteDuration        time.Duration
	RunTokenDuration      time.Duration
	CodexBin              string
	HermesBin             string
	ClaudeBin             string
	CodexHome             string
	HermesHome            string
	ClaudeHome            string
	AgentTimeout          time.Duration
	AgentOutputLimit      int64
	GlobalConcurrency     int
	ManualRunsPerDay      int
	ScheduledRunsPerDay   int
	RetentionDays         int
	LoginRatePerMinute    int
	APIRatePerMinute      int
	BindOutboundEnabled   bool
	IncomingDMPollSeconds int
	FFmpegBin             string
	WhisperBin            string
	WhisperModelPath      string
	WebDist               string
	ProviderTimeout       time.Duration
	DisableJSAdapters     bool
}

func Load() (Settings, error) {
	_ = loadDotEnv(".env")
	defaultDataDir := ".data"
	if _, set := os.LookupEnv("BILI_SCROLL_DATA_DIR"); !set {
		if pluginData := strings.TrimSpace(os.Getenv("PLUGIN_DATA")); pluginData != "" {
			defaultDataDir = pluginData
		}
	}
	s := Settings{
		AppName:               env("BILI_SCROLL_APP_NAME", "Bili Scroll Agent"),
		Environment:           env("BILI_SCROLL_ENVIRONMENT", "development"),
		Host:                  env("BILI_SCROLL_HOST", "127.0.0.1"),
		Port:                  envInt("BILI_SCROLL_PORT", 8788),
		PublicURL:             env("BILI_SCROLL_PUBLIC_URL", "http://127.0.0.1:8788"),
		SecureCookies:         envBool("BILI_SCROLL_SECURE_COOKIES", false),
		DataDir:               env("BILI_SCROLL_DATA_DIR", defaultDataDir),
		MasterKey:             strings.TrimSpace(os.Getenv("BILI_SCROLL_MASTER_KEY")),
		SessionDuration:       time.Duration(envInt("BILI_SCROLL_SESSION_HOURS", 12)) * time.Hour,
		SessionIdleDuration:   time.Duration(envInt("BILI_SCROLL_SESSION_IDLE_MINUTES", 30)) * time.Minute,
		InviteDuration:        time.Duration(envInt("BILI_SCROLL_INVITE_HOURS", 24)) * time.Hour,
		RunTokenDuration:      time.Duration(envInt("BILI_SCROLL_RUN_TOKEN_MINUTES", 45)) * time.Minute,
		CodexBin:              env("BILI_SCROLL_CODEX_BIN", "codex"),
		HermesBin:             env("BILI_SCROLL_HERMES_BIN", "hermes"),
		ClaudeBin:             env("BILI_SCROLL_CLAUDE_BIN", "claude"),
		CodexHome:             strings.TrimSpace(os.Getenv("BILI_SCROLL_CODEX_HOME")),
		HermesHome:            strings.TrimSpace(os.Getenv("BILI_SCROLL_HERMES_HOME")),
		ClaudeHome:            strings.TrimSpace(os.Getenv("BILI_SCROLL_CLAUDE_HOME")),
		AgentTimeout:          time.Duration(envInt("BILI_SCROLL_AGENT_TIMEOUT_SECONDS", 1800)) * time.Second,
		AgentOutputLimit:      int64(envInt("BILI_SCROLL_AGENT_OUTPUT_LIMIT_BYTES", 2_000_000)),
		GlobalConcurrency:     envInt("BILI_SCROLL_GLOBAL_CONCURRENCY", 1),
		ManualRunsPerDay:      envInt("BILI_SCROLL_MANUAL_RUNS_PER_DAY", 3),
		ScheduledRunsPerDay:   envInt("BILI_SCROLL_SCHEDULED_RUNS_PER_DAY", 1),
		RetentionDays:         envInt("BILI_SCROLL_RETENTION_DAYS", 365),
		LoginRatePerMinute:    envInt("BILI_SCROLL_LOGIN_RATE_LIMIT_PER_MINUTE", 10),
		APIRatePerMinute:      envInt("BILI_SCROLL_API_RATE_LIMIT_PER_MINUTE", 240),
		BindOutboundEnabled:   envBool("BILI_SCROLL_BIND_OUTBOUND_ENABLED", true),
		IncomingDMPollSeconds: envInt("BILI_SCROLL_INCOMING_DM_POLL_SECONDS", 90),
		FFmpegBin:             env("BILI_SCROLL_FFMPEG_BIN", "ffmpeg"),
		WhisperBin:            env("BILI_SCROLL_WHISPER_BIN", "whisper-cli"),
		WhisperModelPath:      strings.TrimSpace(os.Getenv("BILI_SCROLL_WHISPER_MODEL_PATH")),
		WebDist:               env("BILI_SCROLL_WEB_DIST", "web/dist"),
		ProviderTimeout:       time.Duration(envInt("BILI_SCROLL_PROVIDER_TIMEOUT_SECONDS", 20)) * time.Second,
		DisableJSAdapters:     envBool("BILI_SCROLL_DISABLE_JS_ADAPTERS", false),
	}
	if s.Port < 1 || s.Port > 65535 {
		return Settings{}, errors.New("BILI_SCROLL_PORT must be between 1 and 65535")
	}
	absData, err := filepath.Abs(s.DataDir)
	if err != nil {
		return Settings{}, err
	}
	s.DataDir = absData
	if err := os.MkdirAll(s.DataDir, 0o700); err != nil {
		return Settings{}, err
	}
	s.DatabasePath = filepath.Join(s.DataDir, "bili-scroll.db")
	s.WebDist = absPath(s.WebDist)
	key, err := ensureMasterKey(&s)
	if err != nil {
		return Settings{}, err
	}
	s.KeyBytes = key
	return s, nil
}

func (s Settings) IsProduction() bool {
	return strings.EqualFold(s.Environment, "production") || strings.EqualFold(s.Environment, "prod")
}

func (s Settings) Addr() string {
	return s.Host + ":" + strconv.Itoa(s.Port)
}

func ensureMasterKey(s *Settings) ([]byte, error) {
	if s.MasterKey != "" {
		raw, err := base64.StdEncoding.DecodeString(s.MasterKey)
		if err != nil || len(raw) != 32 {
			return nil, errors.New("BILI_SCROLL_MASTER_KEY must be base64 for exactly 32 bytes")
		}
		return raw, nil
	}
	if s.IsProduction() {
		return nil, errors.New("BILI_SCROLL_MASTER_KEY is required in production")
	}
	path := filepath.Join(s.DataDir, "master.key")
	if raw, err := os.ReadFile(path); err == nil {
		key, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
		if decodeErr != nil || len(key) != 32 {
			return nil, errors.New("invalid development master key")
		}
		return key, nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key)), 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func loadDotEnv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key != "" {
			_, exists := os.LookupEnv(key)
			if !exists {
				_ = os.Setenv(key, value)
			}
		}
	}
	return scanner.Err()
}

func env(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil {
		return fallback
	}
	return value
}

func envBool(key string, fallback bool) bool {
	value, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(key)))
	if err != nil {
		return fallback
	}
	return value
}

func absPath(value string) string {
	if filepath.IsAbs(value) {
		return value
	}
	root, err := filepath.Abs(".")
	if err != nil {
		return value
	}
	return filepath.Join(root, value)
}
