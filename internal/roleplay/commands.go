package roleplay

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"raven/internal/bot"
)

func statusCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "chastity",
		Description: "Show your ChastiSafe lock status.",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "status",
				Description: "Show the locks where I am your keyholder.",
			},
		},
	}
}

func promptCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "prompt",
		Description: "Show the live persona SOUL.",
	}
}

func learningCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "learn",
		Description: "Tune Raven's SOUL in a private admin channel.",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "start", Description: "Start an interactive learning session here."},
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "stop", Description: "End the learning session here."},
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "status", Description: "Show the active learning session."},
		},
	}
}

func soulCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "soul",
		Description: "Manage Raven's SOUL.",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "undo", Description: "Restore the previous SOUL version."},
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "reset", Description: "Reset the SOUL to the repository seed."},
		},
	}
}

func memoryCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "memory",
		Description: "View or manage what Raven remembers.",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "view", Description: "Show what Raven remembers about you."},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "forget",
				Description: "Delete memory about you, or about a member as an admin.",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionBoolean, Name: "confirm", Description: "Confirm the deletion", Required: true},
					{Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "Admin only: delete another member's memory", Required: false},
				},
			},
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "list", Description: "Admin only: list members with memory."},
		},
	}
}

func aboutCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "about",
		Description: "Ask me who I am and what I enjoy.",
	}
}

// handleAboutCommand is public. It posts the authored introduction in the
// channel so new members meet her the same way everyone else did.
func (f *Feature) handleAboutCommand(_ context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return bot.ReplyPublic(s, i, f.aboutText())
}

// aboutText returns the authored introduction, or an in-character line when the
// file could not be loaded.
func (f *Feature) aboutText() string {
	if text := strings.TrimSpace(f.about); text != "" {
		return text
	}
	name := strings.TrimSpace(f.botName)
	if name == "" {
		name = "your keyholder"
	}
	return fmt.Sprintf("I am %s, and I decide who kneels here. Bring me a caption worth reading, and we will see what you are worth.", name)
}

// handlePromptCommand is admin-only. It shows the live SOUL and the fixed
// safety rules that are appended to every request.
func (f *Feature) handlePromptCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if err := bot.DeferEphemeral(s, i); err != nil {
		return err
	}

	content := strings.TrimSpace(f.personaText())
	if content == "" {
		content = "(empty)"
	}
	content += "\n\n---\n\nSafety rules appended at runtime:\n" + safetyPrompt
	if f.locks != nil {
		content += "\n\n---\n\nChastiSafe tools appended at runtime:\n" + chastityGuidance
	}
	if len([]rune(content)) <= 1900 {
		return bot.EditEphemeral(s, i, content)
	}

	file := &discordgo.File{
		Name:        "SOUL.md",
		ContentType: "text/markdown",
		Reader:      strings.NewReader(content),
	}
	note := "My live SOUL and safety rules are attached."
	_, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: &note,
		Files:   []*discordgo.File{file},
	})
	return err
}

func (f *Feature) handleStatusCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	data := i.ApplicationCommandData()
	if len(data.Options) == 0 || data.Options[0].Name != "status" {
		return bot.ReplyEphemeral(s, i, "Use `/chastity status`.")
	}

	userID := bot.InteractionUserID(i)
	if userID == "" {
		return bot.ReplyEphemeral(s, i, "I cannot tell who you are.")
	}

	if err := bot.DeferEphemeral(s, i); err != nil {
		return err
	}

	return bot.EditEphemeral(s, i, f.statusFor(ctx, userID))
}

func (f *Feature) statusFor(ctx context.Context, userID string) string {
	if f == nil || f.locks == nil {
		return "ChastiSafe is not configured yet."
	}

	locks, err := f.locksFor(ctx, userID)
	if err != nil {
		f.log.Warn("loading chastisafe status", "err", err, "user_id", userID)
		return "I cannot reach ChastiSafe right now. Try again in a moment."
	}
	if len(locks) == 0 {
		return f.connectText(ctx)
	}
	return statusText(locks)
}

func (f *Feature) handleLearningCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if !bot.CanUseBot(i) {
		return bot.ReplyEphemeral(s, i, "You need **Manage Server** to tune my SOUL.")
	}
	data := i.ApplicationCommandData()
	if len(data.Options) == 0 {
		return bot.ReplyEphemeral(s, i, "Use `/learn start`, `/learn stop`, or `/learn status`.")
	}
	userID := bot.InteractionUserID(i)
	if userID == "" {
		return bot.ReplyEphemeral(s, i, "I cannot tell who you are.")
	}
	if err := bot.DeferEphemeral(s, i); err != nil {
		return err
	}

	switch data.Options[0].Name {
	case "start":
		if f.soul == nil || f.auxChat == nil {
			return bot.EditEphemeral(s, i, "Learning mode is not configured.")
		}
		f.learning.start(i.ChannelID, userID, f.learningTTL)
		return bot.EditEphemeral(s, i, fmt.Sprintf(
			"Learning mode on for %d minutes. Talk to me here without a mention. I will propose SOUL edits and wait for your approval. Use `/learn stop` to end it.",
			int(f.learningTTL/time.Minute),
		))
	case "stop":
		if f.learning.stop(i.ChannelID, userID) {
			return bot.EditEphemeral(s, i, "Learning mode off.")
		}
		return bot.EditEphemeral(s, i, "No learning session of yours is active here.")
	case "status":
		session, ok := f.learning.status(i.ChannelID)
		if !ok {
			return bot.EditEphemeral(s, i, "No learning session is active here.")
		}
		remaining := session.Expires.Sub(f.now())
		if remaining < time.Minute {
			remaining = time.Minute
		}
		return bot.EditEphemeral(s, i, fmt.Sprintf(
			"Learning mode is active for %s with about %d minutes left.",
			session.UserID,
			int(remaining/time.Minute),
		))
	default:
		return bot.EditEphemeral(s, i, "Unknown subcommand.")
	}
}

