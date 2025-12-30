package home

import (
	"encoding/json"
	"net/http"

	"github.com/bwmarrin/discordgo"
	"github.com/leeineian/minder/sys"
)

func handleCatImage(s *discordgo.Session, i *discordgo.InteractionCreate) {
	resp, err := catHttpClient.Get("https://api.thecatapi.com/v1/images/search")
	if err != nil {
		sys.LogCat(sys.MsgCatFailedToFetchImage, err)
		catRespondErrorSync(s, i, sys.ErrCatFailedToFetchImage)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		sys.LogCat(sys.MsgCatImageAPIStatusError, resp.StatusCode)
		catRespondErrorSync(s, i, sys.ErrCatImageServiceUnavailable)
		return
	}

	var data []struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		sys.LogCat(sys.MsgCatFailedToDecodeImage, err)
		catRespondErrorSync(s, i, sys.ErrCatFailedToDecodeImage)
		return
	}

	if len(data) == 0 {
		sys.LogCat(sys.MsgCatImageAPIEmptyArray)
		catRespondErrorSync(s, i, sys.ErrCatNoImagesAvailable)
		return
	}

	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsIsComponentsV2,
			Components: []discordgo.MessageComponent{
				&discordgo.Container{
					Components: []discordgo.MessageComponent{
						&discordgo.MediaGallery{
							Items: []discordgo.MediaGalleryItem{
								{
									Media: discordgo.UnfurledMediaItem{URL: data[0].URL},
								},
							},
						},
					},
				},
			},
		},
	})
	if err != nil {
		sys.LogCat(sys.MsgCatFailedToSendErrorResponse, err)
	}
}
