package home

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/leeineian/minder/sys"
)

// processUndertextColors converts user-friendly color syntax to API format
// Syntax: [color]text[/] where color is hex (#ff0000) or name (red)
// Example: "Hello [red]world[/]!" → "Hello color=red world text=join color=white !"
func processUndertextColors(input string) string {
	// Pattern: [color]text[/] or [#hex]text[/]
	pattern := regexp.MustCompile(`\[([#]?[a-zA-Z0-9]+)\]([^\[]*)\[/\]`)

	return pattern.ReplaceAllStringFunc(input, func(match string) string {
		submatches := pattern.FindStringSubmatch(match)
		if len(submatches) < 3 {
			return match
		}

		color := submatches[1]
		text := submatches[2]

		// Format: color=X text text=join color=white
		return fmt.Sprintf("color=%s %s text=join color=white ", color, text)
	})
}

func handleUndertext(s *discordgo.Session, i *discordgo.InteractionCreate) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsIsComponentsV2,
		},
	})
	if err != nil {
		sys.LogUndertext(sys.MsgUndertextRespondError, err)
		return
	}

	options := i.ApplicationCommandData().Options
	params := make(map[string]string)
	var messageText string
	var animated bool
	var imageAttachmentID string

	for _, opt := range options {
		switch opt.Name {
		case "message":
			messageText = opt.StringValue()
		case "character":
			params["character"] = opt.StringValue()
		case "expression":
			params["expression"] = opt.StringValue()
		case "box":
			params["box"] = opt.StringValue()
		case "mode":
			params["mode"] = opt.StringValue()
		case "size":
			params["size"] = fmt.Sprintf("%d", opt.IntValue())
		case "custom_url":
			params["url"] = opt.StringValue()
		case "boxcolor":
			params["boxcolor"] = opt.StringValue()
		case "charcolor":
			params["charcolor"] = opt.StringValue()
		case "font":
			params["font"] = opt.StringValue()
		case "margin":
			params["margin"] = fmt.Sprintf("%t", opt.BoolValue())
		case "asterisk":
			params["asterisk"] = opt.StringValue()
		case "animated":
			animated = opt.BoolValue()
		case "image":
			// Get attachment ID from the option value
			imageAttachmentID = opt.Value.(string)
		case "user":
			// Get user ID from the option value
			if userID, ok := opt.Value.(string); ok {
				if resolved := i.ApplicationCommandData().Resolved; resolved != nil {
					if user, ok := resolved.Users[userID]; ok {
						params["character"] = "custom"
						params["expression"] = user.AvatarURL("256")
					}
				}
			}
		}
	}

	// If image attachment is provided, use it as custom character (overrides user)
	if imageAttachmentID != "" {
		if resolved := i.ApplicationCommandData().Resolved; resolved != nil {
			if attachment, ok := resolved.Attachments[imageAttachmentID]; ok {
				params["character"] = "custom"
				params["expression"] = attachment.URL
			}
		}
	}

	// Build URL with proper query parameters
	ext := ".png"
	if animated {
		ext = ".gif"
	}

	// Process color syntax: [color]text[/] → color=X text text=join color=white
	processedText := processUndertextColors(messageText)

	// Start with base URL and text parameter
	encodedText := url.QueryEscape(processedText)
	encodedText = strings.ReplaceAll(encodedText, "+", "%20")

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s%s?text=%s", undertextBaseURL, ext, encodedText))

	// Add other parameters as separate query params
	for k, v := range params {
		if v != "" {
			sb.WriteString(fmt.Sprintf("&%s=%s", k, url.QueryEscape(v)))
		}
	}

	generatedURL := sb.String()

	_, err = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Components: &[]discordgo.MessageComponent{
			&discordgo.Container{
				Components: []discordgo.MessageComponent{
					&discordgo.MediaGallery{
						Items: []discordgo.MediaGalleryItem{
							{
								Media: discordgo.UnfurledMediaItem{URL: generatedURL},
							},
						},
					},
				},
			},
		},
	})
	if err != nil {
		sys.LogUndertext(sys.MsgUndertextEditResponseError, err)
		// Fallback: Notify the user using the constant (V2)
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Components: &[]discordgo.MessageComponent{
				&discordgo.Container{
					Components: []discordgo.MessageComponent{
						&discordgo.TextDisplay{Content: sys.ErrUndertextGenerateFailed},
					},
				},
			},
		})
	}
}
