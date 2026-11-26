package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config is the runtime configuration loaded from the environment.
type Config struct {
	Token    string
	GuildID  string
	Port     string
	LogLevel slog.Level

	// OpenRouter powers the persona and its auxiliary model calls.
	OpenRouterAPIKey  string
	OpenRouterModel   string
	OpenRouterBaseURL string

	// Persona is the Discord bot identity and its behavior knobs.
	Persona PersonaConfig
}

// PersonaConfig configures the keyholder persona bot.
type PersonaConfig struct {
	BotName          string
	LockeeRoleID     string
	SystemPromptFile string
	SystemPrompt     string
	AboutFile        string
	ContextMessages  int
	MaxChunks        int
	Timeout          time.Duration
	MaxConcurrent    int
	RateLimitCount   int
	RateLimitWindow  time.Duration

	// Vision attaches Discord image attachments to the persona's prompt.
	VisionEnabled    bool
	VisionMaxImages  int
	VisionMaxImageMB int
	VisionDetail     string

	// Memory and SOUL persistence live on the data volume.
	MemoryEnabled      bool
	MemoryDir          string
	MemoryMaxChars     int
	MemoryContextChars int
	MemoryTimeout      time.Duration
	SoulMaxChars       int
	LearningTTL        time.Duration
	LearningTimeout    time.Duration
	AuxModel           string
	AuxMaxTokens       int

	// Punishment scales added lock time for a broken rule.
	PunishPercent int
	PunishMin     time.Duration
	PunishMax     time.Duration

	// Chastity enables the ChastiSafe tools.
	Chastity ChastityConfig
}

// ChastityConfig binds the persona to one ChastiSafe keyholder account.
type ChastityConfig struct {
	Enabled       bool
	KeyholderCode string
	BaseURL       string
}

const defaultOpenRouterModel = "deepseek/deepseek-v4.1-flash-1017"

