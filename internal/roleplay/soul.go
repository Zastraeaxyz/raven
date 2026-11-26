package roleplay

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultSoulMaxChars = 8000
	soulHistoryLimit    = 10
)

// Soul is the live persona file. The copy on disk wins over the repository
// seed, so learning edits survive deploys.
type Soul struct {
	mu       sync.RWMutex
	path     string
	botName  string
	maxChars int
	text     string
	now      func() time.Time
}

// NewSoul loads path when it exists, otherwise seeds it with fallback. An empty
// path keeps the soul in memory only, which tests and disabled storage use.
func NewSoul(path, fallback, botName string, maxChars int, now func() time.Time) (*Soul, error) {
	if now == nil {
		now = time.Now
	}
	if botName == "" {
		botName = "Keyholder"
	}
	if maxChars < 1 {
		maxChars = defaultSoulMaxChars
	}

	s := &Soul{
		path:     strings.TrimSpace(path),
		botName:  botName,
		maxChars: maxChars,
		now:      now,
	}

	if s.path == "" {
		s.text = strings.TrimSpace(fallback)
	} else {
		data, err := os.ReadFile(s.path)
		switch {
		case err == nil:
			s.text = strings.TrimSpace(string(data))
			if s.text == "" {
				s.text = strings.TrimSpace(fallback)
				if s.text != "" {
					if err := writeFileAtomic(s.path, []byte(s.text+"\n")); err != nil {
						return nil, fmt.Errorf("reseeding empty soul: %w", err)
					}
				}
			}
		case errors.Is(err, os.ErrNotExist):
			s.text = strings.TrimSpace(fallback)
			if s.text == "" {
				return nil, errors.New("soul: seed is empty")
			}
			if err := writeFileAtomic(s.path, []byte(s.text+"\n")); err != nil {
				return nil, fmt.Errorf("seeding soul: %w", err)
			}
		default:
			return nil, fmt.Errorf("reading soul: %w", err)
		}
	}

	if s.text == "" {
		return nil, errors.New("soul: text is empty")
	}
	if len([]rune(s.text)) > s.maxChars {
		return nil, fmt.Errorf("soul: %d characters exceeds the %d limit", len([]rune(s.text)), s.maxChars)
	}
	return s, nil
}

// Text returns the persona with the bot name resolved. The file keeps the
// {{bot}} placeholder so the same soul can move between names.
func (s *Soul) Text() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.ReplaceAll(s.text, "{{bot}}", s.botName)
}

// Validate checks a proposed soul without writing it.
func (s *Soul) Validate(text string) error {
	if s == nil {
		return errors.New("soul: not configured")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("soul: text is empty")
	}
	if len([]rune(text)) > s.maxChars {
		return fmt.Errorf("soul: %d characters exceeds the %d limit", len([]rune(text)), s.maxChars)
	}
	return nil
}

// Replace validates and writes a new soul, keeping the previous version for
// undo. An in-memory soul with no path still updates for the running process.
func (s *Soul) Replace(text string) error {
	if err := s.Validate(text); err != nil {
		return err
	}
	text = strings.TrimSpace(text)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.path != "" {
		if err := s.backupLocked(); err != nil {
			return err
		}
		if err := writeFileAtomic(s.path, []byte(text+"\n")); err != nil {
			return fmt.Errorf("writing soul: %w", err)
		}
	}
	s.text = text
	return nil
}

// Undo restores the newest backup and removes it, so repeated calls walk back
// through history.
func (s *Soul) Undo() error {
	if s == nil || s.path == "" {
		return errors.New("soul: history is not available")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.historyDir())
	if err != nil {
		return errors.New("soul: no earlier version is available")
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
			names = append(names, entry.Name())
		}
	}
	if len(names) == 0 {
		return errors.New("soul: no earlier version is available")
	}
	sort.Strings(names)
	latest := names[len(names)-1]
	data, err := os.ReadFile(filepath.Join(s.historyDir(), latest))
	if err != nil {
		return fmt.Errorf("reading soul backup: %w", err)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return errors.New("soul: the backup is empty")
	}
	if err := writeFileAtomic(s.path, []byte(text+"\n")); err != nil {
		return fmt.Errorf("restoring soul: %w", err)
	}
	_ = os.Remove(filepath.Join(s.historyDir(), latest))
	s.text = text
	return nil
}

func (s *Soul) backupLocked() error {
	dir := s.historyDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating soul history: %w", err)
	}
	name := s.now().UTC().Format("20060102-150405.000000000") + ".md"
	if err := writeFileAtomic(filepath.Join(dir, name), []byte(s.text+"\n")); err != nil {
		return fmt.Errorf("backing up soul: %w", err)
	}
	pruneBackups(dir, soulHistoryLimit)
	return nil
}

func (s *Soul) historyDir() string {
	return filepath.Join(filepath.Dir(s.path), "SOUL-history")
}
