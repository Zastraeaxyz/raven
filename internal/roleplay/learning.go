package roleplay

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"raven/internal/ai"
	"raven/internal/bot"
)

const (
	soulUpdateToolName    = "soul_update"
	maxLearningToolRounds = 3
	maxLearningTurns      = 20
	soulProposalTTL       = 15 * time.Minute
)

type learningSession struct {
	mu        sync.Mutex
	ChannelID string
	UserID    string
	Expires   time.Time
	Messages  []ai.Message
}

type learningManager struct {
	mu       sync.Mutex
	sessions map[string]*learningSession
	now      func() time.Time
}

func newLearningManager(now func() time.Time) *learningManager {
	if now == nil {
		now = time.Now
	}
	return &learningManager{sessions: make(map[string]*learningSession), now: now}
}

func (m *learningManager) start(channelID, userID string, ttl time.Duration) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[channelID] = &learningSession{
		ChannelID: channelID,
		UserID:    userID,
		Expires:   m.now().Add(ttl),
	}
}

func (m *learningManager) stop(channelID, userID string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[channelID]
	if session == nil || session.UserID != userID {
		return false
	}
	delete(m.sessions, channelID)
	return true
}

func (m *learningManager) active(channelID, userID string) (*learningSession, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[channelID]
	if session == nil {
		return nil, false
	}
	if !m.now().Before(session.Expires) {
		delete(m.sessions, channelID)
		return nil, false
	}
	if session.UserID != userID {
		return nil, false
	}
	return session, true
}

func (m *learningManager) status(channelID string) (*learningSession, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[channelID]
	if session == nil {
		return nil, false
	}
	if !m.now().Before(session.Expires) {
		delete(m.sessions, channelID)
		return nil, false
	}
	return session, true
}

func (s *learningSession) add(message ai.Message) {
	s.Messages = append(s.Messages, message)
	if len(s.Messages) > maxLearningTurns {
		s.Messages = s.Messages[len(s.Messages)-maxLearningTurns:]
	}
}

type soulProposal struct {
	ID        string
	ChannelID string
	UserID    string
	Summary   string
	Text      string
	Expires   time.Time
}

func soulUpdateTool() ai.Tool {
	return ai.Tool{
		Type: "function",
		Function: ai.ToolFunction{
			Name:        soulUpdateToolName,
			Description: "Propose the full revised SOUL for the developer to approve. Call this only after the developer agrees to a change. Safety rules are appended separately and must not be included.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string","description":"One or two sentences on what changed and why"},"soul":{"type":"string","description":"The full revised SOUL text"}},"required":["summary","soul"],"additionalProperties":false}`),
		},
	}
}

func learningSystemPrompt(soul string, maxChars int) string {
	return fmt.Sprintf(`You are helping the developer tune Raven's persona. You are not roleplaying with a sub and you do not need to speak as Raven.

Discuss the lesson, ask a clarifying question when the request is ambiguous, and when the developer agrees to a change, call soul_update with the full revised SOUL.

Keep the SOUL under %d characters. Preserve its voice, structure, existing rules, and any {{bot}} placeholder unless the developer asks to change them. Do not add safety rules or consent rules: those are fixed and appended separately. Never weaken limits, safewords, or the rule against claiming powers she does not have.

Current SOUL:
%s`, maxChars, soul)
}

// handleLearning answers the developer in an active learning session. It uses a
// meta prompt and a single soul_update tool, never the roleplay persona.
func (f *Feature) handleLearning(ctx context.Context, s *discordgo.Session, m *discordgo.MessageCreate, session *learningSession) error {
	if f == nil || f.soul == nil || f.auxChat == nil {
		f.send(s, m, "Learning mode is not configured.", 1)
		return nil
	}
	if m.Member == nil || m.Member.Permissions&(discordgo.PermissionAdministrator|discordgo.PermissionManageGuild) == 0 {
		f.send(s, m, "Learning mode is for server admins.", 1)
		return nil
	}
	if session == nil || session.UserID != m.Author.ID {
		return nil
	}
	session.mu.Lock()
	defer session.mu.Unlock()

	prompt := strings.TrimSpace(m.Content)
	if prompt == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, f.learningTimeout)
	defer cancel()

	stopTyping := f.startTyping(s, m.ChannelID)
	defer stopTyping()

	session.add(ai.Message{Role: "user", Content: prompt})
	messages := append([]ai.Message(nil), session.Messages...)
	system := learningSystemPrompt(f.soul.Text(), f.soulMaxChars)
	tools := []ai.Tool{soulUpdateTool()}

	var (
		text     string
		proposal *soulProposal
	)
	for round := 0; round < maxLearningToolRounds; round++ {
		reply, err := f.auxChat.ChatWithTools(ctx, system, messages, tools)
		if err != nil {
			f.log.Warn("learning request failed", "err", err, "user_id", session.UserID, "channel_id", session.ChannelID)
			f.send(s, m, "I lost that thought. Say it again and I will keep it.", 1)
			return nil
		}
		if len(reply.ToolCalls) == 0 {
			text = strings.TrimSpace(reply.Text)
			break
		}

		messages = append(messages, ai.Message{
			Role:      "assistant",
			Content:   reply.Text,
			ToolCalls: reply.ToolCalls,
		})
		for _, call := range reply.ToolCalls {
			result := "That tool is not available."
			if call.Function.Name == soulUpdateToolName {
				if proposal != nil {
					result = "A proposal is already waiting for approval."
				} else {
					staged, err := f.stageSoulProposal(session.ChannelID, session.UserID, call.Function.Arguments)
					if err != nil {
						result = err.Error()
					} else {
						proposal = staged
						result = "Proposal staged. The developer will approve or reject it."
					}
				}
			}
			messages = append(messages, ai.Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    result,
			})
		}
	}

	if text == "" {
		if proposal != nil {
			text = "I have drafted the change. Approve or reject it below."
		} else {
			text = "I could not settle that. Try rephrasing it."
		}
	}
	session.add(ai.Message{Role: "assistant", Content: text})
	f.send(s, m, text, f.maxChunks)
	if proposal != nil {
		f.sendSoulProposal(s, proposal)
	}
	return nil
}

func (f *Feature) stageSoulProposal(channelID, userID, arguments string) (*soulProposal, error) {
	if f == nil || f.soul == nil {
		return nil, errors.New("The SOUL is not configured.")
	}
	var args struct {
		Summary string `json:"summary"`
		Soul    string `json:"soul"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return nil, errors.New("Send the summary and the full revised SOUL.")
	}
	summary := strings.TrimSpace(args.Summary)
	if summary == "" {
		return nil, errors.New("Add a short summary of the change.")
	}
	text := strings.TrimSpace(args.Soul)
	if text == "" {
		return nil, errors.New("The proposed SOUL is empty.")
	}
	if err := f.soul.Validate(text); err != nil {
		return nil, err
	}

	f.proposalsMu.Lock()
	defer f.proposalsMu.Unlock()
	now := f.now()
	for id, existing := range f.proposals {
		if existing.ChannelID == channelID && now.Before(existing.Expires) {
			return nil, errors.New("A proposal is already waiting for approval.")
		}
		if !now.Before(existing.Expires) {
			delete(f.proposals, id)
		}
	}

	proposal := &soulProposal{
		ID:        newProposalID(),
		ChannelID: channelID,
		UserID:    userID,
		Summary:   summary,
		Text:      text,
		Expires:   now.Add(soulProposalTTL),
	}
	f.proposals[proposal.ID] = proposal
	return proposal, nil
}

