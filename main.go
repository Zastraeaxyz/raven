package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/zastraeaxyz/chastisafe-go"

	"raven/internal/ai"
	"raven/internal/bot"
	"raven/internal/config"
	"raven/internal/roleplay"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))
	slog.SetDefault(log)

	log.Info("starting raven",
		"guild_id", cfg.GuildID,
		"memory_dir", cfg.Persona.MemoryDir,
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := start(ctx, cfg, log); err != nil {
		return err
	}

	health := newHealthServer(cfg.Port, log)
	health.start()
	defer health.shutdown()

	<-ctx.Done()
	return nil
}

// start connects the persona bot. A missing prompt or an invalid ChastiSafe
// code disables only that feature.
func start(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	rp := cfg.Persona

	if rp.LockeeRoleID != "" {
		if _, err := strconv.ParseUint(rp.LockeeRoleID, 10, 64); err != nil {
			return fmt.Errorf("LOCKEE_ROLE_ID %q is not a valid Discord snowflake", rp.LockeeRoleID)
		}
	}

	prompt, err := loadPrompt(rp)
	if err != nil {
		return err
	}

	about, err := loadAbout(rp)
	if err != nil {
		log.Warn("about text unavailable", "err", err)
	}

	chat, err := ai.New(ai.Options{
		APIKey:    cfg.OpenRouterAPIKey,
		BaseURL:   cfg.OpenRouterBaseURL,
		Model:     cfg.OpenRouterModel,
		AppName:   "Raven",
		MaxTokens: 1024,
		Timeout:   rp.Timeout,
	})
	if err != nil {
		return fmt.Errorf("building the chat client: %w", err)
	}

	auxModel := strings.TrimSpace(rp.AuxModel)
	if auxModel == "" {
		auxModel = cfg.OpenRouterModel
	}
	auxChat, auxErr := ai.New(ai.Options{
		APIKey:    cfg.OpenRouterAPIKey,
		BaseURL:   cfg.OpenRouterBaseURL,
		Model:     auxModel,
		AppName:   "Raven",
		MaxTokens: rp.AuxMaxTokens,
		Timeout:   rp.LearningTimeout,
	})
	if auxErr != nil {
		log.Warn("memory and learning disabled", "err", auxErr)
	}

	soul, err := roleplay.NewSoul(
		filepath.Join(rp.MemoryDir, "SOUL.md"),
		prompt,
		rp.BotName,
		rp.SoulMaxChars,
		time.Now,
	)
	if err != nil {
		return fmt.Errorf("loading the SOUL: %w", err)
	}

	var memory *roleplay.MemoryStore
	if rp.MemoryEnabled {
		memory, err = roleplay.NewMemoryStore(
			filepath.Join(rp.MemoryDir, "memory"),
			rp.MemoryMaxChars,
			rp.MemoryContextChars,
			time.Now,
		)
		if err != nil {
			return fmt.Errorf("opening memory: %w", err)
		}
	}

	var (
		locks     roleplay.LockService
		keyholder *chastisafe.Identity
	)
	switch {
	case !rp.Chastity.Enabled:
		log.Info("chastisafe status disabled by configuration")
	case rp.Chastity.KeyholderCode == "":
		log.Warn("chastisafe status disabled: CHASTISAFE_KEYHOLDER_CODE is empty")
	default:
		client, err := chastisafe.New(chastisafe.Options{
			Token:     rp.Chastity.KeyholderCode,
			BaseURL:   rp.Chastity.BaseURL,
			UserAgent: "raven/1",
			Logger:    log,
		})
		if err != nil {
			log.Warn("chastisafe status disabled", "err", err)
			break
		}

		whoCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		identity, whoErr := client.Who(whoCtx)
		cancel()
		if whoErr != nil {
			log.Warn("chastisafe status disabled: cannot load the keyholder account", "err", whoErr)
			break
		}

		locks = client
		keyholder = identity
		log.Info("chastisafe keyholder ready",
			"keyholder", identity.User,
			"keyholder_id", identity.ID.String(),
		)
	}

	b, err := bot.New(bot.Options{
		Token:   cfg.Token,
		GuildID: cfg.GuildID,
		Log:     log,
		Intents: discordgo.IntentsGuilds |
			discordgo.IntentsGuildMessages |
			discordgo.IntentMessageContent,
	})
	if err != nil {
		return fmt.Errorf("creating the bot: %w", err)
	}

	roleplay.New(roleplay.Options{
		GuildID:          cfg.GuildID,
		BotName:          rp.BotName,
		LockeeRoleID:     rp.LockeeRoleID,
		SystemPrompt:     prompt,
		About:            about,
		ContextMessages:  rp.ContextMessages,
		MaxChunks:        rp.MaxChunks,
		Timeout:          rp.Timeout,
		MaxConcurrent:    rp.MaxConcurrent,
		RateLimitCount:   rp.RateLimitCount,
		RateLimitWindow:  rp.RateLimitWindow,
		PunishPercent:    rp.PunishPercent,
		PunishMin:        rp.PunishMin,
		PunishMax:        rp.PunishMax,
		VisionEnabled:    rp.VisionEnabled,
		VisionMaxImages:  rp.VisionMaxImages,
		VisionMaxImageMB: rp.VisionMaxImageMB,
		VisionDetail:     rp.VisionDetail,
		Soul:             soul,
		Memory:           memory,
		AuxChat:          auxChat,
		MemoryTimeout:    rp.MemoryTimeout,
		SoulMaxChars:     rp.SoulMaxChars,
		LearningTTL:      rp.LearningTTL,
		LearningTimeout:  rp.LearningTimeout,
		Chat:             chat,
		Locks:            locks,
		Keyholder:        keyholder,
		Log:              log,
	}).Register(b)

	b.SetUserRoleLookup(func(context.Context) (string, error) {
		return rp.LockeeRoleID, nil
	})

	log.Info("persona bot starting",
		"name", rp.BotName,
		"model", cfg.OpenRouterModel,
		"chastity", locks != nil,
		"memory", memory != nil,
		"learning", auxChat != nil,
	)

	return b.Run(ctx)
}

