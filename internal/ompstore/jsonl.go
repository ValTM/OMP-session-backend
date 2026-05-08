package ompstore

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

func ParseSessionFile(path string, includeRaw bool) ParseResult {
	file, err := os.Open(path)
	if err != nil {
		msg := err.Error()
		return ParseResult{ParseError: &msg}
	}
	defer file.Close()

	var result ParseResult
	var searchParts []string
	var currentModel string
	toolCallModels := make(map[string]string)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		metadata, err := parseJSONLSessionMetadata(line)
		if err != nil {
			msg := fmt.Sprintf("line %d: %v", lineNumber, err)
			result.ParseError = &msg
			continue
		}
		if metadata.ID != "" && result.SessionID == "" {
			result.SessionID = metadata.ID
		}
		if metadata.CWD != "" && result.SessionCWD == "" {
			result.SessionCWD = metadata.CWD
		}
		if metadata.StartedAt != nil && result.SessionStartedAt == nil {
			startedAt := *metadata.StartedAt
			result.SessionStartedAt = &startedAt
		}
		if result.Title == nil && metadata.Title != "" {
			title := metadata.Title
			result.Title = &title
		}

		modelChange, err := parseJSONLModelChange(line)
		if err != nil {
			msg := fmt.Sprintf("line %d: %v", lineNumber, err)
			result.ParseError = &msg
			continue
		}
		if modelChange != "" {
			currentModel = modelChange
			result.MainModels = appendModel(result.MainModels, modelChange)
		}

		messages, err := parseJSONLMessages(line, includeRaw)
		if err != nil {
			msg := fmt.Sprintf("line %d: %v", lineNumber, err)
			result.ParseError = &msg
			continue
		}

		for _, message := range messages {
			if strings.TrimSpace(message.Text) == "" {
				continue
			}

			applyMessageModel(&message, currentModel, toolCallModels)
			result.MainModels = appendModel(result.MainModels, message.Model)
			result.MainUsage = addUsage(result.MainUsage, message.Usage)
			searchParts = append(searchParts, message.Model)
			result.Messages = append(result.Messages, message)
			if message.Usage != nil {
				searchParts = append(searchParts, fmt.Sprintf("%d tokens", message.Usage.TotalTokens))
			}
			if isActualMessage(message) {
				result.MessageCount++
			}
			searchParts = append(searchParts, message.Text)
			if !message.Timestamp.IsZero() && (result.LatestMessageAt == nil || message.Timestamp.After(*result.LatestMessageAt)) {
				latest := message.Timestamp
				result.LatestMessageAt = &latest
			}

			if result.FirstUserPrompt == nil && message.Role == "user" {
				text := message.Text
				result.FirstUserPrompt = &text
			}
		}
	}

	if err := scanner.Err(); err != nil {
		msg := err.Error()
		result.ParseError = &msg
	}

	if result.MainUsage != nil && result.MainUsage.TotalTokens == 0 {
		result.MainUsage.TotalTokens = result.MainUsage.Input + result.MainUsage.Output + result.MainUsage.CacheRead + result.MainUsage.CacheWrite
	}
	if result.MainModels == nil {
		result.MainModels = []string{}
	}
	result.SearchText = strings.Join(searchParts, " ")
	return result
}

type sessionMetadata struct {
	ID        string
	CWD       string
	Title     string
	StartedAt *time.Time
}

func parseJSONLSessionMetadata(line []byte) (sessionMetadata, error) {
	var envelope struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		CWD       string `json:"cwd"`
		Title     string `json:"title"`
		Timestamp string `json:"timestamp"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return sessionMetadata{}, err
	}
	if envelope.Type != "session" {
		return sessionMetadata{}, nil
	}
	var startedAt *time.Time
	if envelope.Timestamp != "" {
		parsed, err := time.Parse(time.RFC3339Nano, envelope.Timestamp)
		if err == nil {
			startedAt = &parsed
		}
	}
	return sessionMetadata{
		ID:        strings.TrimSpace(envelope.ID),
		CWD:       strings.TrimSpace(envelope.CWD),
		Title:     strings.TrimSpace(envelope.Title),
		StartedAt: startedAt,
	}, nil
}

func parseJSONLModelChange(line []byte) (string, error) {
	var envelope struct {
		Type  string `json:"type"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return "", err
	}
	if envelope.Type != "model_change" {
		return "", nil
	}
	return strings.TrimSpace(envelope.Model), nil
}

