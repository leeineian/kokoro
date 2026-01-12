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

func handleCatFact(event *events.ApplicationCommandInteractionCreate) {
	resp, err := sys.HttpClient.Get(apiURL)
	if err != nil {
		event.CreateMessage(discord.NewMessageCreateBuilder().
			SetIsComponentsV2(true).
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay(fmt.Sprintf(sys.MsgCatFactAPIUnreachable, err)),
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
					discord.NewTextDisplay(fmt.Sprintf(sys.MsgCatAPIStatusErrorDisp, resp.StatusCode, resp.Status)),
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
					discord.NewTextDisplay(sys.MsgCatDataError),
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
					discord.NewTextDisplay(fmt.Sprintf(sys.MsgCatFormatErrorExt, err)),
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
