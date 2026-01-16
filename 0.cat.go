package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
)

// ===========================
// Command Registration
// ===========================

func init() {
	RegisterCommand(discord.SlashCommandCreate{
		Name:        "cat",
		Description: "Cat related commands",
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:        "fact",
				Description: "Get a random cat fact",
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "image",
				Description: "Get a random cat image",
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "say",
				Description: "Cowsay but cat",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:        "message",
						Description: "The message for the cat to say",
						Required:    true,
						MaxLength:   intPtr(2000),
						MinLength:   intPtr(1),
					},
					discord.ApplicationCommandOptionString{
						Name:        "msgcolor",
						Description: "Color of the message text",
						Required:    false,
						Choices:     getCatColorChoices(),
					},
					discord.ApplicationCommandOptionString{
						Name:        "bubcolor",
						Description: "Color of the speech bubble",
						Required:    false,
						Choices:     getCatColorChoices(),
					},
					discord.ApplicationCommandOptionString{
						Name:        "catcolor",
						Description: "Color of the cat",
						Required:    false,
						Choices:     getCatColorChoices(),
					},
					discord.ApplicationCommandOptionString{
						Name:        "expression",
						Description: "The cat's facial expression",
						Required:    false,
						Choices:     getCatExpressionChoices(),
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "stats",
				Description: "View cat system status and details",
			},
		},
	}, func(event *events.ApplicationCommandInteractionCreate) {
		data := event.SlashCommandInteractionData()
		subCmd := data.SubCommandName
		if subCmd == nil {
			return
		}

		switch *subCmd {
		case "stats":
			handleCatStats(event)
		case "fact":
			handleCatFact(event)
		case "image":
			handleCatImage(event)
		case "say":
			handleCatSay(event, data)
		}
	})
}

// ===========================
// Cat API Types
// ===========================

// CatFact represents a cat fact response from the API
type CatFact struct {
	Fact   string `json:"fact"`
	Length int    `json:"length"`
}

// CatImage represents a cat image response from the API
type CatImage struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// ===========================
// Cat ANSI Art Constants
// ===========================

const (
	catAsciiWidth  = 13                                           // Width of the cat ASCII art
	catAnsiReset   = "\u001b[0m"                                  // ANSI reset code
	catFactApiURL  = "https://catfact.ninja/fact"                 // Cat fact API endpoint
	catImageApiURL = "https://api.thecatapi.com/v1/images/search" // Cat image API endpoint
)

// catAnsiColors maps color names to ANSI color codes
var catAnsiColors = map[string]string{
	"gray":   "30",
	"red":    "31",
	"green":  "32",
	"yellow": "33",
	"blue":   "34",
	"pink":   "35",
	"cyan":   "36",
	"white":  "37",
}

// ===========================
// Handler Functions
// ===========================

// handleCatFact fetches and displays a random cat fact from the API
func handleCatFact(event *events.ApplicationCommandInteractionCreate) {
	resp, err := HttpClient.Get(catFactApiURL)
	if err != nil {
		catRespond(event, fmt.Sprintf(MsgCatFactAPIUnreachable, err), true)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		catRespond(event, fmt.Sprintf(MsgCatAPIStatusErrorDisp, resp.StatusCode, resp.Status), true)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		catRespond(event, MsgCatDataError, true)
		return
	}

	var data CatFact
	if err := json.Unmarshal(body, &data); err != nil {
		catRespond(event, fmt.Sprintf(MsgCatFormatErrorExt, err), true)
		return
	}

	catRespond(event, data.Fact+" 🐱", false)
}

