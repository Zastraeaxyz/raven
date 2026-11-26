package main

import (
	"os"
	"path/filepath"
	"testing"

	"raven/internal/config"
)

func TestLoadAbout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "about.md")
	if err := os.WriteFile(path, []byte("  I am {{bot}}.  \n"), 0o600); err != nil {
		t.Fatalf("writing about file: %v", err)
	}

	about, err := loadAbout(config.PersonaConfig{
		AboutFile: path,
		BotName:   "Raven",
	})
	if err != nil {
		t.Fatalf("loadAbout: %v", err)
	}
	if about != "I am Raven." {
		t.Fatalf("about = %q", about)
	}
}

func TestLoadAboutRejectsMissingAndEmpty(t *testing.T) {
	if _, err := loadAbout(config.PersonaConfig{
		AboutFile: filepath.Join(t.TempDir(), "missing.md"),
	}); err == nil {
		t.Fatal("expected an error for a missing file")
	}

	empty := filepath.Join(t.TempDir(), "empty.md")
	if err := os.WriteFile(empty, []byte("   \n"), 0o600); err != nil {
		t.Fatalf("writing about file: %v", err)
	}
	if _, err := loadAbout(config.PersonaConfig{AboutFile: empty}); err == nil {
		t.Fatal("expected an error for an empty file")
	}
}

func TestLoadPromptPrefersInline(t *testing.T) {
	prompt, err := loadPrompt(config.PersonaConfig{
		SystemPrompt:     "  You are {{bot}}.  ",
		SystemPromptFile: filepath.Join(t.TempDir(), "missing.md"),
	})
	if err != nil {
		t.Fatalf("loadPrompt: %v", err)
	}
	if prompt != "You are {{bot}}." {
		t.Fatalf("prompt = %q", prompt)
	}
}
