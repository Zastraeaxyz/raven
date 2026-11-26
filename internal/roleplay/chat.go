package roleplay

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"raven/internal/ai"
)

const (
	discordMessageLimit   = 2000
	maxCharsPerMessage    = 4000
	maxContextChars       = 20000
	typingRefreshInterval = 8 * time.Second
)

func (f *Feature) handleMessage(ctx context.Context, s *discordgo.Session, m *discordgo.MessageCreate) error {
	if f == nil || f.chat == nil || s == nil || s.State == nil || s.State.User == nil {
		return nil
	}
	if m == nil || m.Message == nil || m.Author == nil || m.Author.Bot {
		return nil
	}
	if m.WebhookID != "" || m.GuildID == "" {
		return nil
	}
	if f.guildID != "" && m.GuildID != f.guildID {
		return nil
	}

	botID := s.State.User.ID
	if botID == "" || m.Author.ID == botID {
		return nil
	}

	if session, ok := f.learning.active(m.ChannelID, m.Author.ID); ok {
		return f.handleLearning(ctx, s, m, session)
	}

	mentioned := mentionsUser(m, botID)
	referenced := m.ReferencedMessage
	replyToBot := referenced != nil && referenced.Author != nil && referenced.Author.ID == botID
	if !replyToBot && m.MessageReference != nil && f.sent.has(m.ChannelID, m.MessageReference.MessageID) {
		replyToBot = true
	}
	if !mentioned && !replyToBot {
		return nil
	}

	if !f.limiter.allow(m.Author.ID) {
		f.log.Debug("roleplay rate limit exceeded", "user_id", m.Author.ID, "channel_id", m.ChannelID)
		return nil
	}

	if !f.hasLockeeRole(s, m) {
		f.send(s, m, "Only my lockees can speak to me here.", 1)
		return nil
	}

	prompt := cleanContent(m, botID)
	images := f.visionImages(m, referenced)
	if prompt == "" && len(images) == 0 {
		f.send(s, m, "You have my attention. Tell me what you need.", 1)
		return nil
	}

	release, err := f.acquire(ctx)
	if err != nil {
		f.log.Warn("roleplay busy", "err", err, "user_id", m.Author.ID)
		return nil
	}
	defer release()

	return f.respond(ctx, s, m, referenced, prompt, images)
}

func (f *Feature) respond(ctx context.Context, s *discordgo.Session, m *discordgo.MessageCreate, referenced *discordgo.Message, prompt string, images []ai.Image) error {
	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	stopTyping := f.startTyping(s, m.ChannelID)
	defer stopTyping()

	history := f.gatherContext(s, m, referenced)
	messages := buildMessages(s.State.User.ID, history, prompt, m.Author, m.Timestamp, images)
	if len(messages) == 0 {
		return nil
	}

	system := f.systemPrompt(m.Author.ID, displayName(m.Author))
	started := time.Now()
	target := f.captionTarget(m, referenced)

	f.log.Info("roleplay request",
		"guild_id", m.GuildID,
		"channel_id", m.ChannelID,
		"user_id", m.Author.ID,
		"history", len(history),
	)
	reply, err := f.converse(ctx, system, messages, m.Author.ID, s, target)
	if err != nil {
		f.log.Error("roleplay model request failed",
			"err", err,
			"channel_id", m.ChannelID,
			"user_id", m.Author.ID,
		)
		f.send(s, m, "I lost my train of thought. Give me a moment and try again.", 1)
		return nil
	}

	f.log.Info("roleplay response",
		"guild_id", m.GuildID,
		"channel_id", m.ChannelID,
		"user_id", m.Author.ID,
		"duration", time.Since(started),
		"prompt_tokens", reply.Usage.PromptTokens,
		"completion_tokens", reply.Usage.CompletionTokens,
	)

	f.send(s, m, reply.Text, f.maxChunks)
	f.captureMemory(m.Author.ID, displayName(m.Author), memoryTranscript(messages, reply.Text))
	return nil
}

func (f *Feature) personaText() string {
	if f.soul != nil {
		return f.soul.Text()
	}
	return f.basePrompt
}

func (f *Feature) systemPrompt(userID, name string) string {
	parts := []string{f.personaText()}
	if f.memory != nil {
		if block := f.memory.context(userID, name); block != "" {
			parts = append(parts, block)
		}
	}
	parts = append(parts, safetyPrompt)
	if f.locks != nil {
		parts = append(parts, chastityGuidance)
	}
	parts = append(parts, fmt.Sprintf("Current Unix time: %d.", f.now().Unix()))
	return strings.Join(parts, "\n\n")
}

