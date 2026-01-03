package home

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
)

const catImageApiURL = "https://api.thecatapi.com/v1/images/search"

type CatImage struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

func handleCatImage(event *events.ApplicationCommandInteractionCreate) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(catImageApiURL)
	if err != nil {
		event.CreateMessage(discord.NewMessageCreateBuilder().
			SetIsComponentsV2(true).
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay("❌ Failed to fetch cat image."),
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

	var data []CatImage
	if err := json.Unmarshal(body, &data); err != nil || len(data) == 0 {
		event.CreateMessage(discord.NewMessageCreateBuilder().
			SetIsComponentsV2(true).
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay("❌ Failed to parse cat image."),
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
