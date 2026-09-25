package fun

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
)

const (
	MsgUndertextRespondError = "Failed to respond to interaction: %v"
)

const undertextBaseURL = "https://www.demirramon.com/gen/undertale_text_box"

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

func UndertextAutocomplete(event *events.AutocompleteInteractionCreate) {
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

	for _, char := range undertextCharacters {
		if focusedValue == "" || strings.Contains(strings.ToLower(char.Name), strings.ToLower(focusedValue)) || strings.Contains(strings.ToLower(char.Value), strings.ToLower(focusedValue)) {
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

func HandleUndertext(event *events.ApplicationCommandInteractionCreate) {
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

	if attachment, ok := data.OptAttachment("image"); ok {
		params["character"] = "custom"
		params["expression"] = attachment.URL
	}

	if user, ok := data.OptUser("user"); ok {
		params["character"] = "custom"
		params["expression"] = user.EffectiveAvatarURL()
	}

	ext := ".png"
	if animated {
		ext = ".gif"
	}

	processedText := processUndertextColors(message)

	encodedText := url.QueryEscape(processedText)
	encodedText = strings.ReplaceAll(encodedText, "+", "%20")

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s%s?text=%s", undertextBaseURL, ext, encodedText))

	for k, v := range params {
		if v != "" {
			sb.WriteString(fmt.Sprintf("&%s=%s", k, url.QueryEscape(v)))
		}
	}

	generatedURL := sb.String()

	err := hooks.RespondInteractionContainerV2(*event.Client(), event, hooks.NewV2Container(hooks.NewMediaGallery(generatedURL)), false)
	if err != nil {
		hooks.LogError(MsgUndertextRespondError, err)
	}
}

func processUndertextColors(input string) string {
	pattern := regexp.MustCompile(`\[([#]?[a-zA-Z0-9]+)\]([^\[]*)\[/\]`)

	return pattern.ReplaceAllStringFunc(input, func(match string) string {
		submatches := pattern.FindStringSubmatch(match)
		if len(submatches) < 3 {
			return match
		}

		color := submatches[1]
		text := submatches[2]

		return fmt.Sprintf("color=%s %s text=join color=white ", color, text)
	})
}
