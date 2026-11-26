package roleplay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSoulSeedsAndResolvesName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SOUL.md")
	soul, err := NewSoul(path, "You are {{bot}}.", "Raven", 1000, time.Now)
	if err != nil {
		t.Fatalf("NewSoul: %v", err)
	}
	if got := soul.Text(); got != "You are Raven." {
		t.Fatalf("Text = %q", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading seeded soul: %v", err)
	}
	if !strings.Contains(string(data), "{{bot}}") {
		t.Fatalf("seed should keep the placeholder: %q", data)
	}
}

func TestSoulLoadsExistingFileOverSeed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SOUL.md")
	if err := os.WriteFile(path, []byte("An older soul."), 0o600); err != nil {
		t.Fatalf("writing soul: %v", err)
	}
	soul, err := NewSoul(path, "Seed.", "Raven", 1000, time.Now)
	if err != nil {
		t.Fatalf("NewSoul: %v", err)
	}
	if got := soul.Text(); got != "An older soul." {
		t.Fatalf("Text = %q", got)
	}
}

func TestSoulReplaceAndUndo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SOUL.md")
	soul, err := NewSoul(path, "First soul.", "Raven", 1000, time.Now)
	if err != nil {
		t.Fatalf("NewSoul: %v", err)
	}
	if err := soul.Replace("Second soul."); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if got := soul.Text(); got != "Second soul." {
		t.Fatalf("Text = %q", got)
	}
	if err := soul.Undo(); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if got := soul.Text(); got != "First soul." {
		t.Fatalf("Text after undo = %q", got)
	}
}

func TestSoulValidation(t *testing.T) {
	soul, err := NewSoul("", "Short.", "Raven", 10, time.Now)
	if err != nil {
		t.Fatalf("NewSoul: %v", err)
	}
	if err := soul.Validate(""); err == nil {
		t.Fatal("expected an empty text error")
	}
	if err := soul.Validate(strings.Repeat("a", 11)); err == nil {
		t.Fatal("expected a length error")
	}
	if err := soul.Replace(strings.Repeat("a", 11)); err == nil {
		t.Fatal("Replace should reject oversized text")
	}
}

func TestSoulUndoWithoutHistory(t *testing.T) {
	soul, err := NewSoul(filepath.Join(t.TempDir(), "SOUL.md"), "Only soul.", "Raven", 100, time.Now)
	if err != nil {
		t.Fatalf("NewSoul: %v", err)
	}
	if err := soul.Undo(); err == nil {
		t.Fatal("expected an undo error with no history")
	}
}
