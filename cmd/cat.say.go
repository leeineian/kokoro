package cmd

import (
	"fmt"
	"math"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/leeineian/minder/sys"
)

// Cat say ASCII art constants
const (
	catAsciiWidth = 13
	catAnsiReset  = "\u001b[0m"
)

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

func getCatAnsiCode(color string) string {
	if code, ok := catAnsiColors[color]; ok {
		return "\u001b[0;" + code + "m"
	}
	return ""
}

func handleCatSay(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: sys.MessageFlagsIsComponentsV2,
		},
	})
	go func() {
		var message, msgColor, bubColor, catColor string
		for _, opt := range options {
			switch opt.Name {
			case "message":
				message = opt.StringValue()
			case "msgcolor":
				msgColor = opt.StringValue()
			case "bubcolor":
				bubColor = opt.StringValue()
			case "catcolor":
				catColor = opt.StringValue()
			}
		}

		// Generate ASCII
		output := generateCatSay(message, msgColor, bubColor, catColor)

		// Wrap in ansi block
		content := fmt.Sprintf("```ansi\n%s\n```", output)

		container := sys.NewV2Container(sys.NewTextDisplay(content))
		if err := sys.EditInteractionV2(s, i.Interaction, container); err != nil {
			sys.LogCat(sys.MsgCatErrorEditingResponse, err)
		}
	}()
}

func getCatColorChoices() []*discordgo.ApplicationCommandOptionChoice {
	return []*discordgo.ApplicationCommandOptionChoice{
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

func generateCatSay(message, msgColor, bubColor, catColor string) string {
	// Calculate dynamic bubble width based on message length
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
	sb.WriteString(fmt.Sprintf("%s     %s( o.o )%s\n", catIndent, catColorCode, catReset))
	sb.WriteString(fmt.Sprintf("%s      %s> ^ <%s", catIndent, catColorCode, catReset))

	return sb.String()
}

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