// handleCatImage fetches and displays a random cat image from the API
func handleCatImage(event *events.ApplicationCommandInteractionCreate) {
	resp, err := HttpClient.Get(catImageApiURL)
	if err != nil {
		catRespond(event, fmt.Sprintf(MsgCatImageAPIUnreachable, err), true)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		catRespond(event, fmt.Sprintf(MsgCatAPIStatusErrorDisp, resp.StatusCode, resp.Status), true)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		catRespond(event, MsgCatDataError, true)
		return
	}

	var data []CatImage
	if err := json.Unmarshal(body, &data); err != nil || len(data) == 0 {
		errorMsg := MsgCatFormatError
		if len(data) == 0 && err == nil {
			errorMsg = MsgCatImageEmptyResult
		} else if err != nil {
			errorMsg = fmt.Sprintf(MsgCatFormatErrorExt, err)
		}
		catRespond(event, errorMsg, true)
		return
	}

	// Display image using MediaGallery V2 component
	_ = event.CreateMessage(discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewMediaGallery(
					discord.MediaGalleryItem{
						Media: discord.UnfurledMediaItem{
							URL: data[0].URL,
						},
					},
				),
			),
		).
		Build())
}

// handleCatSay generates a cowsay-style cat ASCII art with a custom message
func handleCatSay(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	message := data.String("message")

	msgColor := ""
	if c, ok := data.OptString("msgcolor"); ok {
		msgColor = c
	}

	bubColor := ""
	if c, ok := data.OptString("bubcolor"); ok {
		bubColor = c
	}

	catColor := ""
	if c, ok := data.OptString("catcolor"); ok {
		catColor = c
	}

	expression := "o.o"
	if e, ok := data.OptString("expression"); ok {
		expression = e
	}

	// Generate ASCII
	output := generateCatSay(message, msgColor, bubColor, catColor, expression)

	// Wrap in ansi block
	content := fmt.Sprintf("```ansi\n%s\n```", output)

	catRespond(event, content, false)
}

// handleCatStats displays the cat system status
func handleCatStats(event *events.ApplicationCommandInteractionCreate) {
	catRespond(event, MsgCatSystemStatus, true)
}

// ===========================
// Utility Functions
// ===========================

// catRespond sends a response message using Discord V2 components
func catRespond(event *events.ApplicationCommandInteractionCreate, content string, ephemeral bool) {
	_ = event.CreateMessage(discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay(content),
			),
		).
		SetEphemeral(ephemeral).
		Build())
}

// generateCatSay creates a cowsay-style ASCII art with a cat and speech bubble
// Supports custom colors for message, bubble, and cat, plus custom expressions
func generateCatSay(message, msgColor, bubColor, catColor, expression string) string {
	if expression == "" {
		expression = "o.o"
	}
	calcWidth := len(message)
	if calcWidth < 20 {
		calcWidth = 20
	}
	if calcWidth > 40 {
		calcWidth = 40
	}

	lines := catWrapText(message, calcWidth)
	maxLen := 0
	for _, l := range lines {
		if len(l) > maxLen {
			maxLen = len(l)
		}
	}

	bubColorCode := getCatAnsiCode(bubColor)
	bubReset := ""
	if bubColorCode != "" {
		bubReset = catAnsiReset
	}

	msgColorCode := getCatAnsiCode(msgColor)
	msgReset := ""
	if msgColorCode != "" {
		msgReset = catAnsiReset
	}

	var sb strings.Builder

	// Top Border
	sb.WriteString(bubColorCode)
	sb.WriteString(" ")
	sb.WriteString(strings.Repeat("_", maxLen+2))
	sb.WriteString(bubReset)
	sb.WriteByte('\n')

	// Message Lines
	for i, line := range lines {
		padding := strings.Repeat(" ", maxLen-len(line))

		leftBracket := "|"
		rightBracket := "|"
		if len(lines) == 1 {
			leftBracket, rightBracket = "<", ">"
		} else if i == 0 {
			leftBracket, rightBracket = "/", "\\"
		} else if i == len(lines)-1 {
			leftBracket, rightBracket = "\\", "/"
		}

		sb.WriteString(bubColorCode)
		sb.WriteString(leftBracket)
		sb.WriteString(bubReset)
		sb.WriteString(" ")
		sb.WriteString(msgColorCode)
		sb.WriteString(line)
		sb.WriteString(msgReset)
		sb.WriteString(padding)
		sb.WriteString(" ")
		sb.WriteString(bubColorCode)
		sb.WriteString(rightBracket)
		sb.WriteString(bubReset)
		sb.WriteByte('\n')
	}

	// Bottom Border
	sb.WriteString(bubColorCode)
	sb.WriteString(" ")
	sb.WriteString(strings.Repeat("-", maxLen+2))
	sb.WriteString(bubReset)
	sb.WriteByte('\n')

	// Cat logic
	overallBubbleWidth := maxLen + 4
	paddingNeeded := int(math.Max(0, float64((overallBubbleWidth-catAsciiWidth)/2)))
	catIndent := strings.Repeat(" ", paddingNeeded)

	catColorCode := getCatAnsiCode(catColor)
	catReset := ""
	if catColorCode != "" {
		catReset = catAnsiReset
	}

	// Cat ASCII
	sb.WriteString(fmt.Sprintf("%s    %s\\%s\n", catIndent, bubColorCode, bubReset))
	sb.WriteString(fmt.Sprintf("%s     %s\\%s\n", catIndent, bubColorCode, bubReset))
	sb.WriteString(fmt.Sprintf("%s      %s/\\_/\\%s\n", catIndent, catColorCode, catReset))
	sb.WriteString(fmt.Sprintf("%s     %s( %s )%s\n", catIndent, catColorCode, expression, catReset))
	sb.WriteString(fmt.Sprintf("%s      %s> ^ <%s", catIndent, catColorCode, catReset))

	return sb.String()
}

