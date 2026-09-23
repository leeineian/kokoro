package main

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
)

// ============================================================================
// Undertext System Constants
// ============================================================================

const (
	MsgUndertextRespondError = "Failed to respond to interaction: %v"
)

// ===========================
// Command Registration
// ===========================

func init() {
	RegisterCommand(discord.SlashCommandCreate{
		Name:        "undertext",
		Description: "Generate an Undertale/Deltarune style text box image",
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionString{
				Name:        "message",
				Description: "The text to display in the box",
				Required:    true,
			},
			discord.ApplicationCommandOptionString{
				Name:         "character",
				Description:  "Character to display (e.g. sans, toriel, papyrus)",
				Autocomplete: true,
			},
			discord.ApplicationCommandOptionString{
				Name:        "expression",
				Description: "Character expression (e.g. wink, blush)",
			},
			discord.ApplicationCommandOptionString{
				Name:        "box",
				Description: "Box style",
				Choices: []discord.ApplicationCommandOptionChoiceString{
					{Name: "Undertale", Value: "undertale"},
					{Name: "Underswap", Value: "underswap"},
					{Name: "Underfell", Value: "underfell"},
					{Name: "Octagonal", Value: "octagonal"},
					{Name: "Derp", Value: "derp"},
				},
			},
			discord.ApplicationCommandOptionString{
				Name:        "mode",
				Description: "Text mode",
				Choices: []discord.ApplicationCommandOptionChoiceString{
					{Name: "Normal", Value: "normal"},
					{Name: "Dark World (Deltarune)", Value: "darkworld"},
				},
			},
			discord.ApplicationCommandOptionInt{
				Name:        "size",
				Description: "Box size (1-3)",
				Choices: []discord.ApplicationCommandOptionChoiceInt{
					{Name: "Small", Value: 1},
					{Name: "Medium", Value: 2},
					{Name: "Large", Value: 3},
				},
			},
			discord.ApplicationCommandOptionString{
				Name:        "custom_url",
				Description: "URL for custom character sprite (set character to 'Custom URL')",
			},
			discord.ApplicationCommandOptionString{
				Name:        "boxcolor",
				Description: "Color of the box outline (HEX or name)",
			},
			discord.ApplicationCommandOptionString{
				Name:        "charcolor",
				Description: "Color of the character sprite (HEX)",
			},
			discord.ApplicationCommandOptionString{
				Name:        "font",
				Description: "Font name",
			},
			discord.ApplicationCommandOptionBool{
				Name:        "margin",
				Description: "Whether to have a black margin around the box",
			},
			discord.ApplicationCommandOptionString{
				Name:        "asterisk",
				Description: "Asterisk setting (true/false/color)",
			},
			discord.ApplicationCommandOptionBool{
				Name:        "animated",
				Description: "Generate animated GIF instead of static image",
			},
			discord.ApplicationCommandOptionAttachment{
				Name:        "image",
				Description: "Custom character image (overrides character selection)",
			},
			discord.ApplicationCommandOptionUser{
				Name:        "user",
				Description: "Use a user's avatar as the character",
			},
		},
	}, handleUndertext)

	RegisterAutocompleteHandler("undertext", undertextAutocomplete)
}

// Undertext command shared utilities
const undertextBaseURL = "https://www.demirramon.com/gen/undertale_text_box"

// Character choices for autocomplete
var undertextCharacters = []struct {
	Name  string
	Value string
}{
	{"Sans", "sans"},
	{"Papyrus", "papyrus"},
	{"Toriel", "toriel"},
	{"Flowey", "flowey"},
	{"Undyne", "undyne"},
	{"Alphys", "alphys"},
	{"Asgore", "asgore"},
	{"Mettaton", "mettaton"},
	{"Frisk", "frisk"},
	{"Chara", "chara"},
	{"Kris (Deltarune)", "kris"},
	{"Susie (Deltarune)", "susie"},
	{"Ralsei (Deltarune)", "ralsei"},
	{"Noelle (Deltarune)", "noelle"},
	{"Berdly (Deltarune)", "berdly"},
	{"Spamton (Deltarune)", "spamton"},
}

// undertextAutocomplete provides character name autocomplete suggestions
func undertextAutocomplete(event *events.AutocompleteInteractionCreate) {
	data := event.Data

	// Find the focused option
	var focusedValue string
	var focusedName string
	for _, opt := range data.Options {
		if opt.Focused {
			focusedName = opt.Name
			if opt.Value != nil {
				focusedValue = strings.Trim(string(opt.Value), `"`)
			}
			break
		}
	}

	if focusedName != "character" {
		return
	}

	var choices []discord.AutocompleteChoice

	// Filter characters based on input
	for _, char := range undertextCharacters {
		if focusedValue == "" || ContainsIgnoreCase(char.Name, focusedValue) || ContainsIgnoreCase(char.Value, focusedValue) {
			choices = append(choices, discord.AutocompleteChoiceString{
				Name:  char.Name,
				Value: char.Value,
			})
			if len(choices) >= 25 {
				break
			}
		}
	}

	event.AutocompleteResult(choices)
}

// handleUndertext generates an Undertale/Deltarune style text box image
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
	err := RespondInteractionContainerV2(*event.Client(), event, NewV2Container(NewMediaGallery(generatedURL)), false)
	if err != nil {
		LogUndertext(MsgUndertextRespondError, err)
	}
}

// Syntax: [color]text[/] where color is hex (#ff0000) or name (red)
func processUndertextColors(input string) string {
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
