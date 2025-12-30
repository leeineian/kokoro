package home

import (
	"sort"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/leeineian/minder/sys"
)

func handleTestError(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	var errorKey string
	if len(options) > 0 {
		errorKey = options[0].StringValue()
	}

	errors := sys.GetUserErrors()
	content, ok := errors[errorKey]
	if !ok {
		content = "❌ Invalid error key selected."
	}

	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Components: []discordgo.MessageComponent{
				&discordgo.Container{
					Components: []discordgo.MessageComponent{
						&discordgo.TextDisplay{Content: content},
					},
				},
			},
			Flags: discordgo.MessageFlagsIsComponentsV2 | discordgo.MessageFlagsEphemeral,
		},
	})

	if err != nil {
		sys.LogDebug(sys.MsgDebugTestErrorSendFail, err)
	}
}

func handleDebugAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()

	// Robustly find the focused option at any nesting level
	var focused *discordgo.ApplicationCommandInteractionDataOption
	var findFocused func([]*discordgo.ApplicationCommandInteractionDataOption)
	findFocused = func(opts []*discordgo.ApplicationCommandInteractionDataOption) {
		for _, opt := range opts {
			if opt.Focused {
				focused = opt
				return
			}
			if len(opt.Options) > 0 {
				findFocused(opt.Options)
			}
		}
	}
	findFocused(data.Options)

	if focused == nil {
		return
	}

	// Route based on option name
	switch focused.Name {
	case "key":
		handleTestErrorAutocomplete(s, i, focused)
	case "target":
		debugWebhookLooperAutocomplete(s, i)
	}
}

func handleTestErrorAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate, focused *discordgo.ApplicationCommandInteractionDataOption) {
	errorMap := sys.GetUserErrors()
	keys := make([]string, 0, len(errorMap))
	for k := range errorMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var choices []*discordgo.ApplicationCommandOptionChoice
	input := focused.StringValue()

	for _, key := range keys {
		if input == "" || strings.Contains(strings.ToLower(key), strings.ToLower(input)) {
			choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
				Name:  key,
				Value: key,
			})
		}
		if len(choices) >= 25 {
			break
		}
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{
			Choices: choices,
		},
	})
}
