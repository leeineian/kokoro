package home

import (
	"encoding/json"
	"net/http"

	"github.com/bwmarrin/discordgo"
	"github.com/leeineian/minder/sys"
)

func handleCatFact(s *discordgo.Session, i *discordgo.InteractionCreate) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsIsComponentsV2,
		},
	})
	go func() {
		resp, err := catHttpClient.Get("https://catfact.ninja/fact")
		if err != nil {
			sys.LogCat(sys.MsgCatFailedToFetchFact, err)
			catRespondErrorFollowup(s, i, sys.ErrCatFailedToFetchFact)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			sys.LogCat(sys.MsgCatFactAPIStatusError, resp.StatusCode)
			catRespondErrorFollowup(s, i, sys.ErrCatFactServiceUnavailable)
			return
		}

		var data struct {
			Fact string `json:"fact"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
			sys.LogCat(sys.MsgCatFailedToDecodeFact, err)
			catRespondErrorFollowup(s, i, sys.ErrCatFailedToDecodeFact)
			return
		}

		_, err = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Components: &[]discordgo.MessageComponent{
				&discordgo.Container{
					Components: []discordgo.MessageComponent{
						&discordgo.TextDisplay{Content: data.Fact},
					},
				},
			},
		})
		if err != nil {
			sys.LogCat(sys.MsgCatErrorEditingResponse, err)
		}
	}()
}
