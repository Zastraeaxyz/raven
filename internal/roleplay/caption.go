package roleplay

import (
	"encoding/json"
	"strings"

	"github.com/bwmarrin/discordgo"

	"raven/internal/ai"
)

const captionReactToolName = "caption_react"

// captionTarget is the image message that receives an emoji reaction.
type captionTarget struct {
	channelID string
	messageID string
}

// captionReactions maps the model's choice to the Unicode emoji Discord expects
// for a reaction.
var captionReactions = map[string]string{
	"heart_on_fire": "\u2764\uFE0F\u200D\U0001F525",
	"heart":         "\u2764\uFE0F",
	"heart_eyes":    "\U0001F60D",
	"smiling_imp":   "\U0001F608",
	"hot_face":      "\U0001F975",
}

var captionReactionNames = []string{
	"heart_on_fire",
	"heart",
	"heart_eyes",
	"smiling_imp",
	"hot_face",
}

// captionReactTool lets the model react to the caption image without putting
// the emoji in the chat reply. The meanings are part of the schema so the model
// picks the reaction that fits, not the one that looks friendly.
func captionReactTool() ai.Tool {
	return ai.Tool{
		Type: "function",
		Function: ai.ToolFunction{
			Name: captionReactToolName,
			Description: "React to the caption image with exactly one emoji. " +
				"heart for a good caption; heart_eyes when it earns more than a " +
				"heart; smiling_imp for a mean or strict keyholder; hot_face for " +
				"forced-bi or cleaning up a bull's cum from her pussy; " +
				"heart_on_fire for a wholesome but still strict keyholder.",
			Parameters: json.RawMessage(`{"type":"object","properties":{"emoji":{"type":"string","enum":["heart_on_fire","heart","heart_eyes","smiling_imp","hot_face"],"description":"The reaction that fits the caption"}},"required":["emoji"],"additionalProperties":false}`),
		},
	}
}

// reactToCaption validates the chosen emoji and adds it to the image message.
// A failure is reported to the model but never blocks the text reply.
func (f *Feature) reactToCaption(s *discordgo.Session, target *captionTarget, arguments string) string {
	if target == nil {
		return "There is no caption image to react to."
	}

	var args struct {
		Emoji string `json:"emoji"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "Choose one reaction emoji."
	}
	name := strings.ToLower(strings.TrimSpace(args.Emoji))
	emoji, ok := captionReactions[name]
	if !ok {
		return "Choose one of: " + strings.Join(captionReactionNames, ", ") + "."
	}
	if s == nil {
		return "The reaction could not be added."
	}

	if err := s.MessageReactionAdd(target.channelID, target.messageID, emoji); err != nil {
		f.log.Warn("adding caption reaction",
			"err", err,
			"channel_id", target.channelID,
			"message_id", target.messageID,
		)
		return "The reaction could not be added."
	}
	return "Reaction added."
}
