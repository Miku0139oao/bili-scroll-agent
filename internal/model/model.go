package model

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

type User struct {
	ID            string `json:"id"`
	Username      string `json:"username"`
	DisplayName   string `json:"display_name"`
	Role          string `json:"role"`
	PasswordHash  string `json:"-"`
	TOTPSecretEnc string `json:"-"`
	TOTPConfirmed bool   `json:"-"`
	Disabled      bool   `json:"disabled"`
	CreatedAt     string `json:"created_at"`
	LastLoginAt   string `json:"last_login_at,omitempty"`
}

type Session struct {
	IDHash        string
	UserID        string
	CSRFHash      string
	CreatedAt     time.Time
	ExpiresAt     time.Time
	IdleExpiresAt time.Time
	RevokedAt     *time.Time
	IPHint        string
}

type APIIdentifier struct {
	User   User
	Hash   string
	Kind   string
	RunID  string
	Scopes []string
}

type Schedule struct {
	Enabled   bool   `json:"enabled"`
	Time      string `json:"time"`
	Timezone  string `json:"timezone"`
	NextRunAt string `json:"next_run_at,omitempty"`
}

type MemberConfig struct {
	Runner          string   `json:"runner"`
	Keywords        []string `json:"keywords"`
	Persona         string   `json:"persona"`
	PerDay          int      `json:"perDay"`
	ReportLevel     string   `json:"reportLevel"`
	Language        string   `json:"language"`
	AutoComment     bool     `json:"autoComment"`
	CommentPerRound int      `json:"commentPerRound"`
	MentionName     string   `json:"mentionName"`
	DMMode          string   `json:"dmMode"`
	DMUID           int64    `json:"dmUid"`
	ReplyToComments bool     `json:"replyToComments"`
	ReplyPerRound   int      `json:"replyPerRound"`
	DMWatch         bool     `json:"dmWatch"`
	DMWatchMinutes  int      `json:"dmWatchMinutes"`
	DMReplyPerDay   int      `json:"dmReplyPerDay"`
	FavFolderID     int64    `json:"favFolderId"`
	Transcribe      bool     `json:"transcribe"`
	ASRModel        string   `json:"asrModel"`
	Schedule        Schedule `json:"schedule"`
}

func DefaultMemberConfig() MemberConfig {
	return MemberConfig{
		Runner:          "codex",
		Keywords:        []string{},
		PerDay:          5,
		ReportLevel:     "full",
		Language:        "zh-TW",
		CommentPerRound: 1,
		DMMode:          "off",
		ReplyPerRound:   1,
		DMWatchMinutes:  5,
		ASRModel:        "small",
		Schedule: Schedule{
			Enabled:  false,
			Time:     "20:00",
			Timezone: "Asia/Shanghai",
		},
	}
}

type AdminSettings struct {
	DefaultRunner       string `json:"default_runner"`
	CodexProvider       string `json:"codex_provider"`
	CodexModel          string `json:"codex_model"`
	CodexReasoning      string `json:"codex_reasoning"`
	HermesProvider      string `json:"hermes_provider"`
	HermesModel         string `json:"hermes_model"`
	OutboundEnabled     bool   `json:"outbound_enabled"`
	GlobalConcurrency   int    `json:"global_concurrency"`
	ManualRunsPerDay    int    `json:"manual_runs_per_day"`
	ScheduledRunsPerDay int    `json:"scheduled_runs_per_day"`
	RetentionDays       int    `json:"retention_days"`
}

func DefaultAdminSettings() AdminSettings {
	return AdminSettings{
		DefaultRunner:       "codex",
		OutboundEnabled:     true,
		GlobalConcurrency:   1,
		ManualRunsPerDay:    3,
		ScheduledRunsPerDay: 1,
		RetentionDays:       365,
	}
}

type Run struct {
	ID           string `json:"id"`
	UserID       string `json:"-"`
	Kind         string `json:"kind"`
	Runner       string `json:"runner"`
	Status       string `json:"status"`
	ScheduledFor string `json:"scheduled_for,omitempty"`
	StartedAt    string `json:"started_at,omitempty"`
	HeartbeatAt  string `json:"-"`
	FinishedAt   string `json:"finished_at,omitempty"`
	Error        string `json:"error,omitempty"`
	PickedCount  int    `json:"picked_count"`
	Telemetry    string `json:"-"`
}

type Video struct {
	BVID        string `json:"bvid"`
	AID         int64  `json:"aid,omitempty"`
	Title       string `json:"title"`
	Author      string `json:"author"`
	URL         string `json:"url"`
	PubDate     string `json:"pubdate,omitempty"`
	Play        int64  `json:"play,omitempty"`
	Review      int64  `json:"review,omitempty"`
	Danmaku     int64  `json:"danmaku,omitempty"`
	Duration    any    `json:"duration,omitempty"`
	Description string `json:"description,omitempty"`
	Cover       string `json:"cover,omitempty"`
}

type PickedItem struct {
	BVID     string `json:"bvid"`
	Title    string `json:"title"`
	Author   string `json:"author"`
	URL      string `json:"url"`
	Tier     string `json:"tier"`
	Reason   string `json:"reason"`
	OneLiner string `json:"one_liner"`
	Comment  string `json:"comment,omitempty"`
}

type SkippedItem struct {
	BVID   string `json:"bvid"`
	Reason string `json:"reason"`
	Title  string `json:"title,omitempty"`
}

type DeliveryDraft struct {
	BVID    string `json:"bvid"`
	Kind    string `json:"kind"`
	Content string `json:"content"`
	Target  string `json:"target,omitempty"`
}

type RunSummary struct {
	RunID           string          `json:"run_id"`
	Picked          []PickedItem    `json:"picked"`
	Skipped         []SkippedItem   `json:"skipped"`
	Summary         string          `json:"summary"`
	NoSubtitleCount int             `json:"no_subtitle_count"`
	DeliveryDrafts  []DeliveryDraft `json:"delivery_drafts,omitempty"`
	CompletedAt     string          `json:"completed_at,omitempty"`
}

func NewID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))[:36]
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return hex.EncodeToString(raw[0:4]) + "-" +
		hex.EncodeToString(raw[4:6]) + "-" +
		hex.EncodeToString(raw[6:8]) + "-" +
		hex.EncodeToString(raw[8:10]) + "-" +
		hex.EncodeToString(raw[10:16])
}
