package home

import (
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/sys"
)

func init() {
	sys.RegisterCommand(discord.SlashCommandCreate{
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

	sys.RegisterAutocompleteHandler("undertext", undertextAutocomplete)
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
		if focusedValue == "" || containsIgnoreCase(char.Name, focusedValue) || containsIgnoreCase(char.Value, focusedValue) {
			choices = append(choices, discord.AutocompleteChoiceString{
				Name:  char.Name,
				Value: char.Value,
			})
			if len(choices) >= 25 { // Discord limit
				break
			}
		}
	}

	event.AutocompleteResult(choices)
}

func containsIgnoreCase(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		len(substr) == 0 ||
		(len(s) > 0 && containsLower(s, substr)))
}

func containsLower(s, substr string) bool {
	s = strings.ToLower(s)
	substr = strings.ToLower(substr)
	return strings.Contains(s, substr)
}
