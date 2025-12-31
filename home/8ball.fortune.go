package home

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/bwmarrin/discordgo"
	"github.com/leeineian/minder/sys"
)

func handleEightballFortune(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	question := ""
	for _, opt := range options {
		if opt.Name == "question" {
			question = opt.StringValue()
		}
	}

	resp, err := eightballHttpClient.Get("https://eightballapi.com/api")
	if err != nil {
		sys.LogEightball(sys.MsgEightballFailedToFetchFortune, err)
		eightballRespondErrorSync(s, i, sys.ErrEightballFailedToFetchFortune)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		sys.LogEightball(sys.MsgEightballFortuneAPIStatusError, resp.StatusCode)
		eightballRespondErrorSync(s, i, sys.ErrEightballServiceUnavailable)
		return
	}

	var data struct {
		Reading string `json:"reading"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		sys.LogEightball(sys.MsgEightballFailedToDecodeFortune, err)
		eightballRespondErrorSync(s, i, sys.ErrEightballFailedToDecode)
		return
	}

	content := data.Reading
	if question != "" {
		content = fmt.Sprintf("**Question:** %s\n**Fortune:** %s", question, data.Reading)
	}

	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsIsComponentsV2,
			Components: []discordgo.MessageComponent{
				&discordgo.Container{
					Components: []discordgo.MessageComponent{
						&discordgo.TextDisplay{Content: content},
					},
				},
			},
		},
	})
	if err != nil {
		sys.LogEightball(sys.MsgEightballFailedToSendError, err)
	}
}