// Load reads configuration from .env (when present) and the environment.
func Load() (*Config, error) {
	// Real environment variables win over .env values.
	_ = godotenv.Load()

	cfg := &Config{
		Token:             strings.TrimSpace(os.Getenv("DISCORD_TOKEN")),
		GuildID:           strings.TrimSpace(os.Getenv("GUILD_ID")),
		Port:              envOr("PORT", "8080"),
		LogLevel:          parseLevel(os.Getenv("LOG_LEVEL")),
		OpenRouterAPIKey:  strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")),
		OpenRouterModel:   envOr("OPENROUTER_MODEL", defaultOpenRouterModel),
		OpenRouterBaseURL: envOr("OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1"),
	}

	persona, err := loadPersona()
	if err != nil {
		return nil, err
	}
	cfg.Persona = persona

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// loadPersona reads the persona configuration. Every value has a safe default
// except the Discord token and the OpenRouter key.
func loadPersona() (PersonaConfig, error) {
	chastityEnabled, err := envBool("CHASTITY_ENABLED", true)
	if err != nil {
		return PersonaConfig{}, err
	}

	contextMessages, err := envInt("CONTEXT_MESSAGES", 15)
	if err != nil {
		return PersonaConfig{}, err
	}
	maxChunks, err := envInt("MAX_CHUNKS", 3)
	if err != nil {
		return PersonaConfig{}, err
	}
	timeout, err := envDuration("TIMEOUT_SECONDS", 60*time.Second)
	if err != nil {
		return PersonaConfig{}, err
	}
	maxConcurrent, err := envInt("MAX_CONCURRENT", 2)
	if err != nil {
		return PersonaConfig{}, err
	}
	rateLimitCount, err := envInt("RATE_LIMIT_CAPACITY", 2)
	if err != nil {
		return PersonaConfig{}, err
	}
	rateLimitWindow, err := envDuration("RATE_LIMIT_SECONDS", 10*time.Second)
	if err != nil {
		return PersonaConfig{}, err
	}
	punishPercent, err := envInt("PUNISH_PERCENT", 5)
	if err != nil {
		return PersonaConfig{}, err
	}
	punishMinMinutes, err := envInt("PUNISH_MIN_MINUTES", 30)
	if err != nil {
		return PersonaConfig{}, err
	}
	punishMaxMinutes, err := envInt("PUNISH_MAX_MINUTES", 240)
	if err != nil {
		return PersonaConfig{}, err
	}
	visionEnabled, err := envBool("VISION_ENABLED", true)
	if err != nil {
		return PersonaConfig{}, err
	}
	visionMaxImages, err := envInt("VISION_MAX_IMAGES", 4)
	if err != nil {
		return PersonaConfig{}, err
	}
	visionMaxImageMB, err := envInt("VISION_MAX_IMAGE_MB", 8)
	if err != nil {
		return PersonaConfig{}, err
	}
	memoryEnabled, err := envBool("MEMORY_ENABLED", true)
	if err != nil {
		return PersonaConfig{}, err
	}
	memoryMaxChars, err := envInt("MEMORY_MAX_CHARS", 3000)
	if err != nil {
		return PersonaConfig{}, err
	}
	memoryContextChars, err := envInt("MEMORY_CONTEXT_MAX_CHARS", 2500)
	if err != nil {
		return PersonaConfig{}, err
	}
	memoryTimeout, err := envDuration("MEMORY_TIMEOUT_SECONDS", 30*time.Second)
	if err != nil {
		return PersonaConfig{}, err
	}
	soulMaxChars, err := envInt("SOUL_MAX_CHARS", 8000)
	if err != nil {
		return PersonaConfig{}, err
	}
	learningMinutes, err := envInt("LEARNING_SESSION_MINUTES", 60)
	if err != nil {
		return PersonaConfig{}, err
	}
	learningTimeout, err := envDuration("LEARNING_TIMEOUT_SECONDS", 60*time.Second)
	if err != nil {
		return PersonaConfig{}, err
	}
	auxMaxTokens, err := envInt("AUX_MAX_TOKENS", 4096)
	if err != nil {
		return PersonaConfig{}, err
	}

	return PersonaConfig{
		BotName:            envOr("BOT_NAME", "Keyholder"),
		LockeeRoleID:       strings.TrimSpace(os.Getenv("LOCKEE_ROLE_ID")),
		SystemPromptFile:   envOr("SYSTEM_PROMPT_FILE", "./prompts/keyholder.md"),
		SystemPrompt:       strings.TrimSpace(os.Getenv("SYSTEM_PROMPT")),
		AboutFile:          envOr("ABOUT_FILE", "./prompts/about.md"),
		ContextMessages:    contextMessages,
		MaxChunks:          maxChunks,
		Timeout:            timeout,
		MaxConcurrent:      maxConcurrent,
		RateLimitCount:     rateLimitCount,
		RateLimitWindow:    rateLimitWindow,
		PunishPercent:      punishPercent,
		PunishMin:          time.Duration(punishMinMinutes) * time.Minute,
		PunishMax:          time.Duration(punishMaxMinutes) * time.Minute,
		VisionEnabled:      visionEnabled,
		VisionMaxImages:    visionMaxImages,
		VisionMaxImageMB:   visionMaxImageMB,
		VisionDetail:       envOr("VISION_DETAIL", "auto"),
		MemoryEnabled:      memoryEnabled,
		MemoryDir:          envOr("MEMORY_DIR", "./data"),
		MemoryMaxChars:     memoryMaxChars,
		MemoryContextChars: memoryContextChars,
		MemoryTimeout:      memoryTimeout,
		SoulMaxChars:       soulMaxChars,
		LearningTTL:        time.Duration(learningMinutes) * time.Minute,
		LearningTimeout:    learningTimeout,
		AuxModel:           strings.TrimSpace(os.Getenv("AUX_MODEL")),
		AuxMaxTokens:       auxMaxTokens,
		Chastity: ChastityConfig{
			Enabled:       chastityEnabled,
			KeyholderCode: strings.TrimSpace(os.Getenv("CHASTISAFE_KEYHOLDER_CODE")),
			BaseURL:       strings.TrimSpace(os.Getenv("CHASTISAFE_BASE_URL")),
		},
	}, nil
}

func (c *Config) validate() error {
	if c.Token == "" {
		return errors.New("DISCORD_TOKEN is required")
	}
	if c.GuildID != "" {
		if _, err := strconv.ParseUint(c.GuildID, 10, 64); err != nil {
			return fmt.Errorf("GUILD_ID %q is not a valid Discord snowflake", c.GuildID)
		}
	}
	if c.Port == "" {
		return errors.New("PORT must not be empty")
	}
	if c.OpenRouterAPIKey == "" {
		return errors.New("OPENROUTER_API_KEY is required")
	}
	return nil
}

// envInt reads a positive or zero integer environment variable.
func envInt(key string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s %q is not an integer", key, raw)
	}
	return value, nil
}

// envDuration reads a duration from a plain number of seconds.
func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s %q is not a number of seconds", key, raw)
	}
	return time.Duration(seconds) * time.Second, nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// envBool reads a boolean environment variable, falling back when unset.
func envBool(key string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("%s %q is not a boolean", key, raw)
	}
}

func parseLevel(raw string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
