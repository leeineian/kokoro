package home

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

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

	event.CreateMessage(discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay("🎱 " + data.Reading),
			),
		).
		Build())
}
