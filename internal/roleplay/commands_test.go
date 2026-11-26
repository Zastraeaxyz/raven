package roleplay

import (
	"strings"
	"testing"
)

func TestAboutTextUsesAuthoredCopy(t *testing.T) {
	f := testFeature(t, nil)
	f.about = "  I am the one who decides.  "

	if got := f.aboutText(); got != "I am the one who decides." {
		t.Fatalf("aboutText = %q", got)
	}
}

func TestAboutTextFallsBackWithHerName(t *testing.T) {
	f := testFeature(t, nil)
	f.about = ""

	got := f.aboutText()
	if !strings.Contains(got, "Mistress") {
		t.Fatalf("aboutText = %q", got)
	}
}

func TestAboutCommandName(t *testing.T) {
	if name := aboutCommand().Name; name != "about" {
		t.Fatalf("command name = %q", name)
	}
}
