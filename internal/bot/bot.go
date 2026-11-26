package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Options configures a Bot.
type Options struct {
	Token   string
	GuildID string
	Log     *slog.Logger

	// Intents selects the gateway events the session subscribes to. Zero uses
	// IntentsGuilds, which is all the command-driven features need.
	Intents discordgo.Intent
}

// CommandHandler runs a slash command. Handlers are responsible for
// acknowledging the interaction.
type CommandHandler func(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error

// ComponentHandler runs a message component interaction (buttons, selects).
type ComponentHandler func(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error

// ModalHandler runs a modal submission. The custom ID namespace works the same
// way as AddComponent.
type ModalHandler func(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error

// AutocompleteHandler answers suggestions for a slash command option.
type AutocompleteHandler func(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error

// MessageHandler runs for every guild message the session receives. Features
// that need message content must ask for the matching intents in Options.
type MessageHandler func(ctx context.Context, s *discordgo.Session, m *discordgo.MessageCreate) error

// ReadyHook runs after every gateway READY event.
type ReadyHook func(ctx context.Context, s *discordgo.Session) error

// UserRoleLookup returns the role ID that may run commands registered with
// AddUserCommand. An empty ID means no role is configured yet.
type UserRoleLookup func(ctx context.Context) (string, error)

// Bot owns the Discord session and dispatches interactions to features.
type Bot struct {
	Session *discordgo.Session
	Log     *slog.Logger

	guildID string

	mu                sync.RWMutex
	commands          map[string]*discordgo.ApplicationCommand
	commandHandlers   map[string]CommandHandler
	userCommands      map[string]struct{}
	publicCommands    map[string]struct{}
	userRole          UserRoleLookup
	autocompletes     map[string]AutocompleteHandler
	componentHandlers map[string]ComponentHandler
	modalHandlers     map[string]ModalHandler
	messageHandlers   []MessageHandler
	readyHooks        []ReadyHook
	commandsSynced    bool
}

// New creates a bot session. The token is validated against the REST API in
// Run so an invalid token fails fast instead of reconnect-looping.
func New(opts Options) (*Bot, error) {
	if opts.Token == "" {
		return nil, errors.New("discord token is empty")
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}

	s, err := discordgo.New("Bot " + opts.Token)
	if err != nil {
		return nil, fmt.Errorf("creating discord session: %w", err)
	}
	intents := opts.Intents
	if intents == 0 {
		intents = discordgo.IntentsGuilds
	}
	s.Identify.Intents = intents
	s.LogLevel = discordgo.LogWarning
	s.ShouldReconnectOnError = true

	b := &Bot{
		Session:           s,
		Log:               opts.Log,
		guildID:           opts.GuildID,
		commands:          make(map[string]*discordgo.ApplicationCommand),
		commandHandlers:   make(map[string]CommandHandler),
		userCommands:      make(map[string]struct{}),
		publicCommands:    make(map[string]struct{}),
		autocompletes:     make(map[string]AutocompleteHandler),
		componentHandlers: make(map[string]ComponentHandler),
		modalHandlers:     make(map[string]ModalHandler),
	}
	s.AddHandler(b.handleReady)
	s.AddHandler(b.handleInteraction)
	s.AddHandler(b.handleMessageCreate)
	return b, nil
}

// AddCommand registers a slash command and its handler.
func (b *Bot) AddCommand(cmd *discordgo.ApplicationCommand, handler CommandHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.commands[cmd.Name] = cmd
	b.commandHandlers[cmd.Name] = handler
}

// AddUserCommand registers a slash command that members may run when they hold
// the configured user command role. Server admins always pass. The role is read
// per invocation, so changing it takes effect without a restart.
func (b *Bot) AddUserCommand(cmd *discordgo.ApplicationCommand, handler CommandHandler) {
	b.AddCommand(cmd, handler)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.userCommands[cmd.Name] = struct{}{}
}

// AddPublicCommand registers a slash command that every member may run.
func (b *Bot) AddPublicCommand(cmd *discordgo.ApplicationCommand, handler CommandHandler) {
	b.AddCommand(cmd, handler)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.publicCommands[cmd.Name] = struct{}{}
}

// SetUserRoleLookup installs the source of the user command role. Without it,
// user commands stay in admin-only mode.
func (b *Bot) SetUserRoleLookup(lookup UserRoleLookup) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.userRole = lookup
}

// AddComponent registers a component handler for a custom ID namespace, for
// example "welcome" handles "welcome:ack". Namespacing keeps buttons working
// across restarts without any in-memory routing state.
func (b *Bot) AddComponent(namespace string, handler ComponentHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.componentHandlers[namespace] = handler
}

// AddModal registers a modal handler for a custom ID namespace, for example
// "chastisafe" handles "chastisafe:login".
func (b *Bot) AddModal(namespace string, handler ModalHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.modalHandlers[namespace] = handler
}

// AddAutocomplete registers suggestions for a command's options.
func (b *Bot) AddAutocomplete(commandName string, handler AutocompleteHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.autocompletes[commandName] = handler
}

// AddMessageHandler registers work that runs for every guild message. Handlers
// run concurrently so a slow model call never blocks the gateway. Each handler
// gets its own timeout context.
func (b *Bot) AddMessageHandler(handler MessageHandler) {
	if handler == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.messageHandlers = append(b.messageHandlers, handler)
}

// AddReadyHook registers work that must be re-validated on every reconnect.
func (b *Bot) AddReadyHook(hook ReadyHook) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.readyHooks = append(b.readyHooks, hook)
}

// Run validates the token, connects the gateway, and blocks until ctx is done.
func (b *Bot) Run(ctx context.Context) error {
	me, err := b.Session.User("@me")
	if err != nil {
		return fmt.Errorf("validating bot token: %w", err)
	}
	b.Log.Info("authenticated with discord",
		"user", me.Username,
		"user_id", me.ID,
		"guild_id", b.guildIDValue(),
	)

	if err := b.Session.Open(); err != nil {
		return fmt.Errorf("opening gateway: %w", err)
	}
	defer b.Session.Close()
	b.Log.Info("gateway connected")

	<-ctx.Done()
	b.Log.Info("gateway shutting down")
	return nil
}

func (b *Bot) handleReady(s *discordgo.Session, r *discordgo.Ready) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	b.Log.Info("discord ready",
		"user", r.User.Username,
		"guilds", len(r.Guilds),
	)

	b.detectGuild(r)

	if b.needsCommandSync() {
		if err := b.syncCommands(s); err != nil {
			// Leaving the flag unset retries on the next READY event.
			b.Log.Error("syncing application commands", "err", err)
		} else {
			b.mu.Lock()
			b.commandsSynced = true
			b.mu.Unlock()
		}
	}

	b.mu.RLock()
	hooks := append([]ReadyHook(nil), b.readyHooks...)
	b.mu.RUnlock()

	for _, hook := range hooks {
		if err := hook(ctx, s); err != nil {
			b.Log.Error("ready hook failed", "err", err)
		}
	}
}

const messageHandlerTimeout = 2 * time.Minute

// handleMessageCreate fans a message out to every registered handler. The
// handlers run in their own goroutines so gateway heartbeats stay responsive
// while a feature calls a model.
func (b *Bot) handleMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m == nil || m.Message == nil {
		return
	}

	b.mu.RLock()
	handlers := append([]MessageHandler(nil), b.messageHandlers...)
	b.mu.RUnlock()

	for _, handler := range handlers {
		go func(handler MessageHandler) {
			defer func() {
				if recovered := recover(); recovered != nil {
					b.Log.Error("message handler panicked", "panic", recovered)
				}
			}()

			ctx, cancel := context.WithTimeout(context.Background(), messageHandlerTimeout)
			defer cancel()
			if err := handler(ctx, s, m); err != nil {
				b.Log.Error("message handler failed", "err", err)
			}
		}(handler)
	}
}

