package home

import (
	"encoding/json"
	"net/http"

	"github.com/bwmarrin/discordgo"
	"github.com/leeineian/minder/sys"
)

func handleCatImage(s *discordgo.Session, i *discordgo.InteractionCreate) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsIsComponentsV2,
		},
	})
	go func() {
		resp, err := catHttpClient.Get("https://api.thecatapi.com/v1/images/search")
		if err != nil {
			sys.LogCat(sys.MsgCatFailedToFetchImage, err)
			catRespondErrorFollowup(s, i, sys.ErrCatFailedToFetchImage)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			sys.LogCat(sys.MsgCatImageAPIStatusError, resp.StatusCode)
			catRespondErrorFollowup(s, i, sys.ErrCatImageServiceUnavailable)
			return
		}

		var data []struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
			sys.LogCat(sys.MsgCatFailedToDecodeImage, err)
			catRespondErrorFollowup(s, i, sys.ErrCatFailedToDecodeImage)
			return
		}

		if len(data) == 0 {
			sys.LogCat(sys.MsgCatImageAPIEmptyArray)
			catRespondErrorFollowup(s, i, sys.ErrCatNoImagesAvailable)
			return
		}

		_, err = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Components: &[]discordgo.MessageComponent{
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
		})
		if err != nil {
			sys.LogCat(sys.MsgCatErrorEditingResponse, err)
		}
	}()
}
