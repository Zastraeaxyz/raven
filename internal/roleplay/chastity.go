package roleplay

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/zastraeaxyz/chastisafe-go"
	"raven/internal/ai"
)

// chastityGuidance is appended to the persona prompt only when ChastiSafe is
// configured. It keeps the lock feature in the model's toolbelt instead of in
// every reply.
const chastityGuidance = `## Tools (added by the bot)
You have tools for the real ChastiSafe lock of the person you are talking to:
- chastity_status reads their lock.
- chastity_punish adds time to their timed lock when they break a house rule. The bot chooses the amount, so never invent one.
- chastity_freeze freezes or thaws their timed lock.

Use these only when chastity, locks, keys, or obedience are already part of the conversation. Do not announce a check, and do not bring up cages or keys unprompted. When a house rule is broken, call chastity_punish directly and never invent an amount. You can add time and freeze. You cannot unlock a lock.`

// chastityTools is the tool schema the model sees.
func chastityTools() []ai.Tool {
	return []ai.Tool{
		{
			Type: "function",
			Function: ai.ToolFunction{
				Name:        "chastity_status",
				Description: "Read the caller's ChastiSafe lock status. Use it only when the conversation already involves chastity, locks, keys, or their account.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			},
		},
		{
			Type: "function",
			Function: ai.ToolFunction{
				Name:        "chastity_punish",
				Description: "Add time to the caller's timed lock as punishment for breaking a house rule. The bot picks the amount and checks the lock itself, so call this directly. Use only after a clear rule break.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"reason":{"type":"string","description":"The broken rule, in a few words"}},"additionalProperties":false}`),
			},
		},
		{
			Type: "function",
			Function: ai.ToolFunction{
				Name:        "chastity_freeze",
				Description: "Freeze or thaw the caller's timed lock.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"frozen":{"type":"boolean","description":"True to freeze, false to thaw"}},"required":["frozen"],"additionalProperties":false}`),
			},
		},
	}
}

// lockeesCache holds the keyholder's lockee list for a short time so a burst
// of messages does not hit ChastiSafe once per message.
type lockeesCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	now     func() time.Time
	expires time.Time
	locks   []chastisafe.LoadedLock
	valid   bool
}

func newLockeesCache(ttl time.Duration, now func() time.Time) *lockeesCache {
	return &lockeesCache{ttl: ttl, now: now}
}

func (c *lockeesCache) all(ctx context.Context, fetch func(context.Context) ([]chastisafe.LoadedLock, error)) ([]chastisafe.LoadedLock, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.valid && c.now().Before(c.expires) {
		return c.locks, nil
	}

	locks, err := fetch(ctx)
	if err != nil {
		return nil, err
	}
	c.locks = locks
	c.expires = c.now().Add(c.ttl)
	c.valid = true
	return locks, nil
}

func (c *lockeesCache) invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.valid = false
	c.locks = nil
}

// templateCache keeps the keyholder's offered template names around. Template
// edits are rare, so a longer cache is fine.
type templateCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	now     func() time.Time
	expires time.Time
	names   []string
	valid   bool
}

func newTemplateCache(ttl time.Duration, now func() time.Time) *templateCache {
	return &templateCache{ttl: ttl, now: now}
}

func (c *templateCache) load(ctx context.Context, source LockSource, keyholderID string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.valid && c.now().Before(c.expires) {
		return c.names
	}

	templates, err := source.LoadableByKeyholder(ctx, keyholderID)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(templates))
	for _, template := range templates {
		name := strings.TrimSpace(template.LockName)
		if name == "" || len(names) >= 10 {
			continue
		}
		names = append(names, name)
	}
	c.names = names
	c.expires = c.now().Add(c.ttl)
	c.valid = true
	return names
}

// locksFor returns only the locks where the Discord member is the lockee. The
// keyholder list can hold every sub, so filtering happens here, not at the
// API.
func (f *Feature) locksFor(ctx context.Context, userID string) ([]chastisafe.LoadedLock, error) {
	if f.locks == nil {
		return nil, fmt.Errorf("chastity is disabled")
	}
	all, err := f.lockCache.all(ctx, f.locks.Lockees)
	if err != nil {
		return nil, err
	}

	locks := make([]chastisafe.LoadedLock, 0, len(all))
	for _, lock := range all {
		if lock.LockeeID.String() != userID {
			continue
		}
		locks = append(locks, lock)
	}
	return locks, nil
}

