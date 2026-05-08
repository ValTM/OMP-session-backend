package ompstore

import (
	"encoding/json"
	"time"
)

type SessionSummary struct {
	ID              string      `json:"id"`
	UpdatedAt       time.Time   `json:"updatedAt"`
	CWD             string      `json:"cwd"`
	SourceKind      string      `json:"sourceKind"`
	RolloutPath     string      `json:"rolloutPath"`
	Title           *string     `json:"title,omitempty"`
	Slug            *string     `json:"slug,omitempty"`
	Summary         *string     `json:"summary,omitempty"`
	FirstUserPrompt *string     `json:"firstUserPrompt,omitempty"`
	MessageCount    int         `json:"messageCount"`
	MainModels      []string    `json:"mainModels"`
	MainUsage       *TokenUsage `json:"mainUsage,omitempty"`
	ResumeCommand   string      `json:"resumeCommand"`
	SearchText      string      `json:"searchText"`
	ParseError      *string     `json:"parseError,omitempty"`
}

type SessionMessage struct {
	ID          string          `json:"id"`
	ParentID    *string         `json:"parentId,omitempty"`
	Timestamp   time.Time       `json:"timestamp"`
	Role        string          `json:"role"`
	Text        string          `json:"text"`
	Type        string          `json:"type"`
	Model       string          `json:"model,omitempty"`
	ModelSource string          `json:"modelSource,omitempty"`
	Usage       *TokenUsage     `json:"usage,omitempty"`
	ToolCallID  string          `json:"-"`
	ToolCallIDs []string        `json:"-"`
	Raw         json.RawMessage `json:"raw,omitempty"`
}

type TokenUsage struct {
	Input           int              `json:"input"`
	Output          int              `json:"output"`
	CacheRead       int              `json:"cacheRead"`
	CacheWrite      int              `json:"cacheWrite"`
	TotalTokens     int              `json:"totalTokens"`
	PremiumRequests int              `json:"premiumRequests,omitempty"`
	ReasoningTokens *int             `json:"reasoningTokens,omitempty"`
	CTTL            *CacheWriteTTL   `json:"cttl,omitempty"`
	Server          *ServerToolUsage `json:"server,omitempty"`
	Cost            UsageCost        `json:"cost"`
}

type CacheWriteTTL struct {
	Ephemeral5m int `json:"ephemeral5m,omitempty"`
	Ephemeral1h int `json:"ephemeral1h,omitempty"`
}

type ServerToolUsage struct {
	WebSearch int `json:"webSearch,omitempty"`
	WebFetch  int `json:"webFetch,omitempty"`
}

type UsageCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
}

type MessageListResult struct {
	Items           []SessionMessage `json:"items"`
	Total           int              `json:"total"`
	Limit           int              `json:"limit"`
	Offset          int              `json:"offset"`
	ToolCallCount   int              `json:"toolCallCount"`
	ToolResultCount int              `json:"toolResultCount"`
}

type ListSessionsFilter struct {
	Query                string
	CWD                  string
	SourceKind           string
	From                 *time.Time
	To                   *time.Time
	IncludeEmptyMessages bool
	MessageCountBucket   string
	Limit                int
	Offset               int
	Sort                 string
}

type ListSessionsResult struct {
	Items  []SessionSummary `json:"items"`
	Total  int              `json:"total"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
}

type CWDOption struct {
	CWD   string `json:"cwd"`
	Count int    `json:"count"`
}

type ParseResult struct {
	SessionID        string
	SessionCWD       string
	SessionStartedAt *time.Time
	Title            *string
	FirstUserPrompt  *string
	MessageCount     int
	MainModels       []string
	MainUsage        *TokenUsage
	LatestMessageAt  *time.Time
	Messages         []SessionMessage
	SearchText       string
	ParseError       *string
}
