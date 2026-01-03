package home

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
)

const eightBallApiURL = "https://www.eightballapi.com/api"

type EightBallResponse struct {
	Reading string `json:"reading"`
	Locale  string `json:"locale"`
}

func handle8BallFortune(event *events.ApplicationCommandInteractionCreate) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(eightBallApiURL)
	if err != nil {
		event.CreateMessage(discord.NewMessageCreateBuilder().
			SetIsComponentsV2(true).
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay("❌ Failed to fetch fortune."),
				),
			).
			SetEphemeral(true).
			Build())
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		event.CreateMessage(discord.NewMessageCreateBuilder().
			SetIsComponentsV2(true).
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay("❌ Failed to read response."),
				),
			).
			SetEphemeral(true).
			Build())
		return
	}

	var data EightBallResponse
	if err := json.Unmarshal(body, &data); err != nil {
		event.CreateMessage(discord.NewMessageCreateBuilder().
			SetIsComponentsV2(true).
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay("❌ Failed to parse fortune."),
				),
			).
			SetEphemeral(true).
			Build())
		return
	}

	reading := data.Reading

	// Determine width: at least 21, and at least readingLen + 2 for margins
	readingLen := utf8.RuneCountInString(reading)
	width := 21
	if readingLen+2 > width {
		width = readingLen + 2
	}

	// ANSI color codes for Discord ansi blocks
	const (
		ansiRed   = "\x1b[31m"
		ansiGreen = "\x1b[32m"
		ansiBlue  = "\x1b[34m"
		ansiReset = "\x1b[0m"
	)

	// Send initial "Filled" State (No color)
	_ = event.CreateMessage(discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay(generateDynamic8Ball("", width, true, "")),
			),
		).
		Build())

	// Animate through colors and then reveal
	go func() {
		steps := []struct {
			color  string
			filled bool
			text   string
		}{
			{ansiRed, true, ""},
			{ansiGreen, true, ""},
			{ansiBlue, true, ""},
			{"", false, reading}, // Reveal
		}

		for _, step := range steps {
			time.Sleep(300 * time.Millisecond)
			_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.NewMessageUpdateBuilder().
				SetIsComponentsV2(true).
				AddComponents(
					discord.NewContainer(
						discord.NewTextDisplay(generateDynamic8Ball(step.text, width, step.filled, step.color)),
					),
				).
				Build())
		}
	}()
}

func generateDynamic8Ball(reading string, width int, filled bool, colorCode string) string {
	extra := width - 21
	sAFill := strings.Repeat("a", 5+extra)

	// Centering text for line 8
	padding := (width - utf8.RuneCountInString(reading)) / 2
	centered := strings.Repeat(" ", padding) + reading + strings.Repeat(" ", width-utf8.RuneCountInString(reading)-padding)

	if filled {
		centered = strings.Repeat("8", width)
	}

	// Dynamic lines based on symmetrical model
	l1 := "          .aad" + strings.Repeat("8", 7+extra) + "baa. "
	l2 := "      .ad" + strings.Repeat("8", 17+extra) + "ba. "
	l3 := "    .d" + strings.Repeat("8", 23+extra) + "b. "

	var l4, l5, l6, l7, l8, l9, l10, l11, l12, l13, l14, l15 string
	if filled {
		l4 = "   d" + strings.Repeat("8", 27+extra) + "b "
		l5 = "  d" + strings.Repeat("8", 29+extra) + "b "
		l6 = " d" + strings.Repeat("8", 31+extra) + "b "
		l7 = " 8" + strings.Repeat("8", 31+extra) + "8 "
		l8 = " 888888" + centered + "888888 "
		l9 = l7
		l10 = l6
		l11 = "  Y" + strings.Repeat("8", 29+extra) + "P "
		l12 = "   Y" + strings.Repeat("8", 27+extra) + "P "
		l13 = "    \"Y" + strings.Repeat("8", 23+extra) + "P\" "
		l14 = "      \"Y" + strings.Repeat("8", 19+extra) + "P\" "
		l15 = "          \"\"" + strings.Repeat("8", 11+extra) + "\"\" "
	} else {
		l4 = "   d8888888P\"\"" + strings.Repeat(" ", 7+extra) + "\"\"Y8888888b "
		l5 = "  d888888P" + strings.Repeat(" ", 15+extra) + "Y888888b "
		l6 = " d888888" + strings.Repeat(" ", 19+extra) + "888888b "
		l7 = " 888888" + strings.Repeat(" ", 21+extra) + "888888 "
		l8 = " 888888" + centered + "888888 "
		l9 = l7
		l10 = l6
		l11 = "  Y88888b." + strings.Repeat(" ", 15+extra) + ".d88888P "
		l12 = "   Y888888b." + strings.Repeat(" ", 11+extra) + ".d888888P "
		l13 = "    \"Y8888888bb" + sAFill + "dd8888888P\" "
		l14 = "      \"Y" + strings.Repeat("8", 19+extra) + "P\" "
		l15 = "          \"\"" + strings.Repeat("8", 11+extra) + "\"\" "
	}

	content := l1 + "\n" +
		l2 + "\n" +
		l3 + "\n" +
		l4 + "\n" +
		l5 + "\n" +
		l6 + "\n" +
		l7 + "\n" +
		l8 + "\n" +
		l9 + "\n" +
		l10 + "\n" +
		l11 + "\n" +
		l12 + "\n" +
		l13 + "\n" +
		l14 + "\n" +
		l15

	if colorCode != "" {
		content = colorCode + content + "\x1b[0m"
	}

	return "```ansi\n" + content + "\n```"
}