// statusText renders lock summaries for a member. It never includes a
// combination, and LockSummary does not read one.
func statusText(locks []chastisafe.LoadedLock) string {
	if len(locks) == 0 {
		return ""
	}

	var b strings.Builder
	if len(locks) == 1 {
		b.WriteString("**Your lock**\n")
	} else {
		b.WriteString("**Your locks**\n")
	}
	for i := range locks {
		b.WriteString("- ")
		b.WriteString(chastisafe.LockSummary(&locks[i]))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// connectText explains how a lockee adds the persona as their keyholder in the
// ChastiSafe app. There is no API for a keyholder to attach a lockee; the
// lockee starts that relationship from their side.
func (f *Feature) connectText(ctx context.Context) string {
	if f.keyholder == nil || f.keyholder.ID == "" {
		return "ChastiSafe is not set up yet."
	}

	var b strings.Builder
	b.WriteString("You have no active lock where I am the keyholder.\n\n")
	b.WriteString("Add me as your keyholder in ChastiSafe:\n")
	name := strings.TrimSpace(f.keyholder.User)
	if name != "" {
		fmt.Fprintf(&b, "**%s**", name)
	}
	fmt.Fprintf(&b, " (ID `%s`)\n", f.keyholder.ID)

	if f.locks != nil {
		if names := f.templates.load(ctx, f.locks, f.keyholder.ID.String()); len(names) > 0 {
			b.WriteString("\nTemplates I offer: ")
			b.WriteString(strings.Join(names, ", "))
			b.WriteString("\n")
		}
	}

	b.WriteString("\nThen load a lock. I will see it here.")
	return b.String()
}

// runTool executes a model tool call and returns a short result for the
// model. Results never include lock combinations.
func (f *Feature) runTool(ctx context.Context, userID, name, arguments string) string {
	switch name {
	case "chastity_status":
		return f.toolStatus(ctx, userID)
	case "chastity_punish":
		return f.toolPunish(ctx, userID)
	case "chastity_freeze":
		var args struct {
			Frozen *bool `json:"frozen"`
		}
		if strings.TrimSpace(arguments) == "" {
			return "I need to know whether to freeze or thaw."
		}
		if err := json.Unmarshal([]byte(arguments), &args); err != nil || args.Frozen == nil {
			return "I need to know whether to freeze or thaw."
		}
		return f.toolFreeze(ctx, userID, *args.Frozen)
	default:
		return "That tool is not available."
	}
}

func (f *Feature) toolStatus(ctx context.Context, userID string) string {
	if f.locks == nil {
		return "ChastiSafe is not connected."
	}

	locks, err := f.locksFor(ctx, userID)
	if err != nil {
		f.log.Warn("loading chastisafe status for a tool call", "err", err, "user_id", userID)
		return "Lock status is unavailable right now."
	}
	if len(locks) == 0 {
		return "No active lock. They can become your pet by loading one of your timed lock templates in ChastiSafe and choosing you as keyholder. They can run /chastity status for the connection details."
	}
	return "Current lock status:\n" + statusText(locks)
}

// punishmentDuration scales added time with the remaining lock. Short locks
// get a meaningful floor, long locks keep the increase modest.
func (f *Feature) punishmentDuration(remaining time.Duration) time.Duration {
	add := time.Duration(float64(remaining) * float64(f.punishPercent) / 100)
	if add < f.punishMin {
		add = f.punishMin
	}
	if add > f.punishMax {
		add = f.punishMax
	}
	return add.Round(time.Minute)
}

func firstTimerLock(locks []chastisafe.LoadedLock) *chastisafe.LoadedLock {
	for i := range locks {
		if locks[i].IsTimerLock() {
			return &locks[i]
		}
	}
	return nil
}

func (f *Feature) toolPunish(ctx context.Context, userID string) string {
	if f.locks == nil {
		return "ChastiSafe is not connected."
	}

	locks, err := f.locksFor(ctx, userID)
	if err != nil {
		f.log.Warn("loading chastisafe status for a punishment", "err", err, "user_id", userID)
		return "The lock is unreachable right now."
	}
	lock := firstTimerLock(locks)
	if lock == nil {
		return "There is no timed lock to punish."
	}

	add := f.punishmentDuration(lock.Remaining())
	updated, err := f.locks.AddTime(ctx, lock.ID, add)
	if err != nil {
		f.log.Warn("adding lock time", "err", err, "user_id", userID, "lock_id", lock.ID.String())
		return "The lock did not accept the punishment."
	}
	f.lockCache.invalidate()

	remaining := lock.Remaining() + add
	if updated != nil {
		remaining = updated.Remaining()
	}
	f.log.Info("punishment applied",
		"user_id", userID,
		"lock_id", lock.ID.String(),
		"added", add.String(),
		"remaining", remaining.String(),
	)
	return fmt.Sprintf("Punishment applied: %s added. %s remaining.", chastisafe.FormatDuration(add), chastisafe.FormatDuration(remaining))
}

func (f *Feature) toolFreeze(ctx context.Context, userID string, frozen bool) string {
	if f.locks == nil {
		return "ChastiSafe is not connected."
	}

	locks, err := f.locksFor(ctx, userID)
	if err != nil {
		f.log.Warn("loading chastisafe status for a freeze", "err", err, "user_id", userID)
		return "The lock is unreachable right now."
	}
	lock := firstTimerLock(locks)
	if lock == nil {
		return "There is no timed lock to freeze."
	}

	if _, err := f.locks.Freeze(ctx, lock.ID, frozen); err != nil {
		f.log.Warn("freezing lock", "err", err, "user_id", userID, "lock_id", lock.ID.String(), "frozen", frozen)
		return "The lock did not respond."
	}
	f.lockCache.invalidate()

	if frozen {
		return "Lock frozen."
	}
	return "Lock thawed."
}
