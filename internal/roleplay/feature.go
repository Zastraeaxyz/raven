// Package roleplay runs the optional persona bot that shares a process with
// Raven. It answers mentions and replies from members, optionally limited to
// one Discord role, and can check their ChastiSafe status with a tool.
package roleplay

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/zastraeaxyz/chastisafe-go"
	"raven/internal/ai"
	"raven/internal/bot"
)

// ChatClient is the model surface the feature needs.
type ChatClient interface {
	Chat(ctx context.Context, system string, messages []ai.Message) (ai.Reply, error)
	ChatWithTools(ctx context.Context, system string, messages []ai.Message, tools []ai.Tool) (ai.Reply, error)
}

// LockSource is the read-only ChastiSafe surface used for status. A nil source
// disables the ChastiSafe block entirely.
type LockSource interface {
	Lockees(ctx context.Context) ([]chastisafe.LoadedLock, error)
	LoadableByKeyholder(ctx context.Context, keyholderID string) ([]chastisafe.Template, error)
}

// LockService is the ChastiSafe surface used for status and keyholder actions.
type LockService interface {
	LockSource
	AddTime(ctx context.Context, lockID chastisafe.ID, delta time.Duration) (*chastisafe.LoadedLock, error)
	Freeze(ctx context.Context, lockID chastisafe.ID, frozen bool) (*chastisafe.LoadedLock, error)
}

// Options configures the roleplay feature.
type Options struct {
	GuildID      string
	BotName      string
	LockeeRoleID string
	SystemPrompt string
	About        string

	ContextMessages int
	MaxChunks       int
	Timeout         time.Duration
	MaxConcurrent   int

	RateLimitCount  int
	RateLimitWindow time.Duration

	// Vision attaches Discord-hosted image attachments to the model request.
	VisionEnabled    bool
	VisionMaxImages  int
	VisionMaxImageMB int
	VisionDetail     string

	// Punishment scales added lock time for a broken rule.
	PunishPercent int
	PunishMin     time.Duration
	PunishMax     time.Duration

	// Soul, Memory, and AuxChat turn on persistent memory and learning.
	Soul            *Soul
	Memory          *MemoryStore
	AuxChat         ChatClient
	MemoryTimeout   time.Duration
	SoulMaxChars    int
	LearningTTL     time.Duration
	LearningTimeout time.Duration

	Chat      ChatClient
	Locks     LockService
	Keyholder *chastisafe.Identity
	Log       *slog.Logger
}

// Feature implements the persona bot.
type Feature struct {
	guildID      string
	botName      string
	lockeeRoleID string
	basePrompt   string
	about        string
	tools        []ai.Tool

	soul            *Soul
	soulSeed        string
	memory          *MemoryStore
	auxChat         ChatClient
	memoryTimeout   time.Duration
	memorySem       chan struct{}
	soulMaxChars    int
	learning        *learningManager
	learningTTL     time.Duration
	learningTimeout time.Duration

	proposalsMu sync.Mutex
	proposals   map[string]*soulProposal

	contextMessages int
	maxChunks       int
	timeout         time.Duration

	visionEnabled   bool
	visionMaxImages int
	visionMaxBytes  int64
	visionDetail    string

	chat      ChatClient
	locks     LockService
	keyholder *chastisafe.Identity
	log       *slog.Logger

	punishPercent int
	punishMin     time.Duration
	punishMax     time.Duration

	sem       chan struct{}
	limiter   *limiter
	lockCache *lockeesCache
	templates *templateCache
	sent      *sentCache
	now       func() time.Time
}