// detectGuild lets a bot without a configured guild use the only guild it is
// in. That keeps a dedicated persona bot working without another ID to copy.
func (b *Bot) detectGuild(r *discordgo.Ready) {
	if r == nil || len(r.Guilds) != 1 {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.guildID != "" {
		return
	}
	b.guildID = r.Guilds[0].ID
	b.Log.Info("using the only guild", "guild_id", b.guildID)
}

func (b *Bot) guildIDValue() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.guildID
}

func (b *Bot) needsCommandSync() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return !b.commandsSynced
}

func (b *Bot) syncCommands(s *discordgo.Session) error {
	b.mu.RLock()
	cmds := make([]*discordgo.ApplicationCommand, 0, len(b.commands))
	for _, cmd := range b.commands {
		cmds = append(cmds, cmd)
	}
	b.mu.RUnlock()

	sort.Slice(cmds, func(i, j int) bool { return cmds[i].Name < cmds[j].Name })

	appID := s.State.User.ID
	guildID := b.guildIDValue()
	if guildID != "" {
		_, err := s.ApplicationCommandBulkOverwrite(appID, guildID, cmds)
		if err != nil {
			return fmt.Errorf("overwriting guild commands: %w", err)
		}
		b.Log.Info("registered guild commands", "count", len(cmds), "guild_id", guildID)
		return nil
	}

	_, err := s.ApplicationCommandBulkOverwrite(appID, "", cmds)
	if err != nil {
		return fmt.Errorf("overwriting global commands: %w", err)
	}
	b.Log.Info("registered global commands", "count", len(cmds))
	return nil
}