func parseJSONLMessages(line []byte, includeRaw bool) ([]SessionMessage, error) {
	var envelope struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		ParentID  *string         `json:"parentId"`
		Timestamp string          `json:"timestamp"`
		Message   json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return nil, err
	}
	if envelope.Type != "message" || len(envelope.Message) == 0 {
		return nil, nil
	}

	var payload struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		ToolName   string          `json:"toolName"`
		ToolCallID string          `json:"toolCallId"`
		Provider   string          `json:"provider"`
		Model      string          `json:"model"`
		Usage      *TokenUsage     `json:"usage"`
	}
	if err := json.Unmarshal(envelope.Message, &payload); err != nil {
		return nil, err
	}

	timestamp := time.Time{}
	if envelope.Timestamp != "" {
		parsed, err := time.Parse(time.RFC3339Nano, envelope.Timestamp)
		if err == nil {
			timestamp = parsed
		}
	}

	base := SessionMessage{
		ID:          envelope.ID,
		ParentID:    envelope.ParentID,
		Timestamp:   timestamp,
		Role:        payload.Role,
		Type:        envelope.Type,
		Model:       normalizeModel(payload.Provider, payload.Model),
		ModelSource: explicitModelSource(payload.Model),
		Usage:       payload.Usage,
	}
	if includeRaw {
		base.Raw = append(json.RawMessage(nil), line...)
	}

	if payload.Role == "toolResult" {
		content := readableContent(payload.Content)
		text := stripToolLineAnchors(content.Text)
		if payload.ToolName != "" && text != "" {
			text = "Tool result: " + payload.ToolName + "\n" + text
		}
		base.Text = text
		base.ToolCallID = payload.ToolCallID
		return []SessionMessage{base}, nil
	}

	content := readableContent(payload.Content)
	if len(content.ToolCallNames) > 0 {
		message := base
		message.Role = "toolCall"
		message.Text = toolCallText(content.Text, content.ToolCallNames)
		message.ToolCallIDs = content.ToolCallIDs
		return []SessionMessage{message}, nil
	}

	if content.Text == "" {
		return nil, nil
	}

	message := base
	message.Text = content.Text
	return []SessionMessage{message}, nil
}

type readableContentResult struct {
	Text          string
	HasText       bool
	ToolCallNames []string
	ToolCallIDs   []string
}

func isActualMessage(message SessionMessage) bool {
	return message.Role != "toolResult" && message.Role != "toolCall"
}

func applyMessageModel(message *SessionMessage, currentModel string, toolCallModels map[string]string) {
	if message.Role == "user" {
		return
	}

	if message.Role == "toolResult" {
		if model := toolCallModels[message.ToolCallID]; model != "" {
			message.Model = model
			message.ModelSource = "toolCallLink"
		} else if message.Model == "" && currentModel != "" {
			message.Model = currentModel
			message.ModelSource = "modelChange"
		}
		return
	}

	if message.Model == "" && currentModel != "" {
		message.Model = currentModel
		message.ModelSource = "modelChange"
	}

	if message.Role == "toolCall" && message.Model != "" {
		for _, id := range message.ToolCallIDs {
			toolCallModels[id] = message.Model
		}
	}
}

func normalizeModel(provider string, model string) string {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	if provider != "" && !strings.Contains(model, "/") {
		return provider + "/" + model
	}
	return model
}

func explicitModelSource(model string) string {
	if strings.TrimSpace(model) == "" {
		return ""
	}
	return "explicit"
}

func appendModel(models []string, model string) []string {
	model = strings.TrimSpace(model)
	if model == "" {
		return models
	}
	for _, existing := range models {
		if existing == model {
			return models
		}
	}
	return append(models, model)
}

