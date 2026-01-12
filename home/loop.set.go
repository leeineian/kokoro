package home

import (
	"fmt"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
	"github.com/leeineian/minder/proc"
	"github.com/leeineian/minder/sys"
)

// handleLoopSet configures a channel for looping
func handleLoopSet(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	channelIDStr, _ := data.OptString("category")
	channelID, err := snowflake.Parse(channelIDStr)
	if err != nil {
		loopRespond(event, sys.MsgLoopErrInvalidChannel, true)
		return
	}

	// Always defer to be safe
	_ = event.DeferCreateMessage(true)

	go func() {
		channel, ok := event.Client().Caches.Channel(channelID)
		if !ok {
			loopRespond(event, sys.MsgLoopErrChannelFetchFail, true)
			return
		}

		// Validate that channel is a category
		if channel.Type() != discord.ChannelTypeGuildCategory {
			loopRespond(event, sys.MsgLoopErrOnlyCategories, true)
			return
		}

		// Load existing config if available for partial updates
		existing, _ := sys.GetLoopConfig(sys.AppContext, channelID)

		message := "@everyone"
		if msg, ok := data.OptString("message"); ok {
			message = msg
		} else if existing != nil {
			message = existing.Message
		}

		webhookAuthor := "LoopHook"
		if author, ok := data.OptString("webhook_author"); ok {
			webhookAuthor = author
		} else if existing != nil {
			webhookAuthor = existing.WebhookAuthor
		}

		webhookAvatar := ""
		if avatar, ok := data.OptString("webhook_avatar"); ok {
			webhookAvatar = avatar
		} else if existing != nil {
			webhookAvatar = existing.WebhookAvatar
		} else {
			if guild, ok := event.Client().Caches.Guild(*event.GuildID()); ok {
				if icon := guild.IconURL(); icon != nil {
					webhookAvatar = *icon
				}
			}
		}

		threadMessage := ""
		if tmsg, ok := data.OptString("thread_message"); ok {
			threadMessage = tmsg
		} else if existing != nil {
			threadMessage = existing.ThreadMessage
		}

		threadCount := 0
		if count, ok := data.OptInt("thread_count"); ok {
			threadCount = count
		} else if existing != nil {
			threadCount = existing.ThreadCount
		}

		voteChannelID := ""
		if vc, ok := data.OptChannel("vote_channel"); ok {
			voteChannelID = vc.ID.String()
		} else if existing != nil {
			voteChannelID = existing.VoteChannelID
		}

		voteRole := ""
		if vr, ok := data.OptRole("vote_role"); ok {
			voteRole = vr.ID.String()
		} else if existing != nil {
			voteRole = existing.VoteRole
		}

		voteMessage := ""
		if vm, ok := data.OptString("vote_message"); ok {
			voteMessage = strings.ReplaceAll(vm, "\\n", "\n")
		} else if existing != nil {
			voteMessage = existing.VoteMessage
		}

		voteThreshold := 0
		if vt, ok := data.OptInt("vote_threshold"); ok {
			if vt < 0 {
				vt = 0
			}
			if vt > 100 {
				vt = 100
			}
			voteThreshold = vt
		} else if existing != nil {
			voteThreshold = existing.VoteThreshold
		}

		config := &sys.LoopConfig{
			ChannelID:     channelID,
			ChannelName:   channel.Name(),
			ChannelType:   "category",
			Rounds:        0,
			Interval:      0, // Default to infinite random mode
			Message:       message,
			WebhookAuthor: webhookAuthor,
			WebhookAvatar: webhookAvatar,
			UseThread:     threadCount > 0,
			ThreadMessage: threadMessage,
			ThreadCount:   threadCount,
			VoteChannelID: voteChannelID,
			VoteRole:      voteRole,
			VoteMessage:   voteMessage,
			VoteThreshold: voteThreshold,
		}

		if err := proc.SetLoopConfig(sys.AppContext, event.Client(), channelID, config); err != nil {
			loopRespond(event, fmt.Sprintf(sys.MsgLoopSaveFail, err), true)
			return
		}

		loopRespond(event, fmt.Sprintf(
			sys.MsgLoopConfiguredDisp,
			channel.Name(),
		), true)
	}()
}
