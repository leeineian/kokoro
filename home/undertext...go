package home

import (
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/leeineian/minder/sys"
)

func init() {
	sys.RegisterCommand(&discordgo.ApplicationCommand{
		Name:        "undertext",
		Description: "Generate an Undertale/Deltarune style text box image",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "message",
				Description: "The text to display in the box",
				Required:    true,
			},
			{
				Type:         discordgo.ApplicationCommandOptionString,
				Name:         "character",
				Description:  "Character to display (e.g. sans, toriel, papyrus)",
				Autocomplete: true,
			},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "expression",
				Description: "Character expression (e.g. wink, blush)",
			},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "box",
				Description: "Box style",
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "Undertale", Value: "undertale"},
					{Name: "Underswap", Value: "underswap"},
					{Name: "Underfell", Value: "underfell"},
					{Name: "Octagonal", Value: "octagonal"},
					{Name: "Derp", Value: "derp"},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "mode",
				Description: "Text mode",
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "Normal", Value: "normal"},
					{Name: "Dark World (Deltarune)", Value: "darkworld"},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionInteger,
				Name:        "size",
				Description: "Box size (1-3)",
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "Small", Value: 1},
					{Name: "Medium", Value: 2},
					{Name: "Large", Value: 3},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "custom_url",
				Description: "URL for custom character sprite (set character to 'Custom URL')",
			},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "boxcolor",
				Description: "Color of the box outline (HEX or name)",
			},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "charcolor",
				Description: "Color of the character sprite (HEX)",
			},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "font",
				Description: "Font name",
			},
			{
				Type:        discordgo.ApplicationCommandOptionBoolean,
				Name:        "margin",
				Description: "Whether to have a black margin around the box",
			},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "asterisk",
				Description: "Asterisk setting (true/false/color)",
			},
			{
				Type:        discordgo.ApplicationCommandOptionBoolean,
				Name:        "animated",
				Description: "Generate animated GIF instead of static image",
			},
			{
				Type:        discordgo.ApplicationCommandOptionAttachment,
				Name:        "image",
				Description: "Custom character image (overrides character selection)",
			},
			{
				Type:        discordgo.ApplicationCommandOptionUser,
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

func undertextAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()

	// Find the focused option
	var focusedOption *discordgo.ApplicationCommandInteractionDataOption
	for _, opt := range data.Options {
		if opt.Focused {
			focusedOption = opt
			break
		}
	}

	if focusedOption == nil || focusedOption.Name != "character" {
		return
	}

	input := focusedOption.StringValue()
	var choices []*discordgo.ApplicationCommandOptionChoice

	// Filter characters based on input
	for _, char := range undertextCharacters {
		if input == "" || containsIgnoreCase(char.Name, input) || containsIgnoreCase(char.Value, input) {
			choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
				Name:  char.Name,
				Value: char.Value,
			})
			if len(choices) >= 25 { // Discord limit
				break
			}
		}
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{
			Choices: choices,
		},
	})
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
