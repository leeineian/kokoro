package home

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/sys"
)

const catImageApiURL = "https://api.thecatapi.com/v1/images/search"

func handleCatImage(event *events.ApplicationCommandInteractionCreate) {
	resp, err := sys.HttpClient.Get(catImageApiURL)
	if err != nil {
		event.CreateMessage(discord.NewMessageCreateBuilder().
			SetIsComponentsV2(true).
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay(fmt.Sprintf(sys.MsgCatImageAPIUnreachable, err)),
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

	var data []CatImage
	if err := json.Unmarshal(body, &data); err != nil || len(data) == 0 {
		errorMsg := sys.MsgCatFormatError
		if len(data) == 0 && err == nil {
			errorMsg = sys.MsgCatImageEmptyResult
		} else if err != nil {
			errorMsg = fmt.Sprintf(sys.MsgCatFormatErrorExt, err)
		}

		event.CreateMessage(discord.NewMessageCreateBuilder().
			SetIsComponentsV2(true).
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay(errorMsg),
				),
			).
			SetEphemeral(true).
			Build())
		return
	}

	// Display image using MediaGallery V2 component
	event.CreateMessage(discord.NewMessageCreateBuilder().
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
