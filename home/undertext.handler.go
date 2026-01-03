package home

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
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

func handleUndertext(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()

	message := data.String("message")
	params := make(map[string]string)
	var animated bool

	if char, ok := data.OptString("character"); ok {
		params["character"] = char
	}
	if expr, ok := data.OptString("expression"); ok {
		params["expression"] = expr
	}
	if box, ok := data.OptString("box"); ok {
		params["box"] = box
	}
	if mode, ok := data.OptString("mode"); ok {
		params["mode"] = mode
	}
	if size, ok := data.OptInt("size"); ok {
		params["size"] = fmt.Sprintf("%d", size)
	}
	if customURL, ok := data.OptString("custom_url"); ok {
		params["url"] = customURL
	}
	if boxcolor, ok := data.OptString("boxcolor"); ok {
		params["boxcolor"] = boxcolor
	}
	if charcolor, ok := data.OptString("charcolor"); ok {
		params["charcolor"] = charcolor
	}
	if font, ok := data.OptString("font"); ok {
		params["font"] = font
	}
	if margin, ok := data.OptBool("margin"); ok {
		params["margin"] = fmt.Sprintf("%t", margin)
	}
	if asterisk, ok := data.OptString("asterisk"); ok {
		params["asterisk"] = asterisk
	}
	if anim, ok := data.OptBool("animated"); ok {
		animated = anim
	}

	// Handle image attachment - overrides user avatar
	if attachment, ok := data.OptAttachment("image"); ok {
		params["character"] = "custom"
		params["expression"] = attachment.URL
	}

	// Handle user avatar
	if user, ok := data.OptUser("user"); ok {
		params["character"] = "custom"
		params["expression"] = user.EffectiveAvatarURL()
	}

	// Build URL with proper query parameters
	ext := ".png"
	if animated {
		ext = ".gif"
	}

	// Process color syntax: [color]text[/] → color=X text text=join color=white
	processedText := processUndertextColors(message)

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

	// Build response with V2 components and MediaGallery
	builder := discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewMediaGallery(
					discord.MediaGalleryItem{Media: discord.UnfurledMediaItem{URL: generatedURL}},
				),
			),
		)

	err := event.CreateMessage(builder.Build())
	if err != nil {
		sys.LogUndertext(sys.MsgUndertextRespondError, err)
	}
}
