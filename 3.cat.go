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

// ============================================================================
// Cat System Constants
// ============================================================================

const (
	MsgCatFailedToFetchFact         = "Failed to fetch cat fact: %v"
	MsgCatFactAPIStatusError        = "Cat fact API returned status %d"
	MsgCatFailedToDecodeFact        = "Failed to decode cat fact: %v"
	MsgCatFailedToFetchImage        = "Failed to fetch cat image: %v"
	MsgCatImageAPIStatusError       = "Cat image API returned status %d"
	MsgCatFailedToDecodeImage       = "Failed to decode cat image response: %v"
	MsgCatImageAPIEmptyArray        = "Cat image API returned empty array"
	MsgCatCannotSendErrorResponse   = "Cannot send error response: nil session or interaction"
	MsgCatFailedToSendErrorResponse = "Failed to send error response: %v"
	MsgCatFactAPIUnreachable        = "**API Unreachable**: The cat fact service is currently offline or timing out.\n> _%v_"
	MsgCatImageAPIUnreachable       = "**API Unreachable**: The cat image service is currently offline or timing out.\n> _%v_"
	MsgCatSystemStatus              = "**Cat Fact API:** `https://catfact.ninja/fact`\n**Cat Image API:** `https://api.thecatapi.com/v1`"
	MsgCatAPIStatusErrorDisp        = "**Service Error**: The API returned an unexpected status code: **%d %s**"
	MsgCatDataError                 = "**Data Error**: Failed to read the response body from the API."
	MsgCatFormatError               = "**Format Error**: The API returned data in an invalid format."
	MsgCatFormatErrorExt            = "**Format Error**: The API returned data in an invalid format.\n> _%v_"
	MsgCatImageEmptyResult          = "**Empty Result**: The API returned an empty list of images."
	ErrCatFailedToFetchFact         = "Failed to fetch cat fact"
	ErrCatFactServiceUnavailable    = "Cat fact service is unavailable"
	ErrCatFailedToDecodeFact        = "Failed to decode cat fact"
	ErrCatFailedToFetchImage        = "Failed to fetch cat image"
	ErrCatImageServiceUnavailable   = "Cat image service is unavailable"
	ErrCatFailedToDecodeImage       = "Failed to decode cat image"
	ErrCatNoImagesAvailable         = "No cat images available"

	// ASCII Art Constants
	CatArtExpressionDefault = "o.o"
	CatArtColorGray         = "gray"
	CatArtColorRed          = "red"
	CatArtColorGreen        = "green"
	CatArtColorYellow       = "yellow"
	CatArtColorBlue         = "blue"
	CatArtColorPink         = "pink"
	CatArtColorCyan         = "cyan"
	CatArtColorWhite        = "white"

	CatArtExpressionShocked  = "O.O"
	CatArtExpressionHappy    = "^.^"
	CatArtExpressionSleeping = "-.-"
	CatArtExpressionConfused = "o.O"
	CatArtExpressionSilly    = ">.<"
	CatArtExpressionWink     = "o.~"
	CatArtExpressionDizzy    = "@.@"
	CatArtExpressionCrying   = "T.T"
	CatArtExpressionAngry    = "ò.ó"
	CatArtExpressionStarEyes = "*.*"
	CatArtExpressionMoney    = "$.$"
	CatArtExpressionNone     = "   "
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
	CatArtColorGray:   "30",
	CatArtColorRed:    "31",
	CatArtColorGreen:  "32",
	CatArtColorYellow: "33",
	CatArtColorBlue:   "34",
	CatArtColorPink:   "35",
	CatArtColorCyan:   "36",
	CatArtColorWhite:  "37",
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
	_ = RespondInteractionContainerV2(*event.Client(), event, NewV2Container(NewMediaGallery(data[0].URL)), false)
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

	expression := CatArtExpressionDefault
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
	_ = RespondInteractionV2(*event.Client(), event, content, ephemeral)
}

// generateCatSay creates a cowsay-style ASCII art with a cat and speech bubble
// Supports custom colors for message, bubble, and cat, plus custom expressions
func generateCatSay(message, msgColor, bubColor, catColor, expression string) string {
	if expression == "" {
		expression = CatArtExpressionDefault
	}
	calcWidth := len(message)
	if calcWidth < 20 {
		calcWidth = 20
	}
	if calcWidth > 40 {
		calcWidth = 40
	}

	lines := WrapText(message, calcWidth)
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
		{Name: "Gray", Value: CatArtColorGray},
		{Name: "Red", Value: CatArtColorRed},
		{Name: "Green", Value: CatArtColorGreen},
		{Name: "Yellow", Value: CatArtColorYellow},
		{Name: "Blue", Value: CatArtColorBlue},
		{Name: "Pink", Value: CatArtColorPink},
		{Name: "Cyan", Value: CatArtColorCyan},
		{Name: "White", Value: CatArtColorWhite},
	}
}

func getCatExpressionChoices() []discord.ApplicationCommandOptionChoiceString {
	return []discord.ApplicationCommandOptionChoiceString{
		{Name: "Neutral", Value: CatArtExpressionDefault},
		{Name: "Shocked", Value: CatArtExpressionShocked},
		{Name: "Happy", Value: CatArtExpressionHappy},
		{Name: "Sleeping", Value: CatArtExpressionSleeping},
		{Name: "Confused", Value: CatArtExpressionConfused},
		{Name: "Silly", Value: CatArtExpressionSilly},
		{Name: "Wink", Value: CatArtExpressionWink},
		{Name: "Dizzy", Value: CatArtExpressionDizzy},
		{Name: "Crying", Value: CatArtExpressionCrying},
		{Name: "Angry", Value: CatArtExpressionAngry},
		{Name: "Star Eyes", Value: CatArtExpressionStarEyes},
		{Name: "Money", Value: CatArtExpressionMoney},
		{Name: "None", Value: CatArtExpressionNone},
	}
}
