package config

import (
	"strings"
	"testing"
	"time"
)

// clearPersonaEnv keeps the developer's real environment out of the tests.
func clearPersonaEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"GUILD_ID", "LOCKEE_ROLE_ID", "OPENROUTER_API_KEY", "OPENROUTER_MODEL",
		"OPENROUTER_BASE_URL", "CHASTITY_ENABLED", "CHASTISAFE_KEYHOLDER_CODE",
		"CHASTISAFE_BASE_URL", "PUNISH_PERCENT", "PUNISH_MIN_MINUTES",
		"PUNISH_MAX_MINUTES", "VISION_ENABLED", "VISION_MAX_IMAGES",
		"VISION_MAX_IMAGE_MB", "VISION_DETAIL", "ABOUT_FILE", "MEMORY_ENABLED",
		"MEMORY_DIR", "MEMORY_MAX_CHARS", "MEMORY_CONTEXT_MAX_CHARS",
		"MEMORY_TIMEOUT_SECONDS", "SOUL_MAX_CHARS", "LEARNING_SESSION_MINUTES",
		"LEARNING_TIMEOUT_SECONDS", "AUX_MODEL", "AUX_MAX_TOKENS", "BOT_NAME",
		"CONTEXT_MESSAGES", "MAX_CHUNKS", "TIMEOUT_SECONDS", "MAX_CONCURRENT",
		"RATE_LIMIT_CAPACITY", "RATE_LIMIT_SECONDS", "SYSTEM_PROMPT_FILE",
		"SYSTEM_PROMPT", "PORT",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearPersonaEnv(t)
	t.Setenv("DISCORD_TOKEN", "token")
	t.Setenv("OPENROUTER_API_KEY", "key")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Persona.BotName != "Keyholder" {
		t.Fatalf("BotName = %q", cfg.Persona.BotName)
	}
	if cfg.Persona.ContextMessages != 15 || cfg.Persona.MaxChunks != 3 {
		t.Fatalf("limits = %+v", cfg.Persona)
	}
	if cfg.OpenRouterModel != defaultOpenRouterModel {
		t.Fatalf("OpenRouterModel = %q", cfg.OpenRouterModel)
	}
	if cfg.Persona.PunishPercent != 5 ||
		cfg.Persona.PunishMin != 30*time.Minute ||
		cfg.Persona.PunishMax != 4*time.Hour {
		t.Fatalf("punishment defaults = %+v", cfg.Persona)
	}
	if !cfg.Persona.VisionEnabled ||
		cfg.Persona.VisionMaxImages != 4 ||
		cfg.Persona.VisionMaxImageMB != 8 ||
		cfg.Persona.VisionDetail != "auto" {
		t.Fatalf("vision defaults = %+v", cfg.Persona)
	}
	if cfg.Persona.AboutFile != "./prompts/about.md" {
		t.Fatalf("AboutFile = %q", cfg.Persona.AboutFile)
	}
	if !cfg.Persona.MemoryEnabled ||
		cfg.Persona.MemoryDir != "./data" ||
		cfg.Persona.MemoryMaxChars != 3000 ||
		cfg.Persona.MemoryContextChars != 2500 ||
		cfg.Persona.MemoryTimeout != 30*time.Second ||
		cfg.Persona.SoulMaxChars != 8000 ||
		cfg.Persona.LearningTTL != time.Hour ||
		cfg.Persona.LearningTimeout != 60*time.Second ||
		cfg.Persona.AuxMaxTokens != 4096 {
		t.Fatalf("memory defaults = %+v", cfg.Persona)
	}
}

func TestLoadRequiresTokenAndOpenRouterKey(t *testing.T) {
	clearPersonaEnv(t)
	t.Setenv("DISCORD_TOKEN", "")
	t.Setenv("OPENROUTER_API_KEY", "key")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "DISCORD_TOKEN") {
		t.Fatalf("expected a DISCORD_TOKEN error, got %v", err)
	}

	t.Setenv("DISCORD_TOKEN", "token")
	t.Setenv("OPENROUTER_API_KEY", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "OPENROUTER_API_KEY") {
		t.Fatalf("expected an OPENROUTER_API_KEY error, got %v", err)
	}
}

func TestLoadRejectsInvalidGuildID(t *testing.T) {
	clearPersonaEnv(t)
	t.Setenv("DISCORD_TOKEN", "token")
	t.Setenv("OPENROUTER_API_KEY", "key")
	t.Setenv("GUILD_ID", "not-a-snowflake")

	if _, err := Load(); err == nil {
		t.Fatal("expected an error for an invalid GUILD_ID")
	}
}

func TestLoadMemoryOverrides(t *testing.T) {
	clearPersonaEnv(t)
	t.Setenv("DISCORD_TOKEN", "token")
	t.Setenv("OPENROUTER_API_KEY", "key")
	t.Setenv("MEMORY_ENABLED", "false")
	t.Setenv("MEMORY_DIR", "/tmp/raven-memory")
	t.Setenv("MEMORY_MAX_CHARS", "1200")
	t.Setenv("MEMORY_CONTEXT_MAX_CHARS", "900")
	t.Setenv("SOUL_MAX_CHARS", "4000")
	t.Setenv("LEARNING_SESSION_MINUTES", "30")
	t.Setenv("AUX_MODEL", "test/model")
	t.Setenv("AUX_MAX_TOKENS", "2048")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Persona.MemoryEnabled ||
		cfg.Persona.MemoryDir != "/tmp/raven-memory" ||
		cfg.Persona.MemoryMaxChars != 1200 ||
		cfg.Persona.MemoryContextChars != 900 ||
		cfg.Persona.SoulMaxChars != 4000 ||
		cfg.Persona.LearningTTL != 30*time.Minute ||
		cfg.Persona.AuxModel != "test/model" ||
		cfg.Persona.AuxMaxTokens != 2048 {
		t.Fatalf("memory overrides = %+v", cfg.Persona)
	}
}

func TestLoadVisionOverrides(t *testing.T) {
	clearPersonaEnv(t)
	t.Setenv("DISCORD_TOKEN", "token")
	t.Setenv("OPENROUTER_API_KEY", "key")
	t.Setenv("VISION_ENABLED", "false")
	t.Setenv("VISION_MAX_IMAGES", "2")
	t.Setenv("VISION_MAX_IMAGE_MB", "3")
	t.Setenv("VISION_DETAIL", "high")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Persona.VisionEnabled ||
		cfg.Persona.VisionMaxImages != 2 ||
		cfg.Persona.VisionMaxImageMB != 3 ||
		cfg.Persona.VisionDetail != "high" {
		t.Fatalf("vision = %+v", cfg.Persona)
	}
}

func TestLoadChastityDefaults(t *testing.T) {
	clearPersonaEnv(t)
	t.Setenv("DISCORD_TOKEN", "token")
	t.Setenv("OPENROUTER_API_KEY", "key")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Persona.Chastity.Enabled {
		t.Fatal("chastity should default to on")
	}
}