// New builds the roleplay feature. Missing limits fall back to safe defaults.
func New(opts Options) *Feature {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.BotName == "" {
		opts.BotName = "Keyholder"
	}
	if opts.ContextMessages < 1 {
		opts.ContextMessages = 15
	}
	if opts.MaxChunks < 1 {
		opts.MaxChunks = 3
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 60 * time.Second
	}
	if opts.MaxConcurrent < 1 {
		opts.MaxConcurrent = 2
	}
	if opts.RateLimitCount < 1 {
		opts.RateLimitCount = 2
	}
	if opts.RateLimitWindow <= 0 {
		opts.RateLimitWindow = 10 * time.Second
	}
	if opts.PunishPercent < 1 || opts.PunishPercent > 100 {
		opts.PunishPercent = 5
	}
	if opts.PunishMin <= 0 {
		opts.PunishMin = 30 * time.Minute
	}
	if opts.PunishMax <= 0 {
		opts.PunishMax = 4 * time.Hour
	}
	if opts.PunishMax < opts.PunishMin {
		opts.PunishMax = opts.PunishMin
	}
	if opts.VisionMaxImages < 1 {
		opts.VisionMaxImages = 4
	}
	if opts.VisionMaxImageMB < 1 {
		opts.VisionMaxImageMB = 8
	}
	switch strings.ToLower(strings.TrimSpace(opts.VisionDetail)) {
	case "auto", "low", "high", "original":
		opts.VisionDetail = strings.ToLower(strings.TrimSpace(opts.VisionDetail))
	default:
		opts.VisionDetail = "auto"
	}
	if opts.MemoryTimeout <= 0 {
		opts.MemoryTimeout = 30 * time.Second
	}
	if opts.SoulMaxChars < 1 {
		opts.SoulMaxChars = defaultSoulMaxChars
	}
	if opts.LearningTTL <= 0 {
		opts.LearningTTL = 60 * time.Minute
	}
	if opts.LearningTimeout <= 0 {
		opts.LearningTimeout = 60 * time.Second
	}
	now := time.Now
	prompt := strings.ReplaceAll(opts.SystemPrompt, "{{bot}}", opts.BotName)
	var tools []ai.Tool
	if opts.Locks != nil {
		tools = chastityTools()
	}

	f := &Feature{
		guildID:         opts.GuildID,
		botName:         opts.BotName,
		lockeeRoleID:    opts.LockeeRoleID,
		basePrompt:      prompt,
		about:           strings.TrimSpace(opts.About),
		tools:           tools,
		soul:            opts.Soul,
		soulSeed:        opts.SystemPrompt,
		memory:          opts.Memory,
		auxChat:         opts.AuxChat,
		memoryTimeout:   opts.MemoryTimeout,
		soulMaxChars:    opts.SoulMaxChars,
		learning:        newLearningManager(now),
		learningTTL:     opts.LearningTTL,
		learningTimeout: opts.LearningTimeout,
		proposals:       make(map[string]*soulProposal),
		contextMessages: opts.ContextMessages,
		maxChunks:       opts.MaxChunks,
		timeout:         opts.Timeout,

		visionEnabled:   opts.VisionEnabled,
		visionMaxImages: opts.VisionMaxImages,
		visionMaxBytes:  int64(opts.VisionMaxImageMB) * 1024 * 1024,
		visionDetail:    opts.VisionDetail,

		chat:          opts.Chat,
		locks:         opts.Locks,
		keyholder:     opts.Keyholder,
		log:           opts.Log,
		punishPercent: opts.PunishPercent,
		punishMin:     opts.PunishMin,
		punishMax:     opts.PunishMax,
		sem:           make(chan struct{}, opts.MaxConcurrent),
		limiter:       newLimiter(opts.RateLimitCount, opts.RateLimitWindow, now),
		lockCache:     newLockeesCache(30*time.Second, now),
		templates:     newTemplateCache(5*time.Minute, now),
		sent:          newSentCache(time.Hour, now),
		now:           now,
	}
	if f.memory != nil && f.auxChat != nil {
		f.memorySem = make(chan struct{}, 2)
	}
	return f
}

// Register wires the feature into a bot.
func (f *Feature) Register(b *bot.Bot) {
	if b == nil {
		return
	}
	if f.lockeeRoleID != "" {
		b.AddUserCommand(statusCommand(), f.handleStatusCommand)
	} else {
		b.AddPublicCommand(statusCommand(), f.handleStatusCommand)
	}
	b.AddCommand(promptCommand(), f.handlePromptCommand)
	b.AddCommand(learningCommand(), f.handleLearningCommand)
	b.AddCommand(soulCommand(), f.handleSoulCommand)
	b.AddPublicCommand(aboutCommand(), f.handleAboutCommand)
	b.AddPublicCommand(memoryCommand(), f.handleMemoryCommand)
	b.AddComponent("soul", f.handleSoulComponent)
	b.AddMessageHandler(f.handleMessage)
}

// String describes the feature without exposing any configuration secrets.
func (f *Feature) String() string {
	return fmt.Sprintf("roleplay(bot=%q, chastity=%t, memory=%t, learning=%t)",
		f.botName, f.locks != nil, f.memory != nil, f.auxChat != nil)
}

func (f *Feature) acquire(ctx context.Context) (func(), error) {
	select {
	case f.sem <- struct{}{}:
		return func() { <-f.sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Second):
		return nil, fmt.Errorf("roleplay is busy")
	}
}
