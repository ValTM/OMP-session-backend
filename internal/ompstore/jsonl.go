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

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
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

			result.Messages = append(result.Messages, message)
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

	result.SearchText = strings.Join(searchParts, " ")
	return result
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
		Role     string          `json:"role"`
		Content  json.RawMessage `json:"content"`
		ToolName string          `json:"toolName"`
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
		ID:        envelope.ID,
		ParentID:  envelope.ParentID,
		Timestamp: timestamp,
		Role:      payload.Role,
		Type:      envelope.Type,
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
		return []SessionMessage{base}, nil
	}

	content := readableContent(payload.Content)
	messages := make([]SessionMessage, 0, 1+len(content.ToolCallNames))
	if content.Text != "" {
		message := base
		message.Text = content.Text
		messages = append(messages, message)
	}
	for index, name := range content.ToolCallNames {
		message := base
		message.ID = fmt.Sprintf("%s:toolCall:%d", envelope.ID, index)
		message.Role = "toolCall"
		message.Text = "Tool call: " + name
		messages = append(messages, message)
	}
	return messages, nil
}

type readableContentResult struct {
	Text          string
	HasText       bool
	ToolCallNames []string
}

func isActualMessage(message SessionMessage) bool {
	return message.Role != "toolResult" && message.Role != "toolCall"
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
		case "reasoning", "thinking":
			// Intentionally hidden: these entries often contain encrypted thinking blobs.
		default:
			if appendIfNotEmpty(&out, part.Text) {
				hasText = true
			}
		}
	}
	return readableContentResult{Text: strings.TrimSpace(strings.Join(out, "\n")), HasText: hasText, ToolCallNames: toolCallNames}
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
