package roleplay

import (
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"raven/internal/ai"
)

func visionFeature(t *testing.T, maxImages, maxMB int) *Feature {
	t.Helper()
	f := testFeature(t, nil)
	f.visionEnabled = true
	f.visionMaxImages = maxImages
	f.visionMaxBytes = int64(maxMB) * 1024 * 1024
	f.visionDetail = "auto"
	return f
}

func attachment(filename, contentType string, size int, url string) *discordgo.MessageAttachment {
	return &discordgo.MessageAttachment{
		Filename:    filename,
		ContentType: contentType,
		Size:        size,
		URL:         url,
	}
}

func TestVisionImagesIncludesCurrentAndReferencedImages(t *testing.T) {
	f := visionFeature(t, 4, 8)
	current := &discordgo.MessageCreate{Message: &discordgo.Message{
		Attachments: []*discordgo.MessageAttachment{
			attachment("current.png", "image/png", 1000, "https://cdn.discordapp.com/attachments/1/2/current.png"),
		},
	}}
	referenced := &discordgo.Message{
		Attachments: []*discordgo.MessageAttachment{
			attachment("referenced.jpg", "image/jpeg", 2000, "https://media.discordapp.net/attachments/3/4/referenced.jpg"),
		},
	}

	images := f.visionImages(current, referenced)

	if len(images) != 2 {
		t.Fatalf("images = %+v", images)
	}
	if images[0].URL != "https://cdn.discordapp.com/attachments/1/2/current.png" {
		t.Fatalf("first URL = %q", images[0].URL)
	}
	if len(images[0].Label) == 0 || images[0].Detail != "auto" {
		t.Fatalf("first image = %+v", images[0])
	}
	if images[1].URL != "https://media.discordapp.net/attachments/3/4/referenced.jpg" {
		t.Fatalf("second URL = %q", images[1].URL)
	}
}

func TestVisionImagesFiltersUnsupportedOversizedAndForeign(t *testing.T) {
	f := visionFeature(t, 4, 1)
	current := &discordgo.MessageCreate{Message: &discordgo.Message{
		Attachments: []*discordgo.MessageAttachment{
			attachment("notes.txt", "text/plain", 100, "https://cdn.discordapp.com/attachments/1/2/notes.txt"),
			attachment("huge.png", "image/png", 2*1024*1024, "https://cdn.discordapp.com/attachments/1/2/huge.png"),
			attachment("external.png", "image/png", 100, "https://example.com/external.png"),
			attachment("insecure.png", "image/png", 100, "http://cdn.discordapp.com/attachments/1/2/insecure.png"),
			attachment("ok.webp", "image/webp", 100, "https://cdn.discordapp.com/attachments/1/2/ok.webp"),
		},
	}}

	images := f.visionImages(current, nil)

	if len(images) != 1 {
		t.Fatalf("images = %+v", images)
	}
	if images[0].URL != "https://cdn.discordapp.com/attachments/1/2/ok.webp" {
		t.Fatalf("URL = %q", images[0].URL)
	}
}

func TestVisionImagesRespectsMaxImages(t *testing.T) {
	f := visionFeature(t, 1, 8)
	current := &discordgo.MessageCreate{Message: &discordgo.Message{
		Attachments: []*discordgo.MessageAttachment{
			attachment("one.png", "image/png", 100, "https://cdn.discordapp.com/attachments/1/2/one.png"),
			attachment("two.png", "image/png", 100, "https://cdn.discordapp.com/attachments/1/2/two.png"),
		},
	}}

	images := f.visionImages(current, nil)

	if len(images) != 1 || images[0].URL != "https://cdn.discordapp.com/attachments/1/2/one.png" {
		t.Fatalf("images = %+v", images)
	}
}

func TestVisionImagesDisabled(t *testing.T) {
	f := testFeature(t, nil)
	current := &discordgo.MessageCreate{Message: &discordgo.Message{
		Attachments: []*discordgo.MessageAttachment{
			attachment("image.png", "image/png", 100, "https://cdn.discordapp.com/attachments/1/2/image.png"),
		},
	}}

	if images := f.visionImages(current, nil); images != nil {
		t.Fatalf("images = %+v", images)
	}
}

func TestVisionImagesUsesProxyURLFallback(t *testing.T) {
	f := visionFeature(t, 4, 8)
	current := &discordgo.MessageCreate{Message: &discordgo.Message{
		Attachments: []*discordgo.MessageAttachment{
			{
				Filename:    "proxy.png",
				ContentType: "image/png",
				Size:        100,
				ProxyURL:    "https://media.discordapp.net/attachments/1/2/proxy.png",
			},
		},
	}}

	images := f.visionImages(current, nil)

	if len(images) != 1 || images[0].URL != "https://media.discordapp.net/attachments/1/2/proxy.png" {
		t.Fatalf("images = %+v", images)
	}
}

func TestBuildMessagesAttachesImagesToCurrentTurn(t *testing.T) {
	images := []ai.Image{{Label: "Image on the current message: cat.png", URL: "https://cdn.discordapp.com/cat.png", Detail: "auto"}}

	messages := buildMessages(
		"bot-1",
		[]*discordgo.Message{{ID: "m1", Author: &discordgo.User{ID: "user-1", Username: "alice"}, Content: "hello"}},
		"",
		&discordgo.User{ID: "user-1", Username: "alice"},
		time.Time{},
		images,
	)

	if len(messages) != 2 {
		t.Fatalf("messages = %+v", messages)
	}
	if len(messages[0].Images) != 0 {
		t.Fatalf("history images = %+v", messages[0].Images)
	}
	if len(messages[1].Images) != 1 || messages[1].Images[0].URL != "https://cdn.discordapp.com/cat.png" {
		t.Fatalf("last images = %+v", messages[1].Images)
	}
}