func (f *Feature) handleSoulCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if !bot.CanUseBot(i) {
		return bot.ReplyEphemeral(s, i, "You need **Manage Server** to change my SOUL.")
	}
	data := i.ApplicationCommandData()
	if len(data.Options) == 0 {
		return bot.ReplyEphemeral(s, i, "Use `/soul undo` or `/soul reset`.")
	}
	if err := bot.DeferEphemeral(s, i); err != nil {
		return err
	}
	if f.soul == nil {
		return bot.EditEphemeral(s, i, "The SOUL is not configured.")
	}

	switch data.Options[0].Name {
	case "undo":
		if err := f.soul.Undo(); err != nil {
			return bot.EditEphemeral(s, i, "I could not restore an earlier SOUL: "+err.Error())
		}
		return bot.EditEphemeral(s, i, "Restored the previous SOUL. It is live now.")
	case "reset":
		if strings.TrimSpace(f.soulSeed) == "" {
			return bot.EditEphemeral(s, i, "No repository seed is available.")
		}
		if err := f.soul.Replace(f.soulSeed); err != nil {
			return bot.EditEphemeral(s, i, "I could not reset the SOUL: "+err.Error())
		}
		return bot.EditEphemeral(s, i, "Reset to the repository seed. It is live now. `/soul undo` restores the previous version.")
	default:
		return bot.EditEphemeral(s, i, "Use `/soul undo` or `/soul reset`.")
	}
}

func (f *Feature) handleMemoryCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	data := i.ApplicationCommandData()
	if len(data.Options) == 0 {
		return bot.ReplyEphemeral(s, i, "Use `/memory view`, `/memory forget`, or `/memory list`.")
	}
	if f.memory == nil {
		return bot.ReplyEphemeral(s, i, "Memory is not enabled.")
	}

	switch data.Options[0].Name {
	case "view":
		return f.handleMemoryView(s, i)
	case "forget":
		return f.handleMemoryForget(s, i, data.Options[0].Options)
	case "list":
		return f.handleMemoryList(s, i)
	default:
		return bot.ReplyEphemeral(s, i, "Unknown subcommand.")
	}
}

func (f *Feature) handleMemoryView(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	userID := bot.InteractionUserID(i)
	if userID == "" {
		return bot.ReplyEphemeral(s, i, "I cannot tell who you are.")
	}
	if err := bot.DeferEphemeral(s, i); err != nil {
		return err
	}
	raw, err := f.memory.raw(userID)
	if err != nil {
		return bot.EditEphemeral(s, i, "I could not read my memory right now.")
	}
	if strings.TrimSpace(raw) == "" {
		return bot.EditEphemeral(s, i, "I have no memory of you yet.")
	}
	if len([]rune(raw)) <= 1900 {
		return bot.EditEphemeral(s, i, raw)
	}
	file := &discordgo.File{
		Name:        "memory-" + userID + ".md",
		ContentType: "text/markdown",
		Reader:      strings.NewReader(raw),
	}
	note := "My memory of you is attached."
	_, err = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: &note,
		Files:   []*discordgo.File{file},
	})
	return err
}

func (f *Feature) handleMemoryForget(s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) error {
	userID := bot.InteractionUserID(i)
	if userID == "" {
		return bot.ReplyEphemeral(s, i, "I cannot tell who you are.")
	}
	target := userID
	confirm := false
	for _, opt := range opts {
		switch opt.Name {
		case "confirm":
			confirm = opt.BoolValue()
		case "user":
			if user := opt.UserValue(s); user != nil {
				target = user.ID
			}
		}
	}
	if target != userID && !bot.CanUseBot(i) {
		return bot.ReplyEphemeral(s, i, "Only an admin can forget another member's memory.")
	}
	if !confirm {
		return bot.ReplyEphemeral(s, i, "Run `/memory forget confirm:true` to delete it.")
	}

	if err := f.memory.forget(target); err != nil {
		return bot.ReplyEphemeral(s, i, "I could not forget that memory: "+err.Error())
	}
	if target == userID {
		return bot.ReplyEphemeral(s, i, "Forgotten. I have no memory of you now.")
	}
	return bot.ReplyEphemeral(s, i, fmt.Sprintf("Forgotten. I have no memory of %s now.", target))
}

func (f *Feature) handleMemoryList(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if !bot.CanUseBot(i) {
		return bot.ReplyEphemeral(s, i, "You need **Manage Server** to list memory.")
	}
	if err := bot.DeferEphemeral(s, i); err != nil {
		return err
	}
	summaries, err := f.memory.list()
	if err != nil {
		return bot.EditEphemeral(s, i, "I could not read the memory directory.")
	}
	if len(summaries) == 0 {
		return bot.EditEphemeral(s, i, "No memory files yet.")
	}

	var builder strings.Builder
	builder.WriteString("Memory files:\n")
	for _, summary := range summaries {
		updated := "unknown"
		if !summary.Updated.IsZero() {
			updated = summary.Updated.UTC().Format("2006-01-02")
		}
		name := summary.Name
		if name == "" {
			name = summary.ID
		}
		fmt.Fprintf(&builder, "- %s (%s), %s, %d chars\n", name, summary.ID, updated, summary.Chars)
	}
	return bot.EditEphemeral(s, i, truncateRunes(builder.String(), 1900))
}