// maxToolRounds caps a chain of tool calls so a confused model cannot loop.
const maxToolRounds = 2

// converse runs the model, executes any tool calls, and sends the results
// back. The final answer is a normal text reply.
func (f *Feature) converse(ctx context.Context, system string, messages []ai.Message, userID string, s *discordgo.Session, target *captionTarget) (ai.Reply, error) {
	tools := f.tools
	if target != nil {
		tools = append(append([]ai.Tool(nil), f.tools...), captionReactTool())
	}
	reacted := false

	for round := 0; round < maxToolRounds; round++ {
		reply, err := f.chat.ChatWithTools(ctx, system, messages, tools)
		if err != nil {
			return ai.Reply{}, err
		}
		if len(reply.ToolCalls) == 0 {
			return reply, nil
		}

		messages = append(messages, ai.Message{
			Role:      "assistant",
			Content:   reply.Text,
			ToolCalls: reply.ToolCalls,
		})
		for _, call := range reply.ToolCalls {
			var result string
			switch {
			case call.Function.Name == captionReactToolName:
				if reacted {
					result = "You already reacted to this caption."
				} else {
					result = f.reactToCaption(s, target, call.Function.Arguments)
					reacted = true
				}
			default:
				result = f.runTool(ctx, userID, call.Function.Name, call.Function.Arguments)
			}
			f.log.Info("roleplay tool call",
				"tool", call.Function.Name,
				"user_id", userID,
			)
			messages = append(messages, ai.Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    result,
			})
		}
	}

	// The model kept calling tools. Ask once more with no tools available so
	// the user gets a spoken answer.
	return f.chat.ChatWithTools(ctx, system, messages, nil)
}

func (f *Feature) gatherContext(s *discordgo.Session, m *discordgo.MessageCreate, referenced *discordgo.Message) []*discordgo.Message {
	limit := f.contextMessages
	if limit < 1 {
		limit = 1
	}

	prior, err := s.ChannelMessages(m.ChannelID, limit, m.ID, "", "")
	if err != nil {
		f.log.Debug("cannot fetch channel context", "err", err, "channel_id", m.ChannelID)
		if referenced != nil {
			return []*discordgo.Message{referenced}
		}
		return nil
	}

	history := reverseMessages(prior)
	if referenced != nil && !containsMessage(history, referenced.ID) {
		history = append([]*discordgo.Message{referenced}, history...)
	}
	if len(history) > limit {
		history = history[len(history)-limit:]
	}
	return history
}

func reverseMessages(messages []*discordgo.Message) []*discordgo.Message {
	reversed := make([]*discordgo.Message, len(messages))
	for i, message := range messages {
		reversed[len(messages)-1-i] = message
	}
	return reversed
}

func containsMessage(messages []*discordgo.Message, id string) bool {
	for _, message := range messages {
		if message != nil && message.ID == id {
			return true
		}
	}
	return false
}

func buildMessages(selfID string, history []*discordgo.Message, prompt string, author *discordgo.User, timestamp time.Time, images []ai.Image) []ai.Message {
	messages := make([]ai.Message, 0, len(history)+1)

	for _, message := range history {
		if message == nil || message.Author == nil {
			continue
		}
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}

		role := "user"
		if message.Author.ID == selfID {
			role = "assistant"
		} else {
			content = displayName(message.Author) + timestampSuffix(message.Timestamp) + ": " + content
		}
		messages = append(messages, ai.Message{
			Role:    role,
			Content: truncateRunes(content, maxCharsPerMessage),
		})
	}

	if author != nil {
		current := displayName(author) + timestampSuffix(timestamp) + ": " + strings.TrimSpace(prompt)
		messages = append(messages, ai.Message{
			Role:    "user",
			Content: truncateRunes(current, maxCharsPerMessage),
			Images:  images,
		})
	}

	return trimContext(messages)
}

// trimContext drops the oldest turns until the context fits. The current user
// message is always last, so the newest context is kept.
func trimContext(messages []ai.Message) []ai.Message {
	total := 0
	for _, message := range messages {
		total += len([]rune(message.Content))
	}
	for total > maxContextChars && len(messages) > 1 {
		total -= len([]rune(messages[0].Content))
		messages = messages[1:]
	}
	if total > maxContextChars && len(messages) == 1 {
		messages[0].Content = truncateRunes(messages[0].Content, maxContextChars)
	}
	return messages
}