// loadPrompt prefers the environment override and otherwise reads the persona
// file from disk.
func loadPrompt(rp config.PersonaConfig) (string, error) {
	if prompt := strings.TrimSpace(rp.SystemPrompt); prompt != "" {
		return prompt, nil
	}

	data, err := os.ReadFile(rp.SystemPromptFile)
	if err != nil {
		return "", fmt.Errorf("reading the system prompt %q: %w", rp.SystemPromptFile, err)
	}
	prompt := strings.TrimSpace(string(data))
	if prompt == "" {
		return "", fmt.Errorf("the system prompt %q is empty", rp.SystemPromptFile)
	}
	return prompt, nil
}

// loadAbout reads the self introduction served by /about. A missing file
// disables only the greeting text, and the command keeps an in-character
// fallback.
func loadAbout(rp config.PersonaConfig) (string, error) {
	data, err := os.ReadFile(rp.AboutFile)
	if err != nil {
		return "", fmt.Errorf("reading the about file %q: %w", rp.AboutFile, err)
	}
	about := strings.TrimSpace(string(data))
	if about == "" {
		return "", fmt.Errorf("the about file %q is empty", rp.AboutFile)
	}
	return strings.ReplaceAll(about, "{{bot}}", rp.BotName), nil
}

type healthServer struct {
	srv *http.Server
	log *slog.Logger
}

func newHealthServer(port string, log *slog.Logger) *healthServer {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "raven is running\n")
	})

	return &healthServer{
		srv: &http.Server{
			Addr:              ":" + port,
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
		},
		log: log,
	}
}

func (h *healthServer) start() {
	go func() {
		if err := h.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			h.log.Warn("health server stopped", "err", err)
		}
	}()
}

func (h *healthServer) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.srv.Shutdown(ctx); err != nil {
		h.log.Warn("shutting down health server", "err", err)
	}
}