// CanUseBot reports whether the interaction comes from a server admin.
// Component handlers such as the welcome button are intentionally not gated,
// because they are meant for every member.
func CanUseBot(i *discordgo.InteractionCreate) bool {
	if i.Member == nil {
		return false
	}
	perms := i.Member.Permissions
	return perms&(discordgo.PermissionAdministrator|discordgo.PermissionManageGuild) != 0
}

// commandDenial returns an empty string when the member may run the command,
// or the reason to show when they may not. Admins always pass. Commands
// registered with AddUserCommand also pass for members holding the role that
// /settings command-role saved.
func (b *Bot) commandDenial(ctx context.Context, i *discordgo.InteractionCreate, name string) string {
	if CanUseBot(i) {
		return ""
	}

	b.mu.RLock()
	_, publicCommand := b.publicCommands[name]
	_, userCommand := b.userCommands[name]
	lookup := b.userRole
	b.mu.RUnlock()

	if publicCommand {
		return ""
	}
	if !userCommand {
		return "You need the **Manage Server** permission to use this command."
	}
	if lookup == nil {
		return "Member commands are not available until an admin chooses a role with `/settings command-role`."
	}

	roleID, err := lookup(ctx)
	if err != nil {
		b.Log.Error("looking up the user command role", "err", err)
		return "I could not check permissions. Try again in a moment."
	}
	if roleID == "" {
		return "An admin needs to choose a member command role with `/settings command-role`."
	}
	if i.Member == nil || !hasRoleID(i.Member.Roles, roleID) {
		return fmt.Sprintf("You need the <@&%s> role to use this command.", roleID)
	}
	return ""
}

func hasRoleID(roles []string, want string) bool {
	if want == "" {
		return false
	}
	for _, id := range roles {
		if id == want {
			return true
		}
	}
	return false
}

func InteractionUserID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

// subcommandName returns the subcommand path of a slash command, or an empty
// string for a command without subcommands.
func subcommandName(i *discordgo.InteractionCreate) string {
	opts := i.ApplicationCommandData().Options
	if len(opts) == 0 {
		return ""
	}
	switch opts[0].Type {
	case discordgo.ApplicationCommandOptionSubCommand:
		return opts[0].Name
	case discordgo.ApplicationCommandOptionSubCommandGroup:
		if len(opts[0].Options) == 0 {
			return opts[0].Name
		}
		return opts[0].Name + " " + opts[0].Options[0].Name
	default:
		return ""
	}
}

