package roleplay

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/bwmarrin/discordgo"

	"raven/internal/ai"
)

// visionImages collects image attachments from the current message and the
// message it replies to. Only Discord-hosted images of a supported type and
// size are used, so the model never fetches an arbitrary link.
func (f *Feature) visionImages(current *discordgo.MessageCreate, referenced *discordgo.Message) []ai.Image {
	if f == nil || !f.visionEnabled {
		return nil
	}

	maxImages := f.visionMaxImages
	if maxImages < 1 {
		maxImages = 1
	}
	images := 0

	result := make([]ai.Image, 0, maxImages)
	add := func(attachments []*discordgo.MessageAttachment, label string) {
		for _, attachment := range attachments {
			if images >= maxImages {
				return
			}
			imageURL, ok := f.usableImageURL(attachment)
			if !ok {
				if attachment != nil {
					f.log.Debug("skipping image attachment",
						"filename", attachment.Filename,
						"type", attachment.ContentType,
						"size", attachment.Size,
					)
				}
				continue
			}

			result = append(result, ai.Image{
				Label:  fmt.Sprintf("%s: %s", label, attachment.Filename),
				URL:    imageURL,
				Detail: f.visionDetail,
			})
			images++
		}
	}

	if current != nil {
		add(current.Attachments, "Image on the current message")
	}
	if referenced != nil {
		add(referenced.Attachments, "Image on the referenced message")
	}

	return result
}

// usableImageURL reports whether an attachment can be sent to the model and
// returns the Discord URL to use. The same rules gate image reactions, so a
// reaction never lands on a message the model could not read.
func (f *Feature) usableImageURL(attachment *discordgo.MessageAttachment) (string, bool) {
	if attachment == nil {
		return "", false
	}
	if !supportedImageType(attachment.ContentType) {
		return "", false
	}
	if attachment.Size <= 0 || int64(attachment.Size) > f.visionMaxBytes {
		return "", false
	}
	imageURL := discordImageURL(attachment)
	if imageURL == "" {
		return "", false
	}
	return imageURL, true
}

// captionTarget identifies the message that carries the caption image. The
// current message wins when both it and its reply have images.
func (f *Feature) captionTarget(current *discordgo.MessageCreate, referenced *discordgo.Message) *captionTarget {
	if f == nil || !f.visionEnabled || current == nil || current.Message == nil {
		return nil
	}
	if hasUsableImage(f, current.Attachments) {
		return &captionTarget{channelID: current.ChannelID, messageID: current.ID}
	}
	if referenced != nil && hasUsableImage(f, referenced.Attachments) {
		return &captionTarget{channelID: current.ChannelID, messageID: referenced.ID}
	}
	return nil
}

func hasUsableImage(f *Feature, attachments []*discordgo.MessageAttachment) bool {
	for _, attachment := range attachments {
		if _, ok := f.usableImageURL(attachment); ok {
			return true
		}
	}
	return false
}

func supportedImageType(contentType string) bool {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch contentType {
	case "image/png", "image/jpeg", "image/jpg", "image/webp", "image/gif":
		return true
	default:
		return false
	}
}

// discordImageURL returns the first attachment URL hosted by Discord. Expiring
// CDN links and proxied links both work; foreign hosts do not.
func discordImageURL(attachment *discordgo.MessageAttachment) string {
	for _, candidate := range []string{attachment.URL, attachment.ProxyURL} {
		if candidate == "" {
			continue
		}
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.Scheme != "https" {
			continue
		}
		host := strings.ToLower(parsed.Hostname())
		if strings.HasSuffix(host, ".discordapp.com") || strings.HasSuffix(host, ".discordapp.net") {
			return candidate
		}
	}
	return ""
}
