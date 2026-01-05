package home

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/sys"
)

const eightBallApiURL = "https://www.eightballapi.com/api"

type EightBallResponse struct {
	Reading string `json:"reading"`
	Locale  string `json:"locale"`
}

func init() {
	sys.RegisterCommand(discord.SlashCommandCreate{
		Name:        "8ball",
		Description: "Eightball related commands",
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:        "fortune",
				Description: "Get a random fortune",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:        "question",
						Description: "The question you want to ask the eightball",
						Required:    false,
					},
				},
			},
		},
	}, func(event *events.ApplicationCommandInteractionCreate) {
		data := event.SlashCommandInteractionData()
		subCmd := data.SubCommandName
		if subCmd == nil {
			return
		}

		switch *subCmd {
		case "fortune":
			handle8BallFortune(event)
		}
	})
}

func handle8BallFortune(event *events.ApplicationCommandInteractionCreate) {
	resp, err := sys.HttpClient.Get(eightBallApiURL)
	if err != nil {
		event.CreateMessage(discord.NewMessageCreateBuilder().
			SetIsComponentsV2(true).
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay("❌ **API Unreachable**: The 8-ball service is currently offline or timing out.\n> _" + err.Error() + "_"),
				),
			).
			SetEphemeral(true).
			Build())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		event.CreateMessage(discord.NewMessageCreateBuilder().
			SetIsComponentsV2(true).
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay(fmt.Sprintf("❌ **Service Error**: The API returned an unexpected status code: **%d %s**", resp.StatusCode, resp.Status)),
				),
			).
			SetEphemeral(true).
			Build())
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		event.CreateMessage(discord.NewMessageCreateBuilder().
			SetIsComponentsV2(true).
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay("❌ **Data Error**: Failed to read the response body from the API."),
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
					discord.NewTextDisplay("❌ **Format Error**: The API returned data in an invalid format.\n> _" + err.Error() + "_"),
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

	// Send initial "Filled" State (No color, Ratio 0.0)
	_ = event.CreateMessage(discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay(generateDynamic8Ball(reading, width, 0.0, "")),
			),
		).
		Build())

	// Animate through colors and then reveal
	go func() {
		// Initial wait so the user can actually see the engraved "8"
		time.Sleep(1200 * time.Millisecond)

		steps := []struct {
			color string
			ratio float64
			text  string
		}{
			{ansiRed, 0.25, reading},
			{ansiGreen, 0.50, reading},
			{ansiBlue, 0.75, reading},
			{"", 1.0, reading}, // Reveal final
		}

		for _, step := range steps {
			_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.NewMessageUpdateBuilder().
				SetIsComponentsV2(true).
				AddComponents(
					discord.NewContainer(
						discord.NewTextDisplay(generateDynamic8Ball(step.text, width, step.ratio, step.color)),
					),
				).
				Build())
			time.Sleep(350 * time.Millisecond)
		}
	}()
}

func generateDynamic8Ball(reading string, width int, revealRatio float64, colorCode string) string {
	// Helper to generate full lines for a specific state
	getLines := func(isFortune bool) []string {
		extra := width - 21
		sAFill := strings.Repeat("a", 5+extra)

		padding := (width - utf8.RuneCountInString(reading)) / 2
		centered := strings.Repeat(" ", padding) + reading + strings.Repeat(" ", width-utf8.RuneCountInString(reading)-padding)

		// Dynamic lines
		l1 := "          .aad" + strings.Repeat("8", 7+extra) + "baa. "
		l2 := "      .ad" + strings.Repeat("8", 17+extra) + "ba. "
		l3 := "    .d" + strings.Repeat("8", 23+extra) + "b. "

		var l4, l5, l6, l7, l8, l9, l10, l11, l12, l13, l14, l15 string

		if !isFortune {
			// Engraved 8 variant
			pad8L := strings.Repeat("8", extra/2)
			pad8R := strings.Repeat("8", extra-extra/2)

			l4 = "   d" + pad8L + "888888888         888888888" + pad8R + "b "
			l5 = "  d" + pad8L + "88888888   8888888   88888888" + pad8R + "b "
			l6 = " d" + pad8L + "88888888   888888888   88888888" + pad8R + "b "
			l7 = " 8" + pad8L + "888888888    88888    888888888" + pad8R + "8 "
			l8 = " 8" + pad8L + "8888888888           8888888888" + pad8R + "8 "
			l9 = " 8" + pad8L + "888888888    88888    888888888" + pad8R + "8 "
			l10 = " d" + pad8L + "88888888   888888888   88888888" + pad8R + "b "
			l11 = "  Y" + pad8L + "88888888   8888888   88888888" + pad8R + "P "
			l12 = "   Y" + pad8L + "888888888         888888888" + pad8R + "P "
			l13 = "    \"Y" + strings.Repeat("8", 23+extra) + "P\" "
			l14 = "      \"Y" + strings.Repeat("8", 19+extra) + "P\" "
			l15 = "          \"\"" + strings.Repeat("8", 11+extra) + "\"\" "
		} else {
			// Hollow variant
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

		return []string{l1, l2, l3, l4, l5, l6, l7, l8, l9, l10, l11, l12, l13, l14, l15}
	}

	filledLines := getLines(false) // Engraved 8
	hollowLines := getLines(true)  // Hollow with fortune

	// Combine lines based on revealRatio
	var sb strings.Builder
	// Use a seeded random to make the reveal "progressive" (dots that appear stay appeared)
	rng := rand.New(rand.NewSource(42))

	for i := 0; i < len(filledLines); i++ {
		fl := filledLines[i]
		hl := hollowLines[i]

		// Blend characters
		fRunes := []rune(fl)
		hRunes := []rune(hl)

		for j := 0; j < len(fRunes); j++ {
			if fRunes[j] != hRunes[j] {
				// If characters differ (e.g. '8' vs ' '), decide based on ratio
				if rng.Float64() < revealRatio {
					sb.WriteRune(hRunes[j])
				} else {
					sb.WriteRune(fRunes[j])
				}
			} else {
				sb.WriteRune(fRunes[j])
			}
		}
		if i < len(filledLines)-1 {
			sb.WriteRune('\n')
		}
	}

	content := sb.String()
	if colorCode != "" {
		content = colorCode + content + "\x1b[0m"
	}

	return "```ansi\n" + content + "\n```"
}