func displayName(user *discordgo.User) string {
	if user == nil {
		return "unknown"
	}
	if name := strings.TrimSpace(user.DisplayName()); name != "" {
		return name
	}
	if name := strings.TrimSpace(user.Username); name != "" {
		return name
	}
	return user.ID
}

func timestampSuffix(timestamp time.Time) string {
	if timestamp.IsZero() {
		return ""
	}
	return " [" + timestamp.UTC().Format(time.RFC3339) + "]"
}

func mentionsUser(m *discordgo.MessageCreate, userID string) bool {
	if m == nil || m.Message == nil {
		return false
	}
	for _, user := range m.Mentions {
		if user != nil && user.ID == userID {
			return true
		}
	}
	return strings.Contains(m.Content, "<@"+userID+">") || strings.Contains(m.Content, "<@!"+userID+">")
}

func cleanContent(m *discordgo.MessageCreate, botID string) string {
	if m == nil || m.Message == nil {
		return ""
	}
	content := m.Content
	content = strings.ReplaceAll(content, "<@"+botID+">", "")
	content = strings.ReplaceAll(content, "<@!"+botID+">", "")
	return strings.TrimSpace(content)
}

func (f *Feature) hasLockeeRole(s *discordgo.Session, m *discordgo.MessageCreate) bool {
	if f.lockeeRoleID == "" {
		return true
	}
	if m == nil || m.Author == nil {
		return false
	}
	if m.Member != nil {
		return hasRoleID(m.Member.Roles, f.lockeeRoleID)
	}

	member, err := s.GuildMember(m.GuildID, m.Author.ID)
	if err != nil || member == nil {
		f.log.Debug("cannot load member for role check", "err", err, "user_id", m.Author.ID)
		return false
	}
	return hasRoleID(member.Roles, f.lockeeRoleID)
}

func hasRoleID(roles []string, want string) bool {
	if want == "" {
		return false
	}
	for _, role := range roles {
		if role == want {
			return true
		}
	}
	return false
}

func (f *Feature) startTyping(s *discordgo.Session, channelID string) func() {
	stop := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		_ = s.ChannelTyping(channelID)

		ticker := time.NewTicker(typingRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				_ = s.ChannelTyping(channelID)
			}
		}
	}()

	return func() {
		close(stop)
		<-done
	}
}

func (f *Feature) send(s *discordgo.Session, m *discordgo.MessageCreate, content string, maxChunks int) {
	chunks := splitMessage(content, discordMessageLimit, maxChunks)
	if len(chunks) == 0 {
		return
	}

	failIfNotExists := false
	for index, chunk := range chunks {
		var reference *discordgo.MessageReference
		if index == 0 {
			reference = &discordgo.MessageReference{
				MessageID:       m.ID,
				ChannelID:       m.ChannelID,
				GuildID:         m.GuildID,
				FailIfNotExists: &failIfNotExists,
			}
		}

		sent, err := s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
			Content:   chunk,
			Reference: reference,
			AllowedMentions: &discordgo.MessageAllowedMentions{
				Parse:       []discordgo.AllowedMentionType{discordgo.AllowedMentionTypeUsers},
				RepliedUser: false,
			},
		})
		if err != nil {
			f.log.Warn("sending roleplay reply", "err", err, "channel_id", m.ChannelID)
			return
		}
		if sent != nil && f.sent != nil {
			f.sent.add(m.ChannelID, sent.ID)
		}
	}
}

func splitMessage(content string, limit, maxChunks int) []string {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	if limit <= 0 {
		limit = discordMessageLimit
	}
	if maxChunks < 1 {
		maxChunks = 1
	}

	remaining := []rune(content)
	chunks := make([]string, 0, maxChunks)

	for len(remaining) > limit && len(chunks) < maxChunks-1 {
		window := remaining[:limit]
		cut := -1
		for i := len(window) - 1; i >= 0; i-- {
			if window[i] == '\n' {
				cut = i
				break
			}
		}
		if cut <= 0 {
			cut = limit
		}

		if chunk := strings.TrimSpace(string(remaining[:cut])); chunk != "" {
			chunks = append(chunks, chunk)
		}
		remaining = []rune(strings.TrimSpace(string(remaining[cut:])))
	}

	if len(remaining) > limit {
		remaining = []rune(truncateRunes(string(remaining), limit))
	}
	if chunk := strings.TrimSpace(string(remaining)); chunk != "" {
		chunks = append(chunks, chunk)
	}
	return chunks
}

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if max <= 0 || len(runes) <= max {
		return s
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}
