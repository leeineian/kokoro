package home

import (
	"fmt"
	"math"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/sys"
)

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

	// Build with V2 components
	builder := discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay(content),
			),
		)

	err := event.CreateMessage(builder.Build())
	if err != nil {
		sys.LogCat(sys.MsgCatFailedToSendErrorResponse, err)
	}
}

func generateCatSay(message, msgColor, bubColor, catColor, expression string) string {
	if expression == "" {
		expression = "o.o"
	}
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
	sb.WriteString(fmt.Sprintf("%s     %s( %s )%s\n", catIndent, catColorCode, expression, catReset))
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