func addUsage(total *TokenUsage, usage *TokenUsage) *TokenUsage {
	if usage == nil {
		return total
	}
	if total == nil {
		total = &TokenUsage{}
	}
	total.Input += usage.Input
	total.Output += usage.Output
	total.CacheRead += usage.CacheRead
	total.CacheWrite += usage.CacheWrite
	total.TotalTokens += usage.TotalTokens
	total.PremiumRequests += usage.PremiumRequests
	total.ReasoningTokens = addOptionalInt(total.ReasoningTokens, usage.ReasoningTokens)
	total.CTTL = addCacheWriteTTL(total.CTTL, usage.CTTL)
	total.Server = addServerToolUsage(total.Server, usage.Server)
	total.Cost.Input += usage.Cost.Input
	total.Cost.Output += usage.Cost.Output
	total.Cost.CacheRead += usage.Cost.CacheRead
	total.Cost.CacheWrite += usage.Cost.CacheWrite
	total.Cost.Total += usage.Cost.Total
	return total
}

func addOptionalInt(total *int, value *int) *int {
	if value == nil {
		return total
	}
	if total == nil {
		zero := 0
		total = &zero
	}
	*total += *value
	return total
}

func addCacheWriteTTL(total *CacheWriteTTL, value *CacheWriteTTL) *CacheWriteTTL {
	if value == nil {
		return total
	}
	if total == nil {
		total = &CacheWriteTTL{}
	}
	total.Ephemeral5m += value.Ephemeral5m
	total.Ephemeral1h += value.Ephemeral1h
	return total
}

func addServerToolUsage(total *ServerToolUsage, value *ServerToolUsage) *ServerToolUsage {
	if value == nil {
		return total
	}
	if total == nil {
		total = &ServerToolUsage{}
	}
	total.WebSearch += value.WebSearch
	total.WebFetch += value.WebFetch
	return total
}

func readableContent(raw json.RawMessage) readableContentResult {
	if len(raw) == 0 || string(raw) == "null" {
		return readableContentResult{}
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		text = strings.TrimSpace(text)
		return readableContentResult{Text: text, HasText: text != ""}
	}

	var parts []struct {
		Type     string          `json:"type"`
		ID       string          `json:"id"`
		Text     string          `json:"text"`
		Name     string          `json:"name"`
		ToolName string          `json:"toolName"`
		Content  json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return readableContentResult{}
	}

	var out []string
	var hasText bool
	var toolCallNames []string
	var toolCallIDs []string
	for _, part := range parts {
		switch part.Type {
		case "text", "output_text":
			if appendIfNotEmpty(&out, part.Text) {
				hasText = true
			}
		case "toolCall":
			name := part.Name
			if name == "" {
				name = part.ToolName
			}
			if name != "" {
				toolCallNames = append(toolCallNames, name)
			}
			if part.ID != "" {
				toolCallIDs = append(toolCallIDs, part.ID)
			}
		case "reasoning", "thinking":
			// Intentionally hidden: these entries often contain encrypted thinking blobs.
		default:
			if appendIfNotEmpty(&out, part.Text) {
				hasText = true
			}
		}
	}
	return readableContentResult{Text: strings.TrimSpace(strings.Join(out, "\n")), HasText: hasText, ToolCallNames: toolCallNames, ToolCallIDs: toolCallIDs}
}

func toolCallText(prefix string, names []string) string {
	var lines []string
	if strings.TrimSpace(prefix) != "" {
		lines = append(lines, strings.TrimSpace(prefix))
	}
	for _, name := range names {
		lines = append(lines, "Tool call: "+name)
	}
	return strings.Join(lines, "\n")
}

func stripToolLineAnchors(text string) string {
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		lines[index] = stripToolLineAnchor(line)
	}
	return strings.Join(lines, "\n")
}

func stripToolLineAnchor(line string) string {
	trimmed := strings.TrimPrefix(line, "*")
	trimmed = strings.TrimPrefix(trimmed, " ")
	prefix, rest, ok := strings.Cut(trimmed, "|")
	if !ok || len(prefix) < 3 {
		return line
	}

	digitEnd := 0
	for digitEnd < len(prefix) && prefix[digitEnd] >= '0' && prefix[digitEnd] <= '9' {
		digitEnd++
	}
	if digitEnd == 0 || len(prefix)-digitEnd != 2 {
		return line
	}
	for _, char := range prefix[digitEnd:] {
		if char < 'a' || char > 'z' {
			return line
		}
	}
	return rest
}

func appendIfNotEmpty(out *[]string, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	*out = append(*out, value)
	return true
}
