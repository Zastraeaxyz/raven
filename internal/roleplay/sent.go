package roleplay

import (
	"sync"
	"time"
)

// sentCache remembers the messages this process posted, so a reply to one of
// them can be recognized without fetching the referenced message from Discord.
type sentCache struct {
	mu       sync.Mutex
	ttl      time.Duration
	now      func() time.Time
	messages map[string]map[string]time.Time
}

func newSentCache(ttl time.Duration, now func() time.Time) *sentCache {
	if ttl <= 0 {
		ttl = time.Hour
	}
	if now == nil {
		now = time.Now
	}
	return &sentCache{
		ttl:      ttl,
		now:      now,
		messages: make(map[string]map[string]time.Time),
	}
}

func (c *sentCache) add(channelID, messageID string) {
	if c == nil || channelID == "" || messageID == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	channel, ok := c.messages[channelID]
	if !ok {
		channel = make(map[string]time.Time)
		c.messages[channelID] = channel
	}
	channel[messageID] = c.now().Add(c.ttl)

	if len(c.messages) > 1000 {
		c.prune()
	}
}

func (c *sentCache) has(channelID, messageID string) bool {
	if c == nil || channelID == "" || messageID == "" {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	channel, ok := c.messages[channelID]
	if !ok {
		return false
	}
	expires, ok := channel[messageID]
	if !ok {
		return false
	}
	if c.now().After(expires) {
		delete(channel, messageID)
		return false
	}
	return true
}

func (c *sentCache) prune() {
	now := c.now()
	for channelID, channel := range c.messages {
		for messageID, expires := range channel {
			if now.After(expires) {
				delete(channel, messageID)
			}
		}
		if len(channel) == 0 {
			delete(c.messages, channelID)
		}
	}
}