func (b *Bot) handleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionPing &&
		i.Type != discordgo.InteractionApplicationCommand &&
		i.Type != discordgo.InteractionApplicationCommandAutocomplete &&
		i.Type != discordgo.InteractionMessageComponent &&
		i.Type != discordgo.InteractionModalSubmit {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	switch i.Type {
	case discordgo.InteractionPing:
		respondErr := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponsePong,
		})
		if respondErr != nil {
			b.Log.Warn("replying to ping", "err", respondErr)
		}

	case discordgo.InteractionApplicationCommand:
		name := i.ApplicationCommandData().Name
		if denial := b.commandDenial(ctx, i, name); denial != "" {
			b.Log.Warn("rejected command",
				"command", name,
				"user_id", InteractionUserID(i),
				"reason", denial,
			)
			if err := ReplyEphemeral(s, i, denial); err != nil {
				b.Log.Warn("replying to unauthorized user", "err", err)
			}
			return
		}
		b.mu.RLock()
		handler := b.commandHandlers[name]
		b.mu.RUnlock()
		if handler == nil {
			b.Log.Warn("unhandled command", "command", name)
			return
		}
		b.Log.Info("command run",
			"command", name,
			"subcommand", subcommandName(i),
			"user_id", InteractionUserID(i),
			"guild_id", i.GuildID,
			"channel_id", i.ChannelID,
		)
		if err := handler(ctx, s, i); err != nil {
			b.handleError(s, i, err)
		}

	case discordgo.InteractionApplicationCommandAutocomplete:
		name := i.ApplicationCommandData().Name
		if denial := b.commandDenial(ctx, i, name); denial != "" {
			b.Log.Warn("rejected autocomplete",
				"command", name,
				"user_id", InteractionUserID(i),
				"reason", denial,
			)
			if err := RespondAutocomplete(s, i, []*discordgo.ApplicationCommandOptionChoice{}); err != nil {
				b.Log.Warn("replying to rejected autocomplete", "err", err)
			}
			return
		}
		b.mu.RLock()
		handler := b.autocompletes[name]
		b.mu.RUnlock()
		if handler == nil {
			b.Log.Warn("unhandled autocomplete", "command", name)
			return
		}
		if err := handler(ctx, s, i); err != nil {
			b.Log.Error("autocomplete failed", "command", name, "err", err)
		}

	case discordgo.InteractionMessageComponent:
		customID := i.MessageComponentData().CustomID
		handler := b.componentHandler(customID)
		if handler == nil {
			b.Log.Warn("unhandled component", "custom_id", customID)
			return
		}
		if err := handler(ctx, s, i); err != nil {
			b.handleError(s, i, err)
		}

	case discordgo.InteractionModalSubmit:
		customID := i.ModalSubmitData().CustomID
		handler := b.modalHandler(customID)
		if handler == nil {
			b.Log.Warn("unhandled modal", "custom_id", customID)
			return
		}
		if err := handler(ctx, s, i); err != nil {
			b.handleError(s, i, err)
		}
	}
}

// componentHandler finds a component handler by full custom ID or namespace.
func (b *Bot) componentHandler(customID string) ComponentHandler {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if handler := b.componentHandlers[customID]; handler != nil {
		return handler
	}
	if idx := strings.Index(customID, ":"); idx > 0 {
		return b.componentHandlers[customID[:idx]]
	}
	return nil
}

// modalHandler finds a modal handler by full custom ID or namespace.
func (b *Bot) modalHandler(customID string) ModalHandler {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if handler := b.modalHandlers[customID]; handler != nil {
		return handler
	}
	if idx := strings.Index(customID, ":"); idx > 0 {
		return b.modalHandlers[customID[:idx]]
	}
	return nil
}

func (b *Bot) handleError(s *discordgo.Session, i *discordgo.InteractionCreate, err error) {
	b.Log.Error("interaction failed",
		"type", i.Type.String(),
		"err", err,
	)

	msg := "Something went wrong on my side. Try again in a moment."
	if editErr := EditEphemeral(s, i, msg); editErr == nil {
		return
	}
	if _, followErr := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: msg,
		Flags:   discordgo.MessageFlagsEphemeral,
	}); followErr == nil {
		return
	}
	if replyErr := ReplyEphemeral(s, i, msg); replyErr != nil {
		b.Log.Warn("could not report interaction error to user", "err", replyErr)
	}
}
