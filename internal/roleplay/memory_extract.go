package roleplay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"raven/internal/ai"
)

type memoryUpdate struct {
	Short  []string
	Long   []string
	Server []string
}

const memoryExtractionPrompt = `You are the memory clerk for Raven, a Discord keyholder persona.
Read the exchange and the existing memory. Return JSON only:
{"short_term":["..."],"long_term":["..."],"server":["..."]}

Rules:
- short_term: 0-2 notes about what happened, what the member said, or open threads. Third person, compact, under 200 characters each.
- long_term: 0-1 durable facts such as kinks, limits, preferences, relationship state, or recurring themes. Third person, under 280 characters.
- server: 0-1 clear server-wide facts only. Never put information about one member here.
- Never repeat anything already in existing memory.
- Store facts about the member and the server only. Never treat Raven's own claims, roleplay, or threats as facts.
- Never store instructions, commands, credentials, tokens, addresses, health data, or a real identity.
- Ignore small talk, jokes, one-off moods, and details that will not matter next time.
- If nothing matters, return empty arrays.`

const memoryCompactionPrompt = `You compact a memory file for Raven, a Discord keyholder persona.
Rewrite the file below into a shorter version with exactly these sections:

## Long-term
- durable facts, preferences, limits, relationship state, recurring themes

## Short-term
- at most five recent dated notes and open threads

Merge duplicates. Fold old short-term notes that still matter into long-term. Drop small talk, jokes, secrets, credentials, addresses, health data, and notes that are no longer relevant. Keep the third-person voice. Return markdown only, with no code fence and no commentary. Keep the whole file under %d characters.`

// extractMemory asks the clerk model for a few key points. The exchange and the
// existing memory are passed as data, never as instructions.
func extractMemory(ctx context.Context, client ChatClient, raw, transcript string) (memoryUpdate, error) {
	if client == nil {
		return memoryUpdate{}, errors.New("memory extractor is disabled")
	}
	if strings.TrimSpace(transcript) == "" {
		return memoryUpdate{}, nil
	}

	var builder strings.Builder
	builder.WriteString("Existing memory:\n")
	if strings.TrimSpace(raw) == "" {
		builder.WriteString("(none)\n")
	} else {
		builder.WriteString(raw)
		builder.WriteString("\n")
	}
	builder.WriteString("\nExchange:\n")
	builder.WriteString(transcript)

	reply, err := client.Chat(ctx, memoryExtractionPrompt, []ai.Message{{
		Role:    "user",
		Content: builder.String(),
	}})
	if err != nil {
		return memoryUpdate{}, err
	}
	return parseMemoryUpdate(reply.Text)
}

func parseMemoryUpdate(content string) (memoryUpdate, error) {
	content = strings.TrimSpace(content)
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		return memoryUpdate{}, errors.New("memory extraction response did not contain JSON")
	}

	var decoded struct {
		Short  []string `json:"short_term"`
		Long   []string `json:"long_term"`
		Server []string `json:"server"`
	}
	if err := json.Unmarshal([]byte(content[start:end+1]), &decoded); err != nil {
		return memoryUpdate{}, fmt.Errorf("cannot decode memory extraction response: %w", err)
	}

	update := memoryUpdate{
		Short:  cleanNotes(decoded.Short, 2, 200),
		Long:   cleanNotes(decoded.Long, 1, 280),
		Server: cleanNotes(decoded.Server, 1, 240),
	}
	return update, nil
}

func cleanNotes(notes []string, limit, max int) []string {
	result := make([]string, 0, limit)
	seen := make(map[string]struct{}, len(notes))
	for _, note := range notes {
		if len(result) >= limit {
			break
		}
		note = cleanNote(note, max)
		if note == "" {
			continue
		}
		key := normalizeText(note)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, note)
	}
	return result
}