// catWrapText wraps text to fit within the specified width, breaking on word boundaries
func catWrapText(text string, width int) []string {
	var lines []string
	words := strings.Fields(text)
	if len(words) == 0 {
		return lines
	}

	var sb strings.Builder
	currentLen := 0

	sb.WriteString(words[0])
	currentLen = len(words[0])

	for _, word := range words[1:] {
		wordLen := len(word)
		if currentLen+1+wordLen > width {
			lines = append(lines, sb.String())
			sb.Reset()
			sb.WriteString(word)
			currentLen = wordLen
		} else {
			sb.WriteString(" ")
			sb.WriteString(word)
			currentLen += 1 + wordLen
		}
	}
	lines = append(lines, sb.String())
	return lines
}

// getCatAnsiCode converts a color name to its ANSI escape code
func getCatAnsiCode(color string) string {
	if code, ok := catAnsiColors[color]; ok {
		return "\u001b[0;" + code + "m"
	}
	return ""
}

// getCatColorChoices returns the available color choices for the cat command
func getCatColorChoices() []discord.ApplicationCommandOptionChoiceString {
	return []discord.ApplicationCommandOptionChoiceString{
		{Name: "Gray", Value: "gray"},
		{Name: "Red", Value: "red"},
		{Name: "Green", Value: "green"},
		{Name: "Yellow", Value: "yellow"},
		{Name: "Blue", Value: "blue"},
		{Name: "Pink", Value: "pink"},
		{Name: "Cyan", Value: "cyan"},
		{Name: "White", Value: "white"},
	}
}

// getCatExpressionChoices returns the available expression choices for the cat
func getCatExpressionChoices() []discord.ApplicationCommandOptionChoiceString {
	return []discord.ApplicationCommandOptionChoiceString{
		{Name: "Neutral", Value: "o.o"},
		{Name: "Shocked", Value: "O.O"},
		{Name: "Happy", Value: "^.^"},
		{Name: "Sleeping", Value: "-.-"},
		{Name: "Confused", Value: "o.O"},
		{Name: "Silly", Value: ">.<"},
		{Name: "Wink", Value: "o.~"},
		{Name: "Dizzy", Value: "@.@"},
		{Name: "Crying", Value: "T.T"},
		{Name: "Angry", Value: "ò.ó"},
		{Name: "Star Eyes", Value: "*.*"},
		{Name: "Money", Value: "$.$"},
		{Name: "None", Value: "   "},
	}
}
