package home

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/sys"
)

const apiURL = "https://catfact.ninja/fact"

type CatFact struct {
	Fact   string `json:"fact"`
	Length int    `json:"length"`
}

func handleCatFact(event *events.ApplicationCommandInteractionCreate) {
	resp, err := sys.HttpClient.Get(apiURL)
	if err != nil {
		event.CreateMessage(discord.NewMessageCreateBuilder().
			SetIsComponentsV2(true).
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay("❌ **API Unreachable**: The cat fact service is currently offline or timing out.\n> _" + err.Error() + "_"),
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

	var data CatFact
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

	event.CreateMessage(discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay(data.Fact + " 🐱"),
			),
		).
		Build())
}