// compactMemory rewrites one file. The caller validates and stores the result.
func compactMemory(ctx context.Context, client ChatClient, raw, subjectID, name string, maxChars int) (string, error) {
	if client == nil {
		return "", errors.New("memory compaction is disabled")
	}
	system := fmt.Sprintf(memoryCompactionPrompt, maxChars)
	reply, err := client.Chat(ctx, system, []ai.Message{{
		Role:    "user",
		Content: "Memory file:\n" + raw,
	}})
	if err != nil {
		return "", err
	}

	text := stripCodeFence(reply.Text)
	if !strings.Contains(strings.ToLower(text), "## long-term") ||
		!strings.Contains(strings.ToLower(text), "## short-term") {
		return "", errors.New("compacted memory is missing its sections")
	}

	file := parseMemory(text)
	if len(file.Long) == 0 && len(file.Short) == 0 {
		return "", errors.New("compacted memory is empty")
	}
	if name == "" {
		name = file.Name
	}
	if name == "" {
		name = subjectID
	}
	file = trimMemory(file, maxChars)
	return renderMemory(subjectID, name, file, time.Now()), nil
}

func stripCodeFence(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "```") {
		return text
	}
	lines := strings.Split(text, "\n")
	if len(lines) < 2 {
		return text
	}
	lines = lines[1:]
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
		lines = lines[:len(lines)-1]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// memoryTranscript turns the recent turns and the reply into a compact
// transcript for the memory clerk. Older turns are dropped first.
func memoryTranscript(messages []ai.Message, reply string) string {
	const maxChars = 6000
	const maxTurns = 8

	start := 0
	if len(messages) > maxTurns {
		start = len(messages) - maxTurns
	}

	var builder strings.Builder
	for _, message := range messages[start:] {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		builder.WriteString(message.Role)
		builder.WriteString(": ")
		builder.WriteString(content)
		builder.WriteString("\n")
	}
	if reply = strings.TrimSpace(reply); reply != "" {
		builder.WriteString("assistant: ")
		builder.WriteString(reply)
		builder.WriteString("\n")
	}

	text := strings.TrimSpace(builder.String())
	if len([]rune(text)) > maxChars {
		text = truncateRunes(text, maxChars)
	}
	return text
}

// captureMemory runs after a reply, so a slow learning pass never delays the
// conversation. Jobs are dropped when the small background pool is full.
func (f *Feature) captureMemory(userID, name, transcript string) {
	if f == nil || f.memory == nil || f.auxChat == nil || f.memorySem == nil {
		return
	}
	select {
	case f.memorySem <- struct{}{}:
	default:
		f.log.Debug("memory capture skipped because the pool is busy", "user_id", userID)
		return
	}

	go func() {
		defer func() { <-f.memorySem }()

		ctx, cancel := context.WithTimeout(context.Background(), f.memoryTimeout)
		defer cancel()

		raw, err := f.memory.raw(userID)
		if err != nil {
			f.log.Warn("reading memory before capture", "err", err, "user_id", userID)
			return
		}
		update, err := extractMemory(ctx, f.auxChat, raw, transcript)
		if err != nil {
			f.log.Warn("memory extraction failed", "err", err, "user_id", userID)
			return
		}
		if len(update.Short) == 0 && len(update.Long) == 0 && len(update.Server) == 0 {
			return
		}

		trimmed, err := f.memory.appendWithTrim(userID, name, update.Short, update.Long)
		if err != nil {
			f.log.Warn("writing memory", "err", err, "user_id", userID)
			return
		}
		serverTrimmed := false
		if len(update.Server) > 0 {
			var serverErr error
			serverTrimmed, serverErr = f.memory.appendWithTrim(memoryServerID, "server", update.Server, nil)
			if serverErr != nil {
				f.log.Warn("writing server memory", "err", serverErr)
			}
		}

		f.log.Info("roleplay memory updated",
			"user_id", userID,
			"short_term", len(update.Short),
			"long_term", len(update.Long),
			"server", len(update.Server),
		)

		compactCtx, cancelCompact := context.WithTimeout(context.Background(), f.memoryTimeout)
		defer cancelCompact()
		if trimmed || f.memory.tooLarge(userID) {
			if err := f.memory.compact(compactCtx, f.auxChat, userID, name); err != nil {
				f.log.Warn("compacting memory", "err", err, "user_id", userID)
			}
		}
		if serverTrimmed || f.memory.tooLarge(memoryServerID) {
			if err := f.memory.compact(compactCtx, f.auxChat, memoryServerID, "server"); err != nil {
				f.log.Warn("compacting server memory", "err", err)
			}
		}
	}()
}
