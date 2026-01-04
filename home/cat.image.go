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

type CatImage struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

func handleCatImage(event *events.ApplicationCommandInteractionCreate) {
	resp, err := sys.HttpClient.Get(catImageApiURL)
	if err != nil {
		event.CreateMessage(discord.NewMessageCreateBuilder().
			SetIsComponentsV2(true).
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay("❌ **API Unreachable**: The cat image service is currently offline or timing out.\n> _" + err.Error() + "_"),
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

	var data []CatImage
	if err := json.Unmarshal(body, &data); err != nil || len(data) == 0 {
		errorMsg := "❌ **Format Error**: The API returned data in an invalid format."
		if len(data) == 0 && err == nil {
			errorMsg = "❌ **Empty Result**: The API returned an empty list of images."
		} else if err != nil {
			errorMsg += "\n> _" + err.Error() + "_"
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
