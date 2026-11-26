package roleplay

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"raven/internal/ai"
)

func TestLearningSessionsExpire(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	manager := newLearningManager(func() time.Time { return now })
	manager.start("channel", "user", time.Hour)

	if session, ok := manager.active("channel", "user"); !ok || session.UserID != "user" {
		t.Fatalf("active session = %+v, %v", session, ok)
	}
	if _, ok := manager.active("channel", "other"); ok {
		t.Fatal("another member should not join the session")
	}

	now = now.Add(61 * time.Minute)
	if _, ok := manager.active("channel", "user"); ok {
		t.Fatal("expired session should not be active")
	}
}

func TestLearningSessionKeepsRecentTurns(t *testing.T) {
	session := &learningSession{}
	for i := 0; i < maxLearningTurns+5; i++ {
		session.add(ai.Message{Role: "user", Content: "turn"})
	}
	if len(session.Messages) != maxLearningTurns {
		t.Fatalf("messages = %d, want %d", len(session.Messages), maxLearningTurns)
	}
}

func TestStageSoulProposal(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	f := testFeature(t, nil)
	f.now = func() time.Time { return now }
	soul, err := NewSoul("", "You are {{bot}}.", "Mistress", 1000, f.now)
	if err != nil {
		t.Fatalf("NewSoul: %v", err)
	}
	f.soul = soul

	proposal, err := f.stageSoulProposal("channel", "user", `{"summary":"Make her softer.","soul":"You are Mistress, and you are gentle."}`)
	if err != nil {
		t.Fatalf("stageSoulProposal: %v", err)
	}
	if proposal == nil || proposal.Summary == "" {
		t.Fatalf("proposal = %+v", proposal)
	}
	if _, err := f.stageSoulProposal("channel", "user", `{"summary":"Again.","soul":"You are Mistress."}`); err == nil {
		t.Fatal("expected a pending-proposal error")
	}

	if _, err := f.stageSoulProposal("other", "user", `{"summary":"Empty.","soul":""}`); err == nil {
		t.Fatal("expected an empty-soul error")
	}
	if _, err := f.stageSoulProposal("other", "user", `{"summary":"Big.","soul":"`+strings.Repeat("a", 1001)+`"}`); err == nil {
		t.Fatal("expected an oversized-soul error")
	}
}

func TestSoulProposalExpires(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	f := testFeature(t, nil)
	f.now = func() time.Time { return now }
	soul, _ := NewSoul("", "Seed.", "Mistress", 1000, f.now)
	f.soul = soul

	proposal, err := f.stageSoulProposal("channel", "user", `{"summary":"Change.","soul":"New soul."}`)
	if err != nil {
		t.Fatalf("stageSoulProposal: %v", err)
	}
	now = now.Add(soulProposalTTL + time.Minute)
	if _, ok := f.peekSoulProposal(proposal.ID); ok {
		t.Fatal("proposal should be expired")
	}
}

func TestLearningSystemPromptCarriesSoul(t *testing.T) {
	prompt := learningSystemPrompt("You are Raven.", 4000)
	for _, want := range []string{"You are Raven.", "soul_update", "safety"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want %q", prompt, want)
		}
	}
}

func TestSoulUpdateToolSchema(t *testing.T) {
	tool := soulUpdateTool()
	if tool.Function.Name != soulUpdateToolName {
		t.Fatalf("name = %q", tool.Function.Name)
	}
	var schema map[string]any
	if err := json.Unmarshal(tool.Function.Parameters, &schema); err != nil {
		t.Fatalf("parameters: %v", err)
	}
	props := schema["properties"].(map[string]any)
	if _, ok := props["summary"]; !ok {
		t.Fatal("summary parameter missing")
	}
	if _, ok := props["soul"]; !ok {
		t.Fatal("soul parameter missing")
	}
}

func TestSystemPromptIncludesMemoryAndSafety(t *testing.T) {
	store := testMemoryStore(t)
	if err := store.append("123", "Alice", nil, []string{"Prefers being called pet."}); err != nil {
		t.Fatalf("append: %v", err)
	}
	f := testFeature(t, nil)
	f.memory = store
	f.soul, _ = NewSoul("", "You are {{bot}} and you enjoy captions.", "Mistress", 1000, f.now)

	prompt := f.systemPrompt("123", "Alice")
	for _, want := range []string{"You are Mistress", "Prefers being called pet.", "Limits and safewords end the scene immediately."} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want %q", prompt, want)
		}
	}
	if strings.Contains(prompt, "{{bot}}") {
		t.Fatalf("bot placeholder was not resolved: %q", prompt)
	}
}
