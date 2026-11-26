package roleplay

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"raven/internal/ai"
)

func testMemoryStore(t *testing.T) *MemoryStore {
	t.Helper()
	store, err := NewMemoryStore(t.TempDir(), 3000, 2500, func() time.Time {
		return time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	return store
}

func TestMemoryAppendAndContext(t *testing.T) {
	store := testMemoryStore(t)
	if err := store.append("123", "Alice", []string{"Sent a cuckold caption.", "Asked for a task."}, []string{"Prefers being called pet."}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := store.append("123", "Alice", []string{"Sent a cuckold caption."}, nil); err != nil {
		t.Fatalf("append duplicate: %v", err)
	}

	raw, err := store.raw("123")
	if err != nil {
		t.Fatalf("raw: %v", err)
	}
	if strings.Count(raw, "Sent a cuckold caption.") != 1 {
		t.Fatalf("duplicate note in memory: %s", raw)
	}
	if !strings.Contains(raw, "2026-11-23: Sent a cuckold caption.") {
		t.Fatalf("short note is not dated: %s", raw)
	}
	if !strings.Contains(raw, "Prefers being called pet.") {
		t.Fatalf("long note missing: %s", raw)
	}

	contextText := store.context("123", "Alice")
	for _, want := range []string{"About Alice:", "Long-term:", "Prefers being called pet.", "Short-term:", "Sent a cuckold caption."} {
		if !strings.Contains(contextText, want) {
			t.Fatalf("context = %q, want %q", contextText, want)
		}
	}
}

func TestMemoryContextIncludesServer(t *testing.T) {
	store := testMemoryStore(t)
	if err := store.append(memoryServerID, "server", []string{"Caption reviews are popular."}, nil); err != nil {
		t.Fatalf("append server: %v", err)
	}
	contextText := store.context("123", "Alice")
	if !strings.Contains(contextText, "About the server:") || !strings.Contains(contextText, "Caption reviews") {
		t.Fatalf("context = %q", contextText)
	}
}

func TestMemoryContextIsEmptyWithoutNotes(t *testing.T) {
	store := testMemoryStore(t)
	if got := store.context("123", "Alice"); got != "" {
		t.Fatalf("context = %q", got)
	}
}

func TestMemoryForgetAndList(t *testing.T) {
	store := testMemoryStore(t)
	if err := store.append("123", "Alice", nil, []string{"Likes captions."}); err != nil {
		t.Fatalf("append: %v", err)
	}
	summaries, err := store.list()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(summaries) != 1 || summaries[0].ID != "123" || summaries[0].Name != "Alice" {
		t.Fatalf("summaries = %+v", summaries)
	}
	if err := store.forget("123"); err != nil {
		t.Fatalf("forget: %v", err)
	}
	if raw, _ := store.raw("123"); raw != "" {
		t.Fatalf("raw after forget = %q", raw)
	}
}

func TestMemoryRejectsInvalidSubject(t *testing.T) {
	store := testMemoryStore(t)
	if _, err := store.raw("../etc/passwd"); err == nil {
		t.Fatal("expected an invalid subject error")
	}
}

func TestMemoryTrimsOverflow(t *testing.T) {
	store, err := NewMemoryStore(t.TempDir(), 300, 250, time.Now)
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	trimmed := false
	for i := 0; i < 10; i++ {
		var err error
		trimmed, err = store.appendWithTrim("123", "Alice", []string{"short note number " + string(rune('a'+i))}, nil)
		if err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if !trimmed {
		t.Fatal("expected the final write to report trimming")
	}
	raw, err := store.raw("123")
	if err != nil {
		t.Fatalf("raw: %v", err)
	}
	if len([]rune(raw)) > 300 {
		t.Fatalf("memory is %d chars, want at most 300", len([]rune(raw)))
	}
}

func TestParseMemoryUpdateLimitsAndCleans(t *testing.T) {
	content := "```json\n{\"short_term\":[\"  one  \",\"two\",\"three\"],\"long_term\":[\" fact \"],\"server\":[\" lore \"]}\n```"
	update, err := parseMemoryUpdate(content)
	if err != nil {
		t.Fatalf("parseMemoryUpdate: %v", err)
	}
	if len(update.Short) != 2 {
		t.Fatalf("short = %+v", update.Short)
	}
	if update.Short[0] != "one" {
		t.Fatalf("short[0] = %q", update.Short[0])
	}
	if len(update.Long) != 1 || update.Long[0] != "fact" {
		t.Fatalf("long = %+v", update.Long)
	}
	if len(update.Server) != 1 || update.Server[0] != "lore" {
		t.Fatalf("server = %+v", update.Server)
	}
}

func TestParseMemoryUpdateRejectsNonJSON(t *testing.T) {
	if _, err := parseMemoryUpdate("nothing here"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestCompactMemory(t *testing.T) {
	chat := &fakeChat{replies: []ai.Reply{{Text: "```markdown\n# Alice (123)\n\n## Long-term\n- Likes captions.\n\n## Short-term\n- 2026-11-23: Asked for a task.\n```"}}}
	raw := "# Alice (123)\n\n## Long-term\n- Likes captions.\n\n## Short-term\n- 2026-11-23: Asked for a task.\n- 2026-11-20: Old note.\n"

	got, err := compactMemory(context.Background(), chat, raw, "123", "Alice", 3000)
	if err != nil {
		t.Fatalf("compactMemory: %v", err)
	}
	if strings.Contains(got, "Old note") {
		t.Fatalf("compacted memory kept the old note: %s", got)
	}
	if !strings.Contains(got, "## Long-term") || !strings.Contains(got, "## Short-term") {
		t.Fatalf("compacted memory = %s", got)
	}
}

func TestMemoryBackupFilePermissions(t *testing.T) {
	store := testMemoryStore(t)
	if err := store.append("123", "Alice", nil, []string{"Likes captions."}); err != nil {
		t.Fatalf("append: %v", err)
	}
	path := filepath.Join(store.dir, "users", "123.md")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
}
