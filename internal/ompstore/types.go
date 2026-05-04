package ompstore

import (
	"encoding/json"
	"time"
)

type SessionSummary struct {
	ID              string    `json:"id"`
	UpdatedAt       time.Time `json:"updatedAt"`
	CWD             string    `json:"cwd"`
	SourceKind      string    `json:"sourceKind"`
	RolloutPath     string    `json:"rolloutPath"`
	Slug            *string   `json:"slug,omitempty"`
	Summary         *string   `json:"summary,omitempty"`
	FirstUserPrompt *string   `json:"firstUserPrompt,omitempty"`
	MessageCount    int       `json:"messageCount"`
	ResumeCommand   string    `json:"resumeCommand"`
	SearchText      string    `json:"searchText"`
	ParseError      *string   `json:"parseError,omitempty"`
}

type SessionMessage struct {
	ID        string          `json:"id"`
	ParentID  *string         `json:"parentId,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
	Role      string          `json:"role"`
	Text      string          `json:"text"`
	Type      string          `json:"type"`
	Raw       json.RawMessage `json:"raw,omitempty"`
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
	FirstUserPrompt *string
	MessageCount    int
	LatestMessageAt *time.Time
	Messages        []SessionMessage
	SearchText      string
	ParseError      *string
}
