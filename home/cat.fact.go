package home

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
)

const apiURL = "https://catfact.ninja/fact"

type CatFact struct {
	Fact   string `json:"fact"`
	Length int    `json:"length"`
}

func handleCatFact(event *events.ApplicationCommandInteractionCreate) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		event.CreateMessage(discord.NewMessageCreateBuilder().
			SetIsComponentsV2(true).
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay("❌ Failed to fetch cat fact."),
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

	var data CatFact
	if err := json.Unmarshal(body, &data); err != nil {
		event.CreateMessage(discord.NewMessageCreateBuilder().
			SetIsComponentsV2(true).
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay("❌ Failed to parse cat fact."),
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
				discord.NewTextDisplay(data.Fact + " 🐱"),
			),
		).
		Build())
}