func (f *Feature) peekSoulProposal(id string) (*soulProposal, bool) {
	f.proposalsMu.Lock()
	defer f.proposalsMu.Unlock()
	proposal := f.proposals[id]
	if proposal == nil || !f.now().Before(proposal.Expires) {
		delete(f.proposals, id)
		return nil, false
	}
	return proposal, true
}

func (f *Feature) removeSoulProposal(id string) {
	f.proposalsMu.Lock()
	defer f.proposalsMu.Unlock()
	delete(f.proposals, id)
}

func (f *Feature) sendSoulProposal(s *discordgo.Session, proposal *soulProposal) {
	if s == nil || proposal == nil {
		return
	}
	content := "**Proposed SOUL update**\n" + proposal.Summary + "\nThe proposed SOUL is attached. Approve or reject it below."
	components := []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				CustomID: "soul:approve:" + proposal.ID,
				Label:    "Approve",
				Style:    discordgo.SuccessButton,
			},
			discordgo.Button{
				CustomID: "soul:reject:" + proposal.ID,
				Label:    "Reject",
				Style:    discordgo.DangerButton,
			},
		}},
	}
	file := &discordgo.File{
		Name:        "proposed-SOUL.md",
		ContentType: "text/markdown",
		Reader:      strings.NewReader(proposal.Text),
	}
	_, err := s.ChannelMessageSendComplex(proposal.ChannelID, &discordgo.MessageSend{
		Content:    content,
		Components: components,
		Files:      []*discordgo.File{file},
	})
	if err != nil {
		f.log.Warn("sending soul proposal", "err", err, "channel_id", proposal.ChannelID)
		f.removeSoulProposal(proposal.ID)
	}
}

func (f *Feature) handleSoulComponent(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	data := i.MessageComponentData()
	parts := strings.SplitN(data.CustomID, ":", 3)
	if len(parts) != 3 || parts[0] != "soul" {
		return nil
	}
	action, id := parts[1], parts[2]
	proposal, ok := f.peekSoulProposal(id)
	if !ok {
		return bot.ReplyEphemeral(s, i, "That proposal expired. Ask me for a new one.")
	}
	userID := bot.InteractionUserID(i)
	if userID != proposal.UserID {
		return bot.ReplyEphemeral(s, i, "Only the developer who started this lesson can decide.")
	}
	if i.Member != nil && i.Member.Permissions&(discordgo.PermissionAdministrator|discordgo.PermissionManageGuild) == 0 {
		return bot.ReplyEphemeral(s, i, "You need **Manage Server** to change the SOUL.")
	}
	if f.soul == nil {
		return bot.ReplyEphemeral(s, i, "The SOUL is not configured.")
	}

	f.removeSoulProposal(id)
	switch action {
	case "approve":
		if err := f.soul.Replace(proposal.Text); err != nil {
			return bot.ReplyEphemeral(s, i, "I could not save that SOUL: "+err.Error())
		}
		return editComponentMessage(s, i, "Approved. The new SOUL is live.")
	case "reject":
		return editComponentMessage(s, i, "Rejected. The SOUL is unchanged.")
	default:
		return bot.ReplyEphemeral(s, i, "Unknown decision.")
	}
}

func editComponentMessage(s *discordgo.Session, i *discordgo.InteractionCreate, content string) error {
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    content,
			Components: []discordgo.MessageComponent{},
		},
	})
}

func newProposalID() string {
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes[:])
}
