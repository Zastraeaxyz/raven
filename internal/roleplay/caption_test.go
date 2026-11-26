package roleplay

import (
	"context"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"raven/internal/ai"
)

func TestCaptionTargetPrefersCurrentImage(t *testing.T) {
	f := visionFeature(t, 4, 8)
	current := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID:          "current",
		Attachments: []*discordgo.MessageAttachment{attachment("current.png", "image/png", 100, "https://cdn.discordapp.com/current.png")},
	}}
	referenced := &discordgo.Message{
		ID:          "referenced",
		Attachments: []*discordgo.MessageAttachment{attachment("referenced.png", "image/png", 100, "https://cdn.discordapp.com/referenced.png")},
	}

	target := f.captionTarget(current, referenced)
	if target == nil || target.messageID != "current" {
		t.Fatalf("target = %+v", target)
	}

	current.Attachments = nil
	target = f.captionTarget(current, referenced)
	if target == nil || target.messageID != "referenced" {
		t.Fatalf("target = %+v", target)
	}
}

func TestCaptionTargetSkipsUnusableImages(t *testing.T) {
	f := visionFeature(t, 4, 8)
	current := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID:          "current",
		Attachments: []*discordgo.MessageAttachment{attachment("notes.txt", "text/plain", 100, "https://cdn.discordapp.com/notes.txt")},
	}}
	if target := f.captionTarget(current, nil); target != nil {
		t.Fatalf("target = %+v", target)
	}

	f.visionEnabled = false
	current.Attachments = []*discordgo.MessageAttachment{attachment("image.png", "image/png", 100, "https://cdn.discordapp.com/image.png")}
	if target := f.captionTarget(current, nil); target != nil {
		t.Fatalf("target = %+v", target)
	}
}

func TestCaptionReactionsMatchDiscordEmoji(t *testing.T) {
	want := map[string]string{
		"heart_on_fire": "\u2764\uFE0F\u200D\U0001F525",
		"heart":         "\u2764\uFE0F",
		"heart_eyes":    "\U0001F60D",
		"smiling_imp":   "\U0001F608",
		"hot_face":      "\U0001F975",
	}
	for name, emoji := range want {
		if got := captionReactions[name]; got != emoji {
			t.Fatalf("%s = %q, want %q", name, got, emoji)
		}
	}
	if len(captionReactions) != len(captionReactionNames) {
		t.Fatalf("names = %v, reactions = %v", captionReactionNames, captionReactions)
	}
}

func TestCaptionReactToolListsEveryEmoji(t *testing.T) {
	tool := captionReactTool()
	if tool.Function.Name != captionReactToolName {
		t.Fatalf("tool name = %q", tool.Function.Name)
	}
	params := string(tool.Function.Parameters)
	for _, name := range captionReactionNames {
		if !strings.Contains(params, `"`+name+`"`) {
			t.Fatalf("parameters missing %q: %s", name, params)
		}
	}
}

func TestReactToCaptionValidatesInput(t *testing.T) {
	f := testFeature(t, nil)

	if got := f.reactToCaption(nil, nil, `{"emoji":"heart"}`); !strings.Contains(got, "no caption image") {
		t.Fatalf("nil target = %q", got)
	}
	target := &captionTarget{channelID: "channel", messageID: "message"}
	if got := f.reactToCaption(nil, target, `{"emoji":"heart"}`); !strings.Contains(got, "could not be added") {
		t.Fatalf("nil session = %q", got)
	}
	if got := f.reactToCaption(nil, target, `{"emoji":"thumbsup"}`); !strings.Contains(got, "Choose one of") {
		t.Fatalf("bad emoji = %q", got)
	}
	if got := f.reactToCaption(nil, target, `not json`); !strings.Contains(got, "Choose one reaction") {
		t.Fatalf("bad json = %q", got)
	}
}

func TestConverseOffersCaptionReactionOnlyWithAnImage(t *testing.T) {
	f := testFeature(t, nil)
	chat := &fakeChat{replies: []ai.Reply{{Text: "plain"}, {Text: "reacted"}}}
	f.chat = chat

	if _, err := f.converse(context.Background(), "sys", []ai.Message{{Role: "user", Content: "hi"}}, "user-1", nil, nil); err != nil {
		t.Fatalf("converse without image: %v", err)
	}
	if _, err := f.converse(context.Background(), "sys", []ai.Message{{Role: "user", Content: "hi"}}, "user-1", nil, &captionTarget{channelID: "channel", messageID: "message"}); err != nil {
		t.Fatalf("converse with image: %v", err)
	}

	chat.mu.Lock()
	defer chat.mu.Unlock()
	if hasTool(chat.calls[0].tools, captionReactToolName) {
		t.Fatal("caption reaction should not be offered without an image")
	}
	if !hasTool(chat.calls[1].tools, captionReactToolName) {
		t.Fatal("caption reaction should be offered with an image")
	}
}

func TestConverseRunsCaptionReaction(t *testing.T) {
	f := testFeature(t, nil)
	chat := &fakeChat{replies: []ai.Reply{
		{ToolCalls: []ai.ToolCall{{
			ID:       "call_1",
			Type:     "function",
			Function: ai.ToolCallFunction{Name: captionReactToolName, Arguments: `{"emoji":"hot_face"}`},
		}}},
		{Text: "That one got me."},
	}}
	f.chat = chat

	reply, err := f.converse(context.Background(), "sys", []ai.Message{{Role: "user", Content: "look"}}, "user-1", nil, &captionTarget{channelID: "channel", messageID: "message"})
	if err != nil {
		t.Fatalf("converse: %v", err)
	}
	if reply.Text != "That one got me." {
		t.Fatalf("reply = %+v", reply)
	}

	chat.mu.Lock()
	defer chat.mu.Unlock()
	if len(chat.calls) != 2 {
		t.Fatalf("chat calls = %d", len(chat.calls))
	}
	last := chat.calls[1].messages[len(chat.calls[1].messages)-1]
	if last.Role != "tool" || last.ToolCallID != "call_1" || !strings.Contains(last.Content, "could not be added") {
		t.Fatalf("last message = %+v", last)
	}
}

func hasTool(tools []ai.Tool, name string) bool {
	for _, tool := range tools {
		if tool.Function.Name == name {
			return true
		}
	}
	return false
}
