package bot

import "github.com/bwmarrin/discordgo"

// ReplyEphemeral sends a new ephemeral reply. Use it only before the
// interaction has been acknowledged.
func ReplyEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) error {
	return ReplyEphemeralWithComponents(s, i, content, nil)
}

// ReplyEphemeralWithComponents sends an ephemeral reply with buttons or other
// components. A nil slice sends no components.
func ReplyEphemeralWithComponents(s *discordgo.Session, i *discordgo.InteractionCreate, content string, components []discordgo.MessageComponent) error {
	data := &discordgo.InteractionResponseData{
		Content: content,
		Flags:   discordgo.MessageFlagsEphemeral,
	}
	if components != nil {
		data.Components = components
	}
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: data,
	})
}

// DeferEphemeral acknowledges the interaction and buys time to do work.
func DeferEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
}

// DeferPublic acknowledges the interaction with a visible reply, for work
// whose result the whole channel should see.
func DeferPublic(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})
}

// ReplyPublic sends a new public reply. Use it only before the interaction
// has been acknowledged.
func ReplyPublic(s *discordgo.Session, i *discordgo.InteractionCreate, content string) error {
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: content},
	})
}

// EditEphemeral replaces a deferred reply with content.
func EditEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) error {
	return EditEphemeralWithComponents(s, i, content, nil)
}

// EditEphemeralWithComponents replaces a deferred reply and sets its
// components. Pass an empty non-nil slice to remove existing buttons; pass nil
// to leave them unchanged.
func EditEphemeralWithComponents(s *discordgo.Session, i *discordgo.InteractionCreate, content string, components []discordgo.MessageComponent) error {
	edit := &discordgo.WebhookEdit{Content: &content}
	if components != nil {
		edit.Components = &components
	}
	_, err := s.InteractionResponseEdit(i.Interaction, edit)
	return err
}

// RespondAutocomplete sends autocomplete suggestions for an option.
func RespondAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate, choices []*discordgo.ApplicationCommandOptionChoice) error {
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{
			Choices: choices,
		},
	})
}
