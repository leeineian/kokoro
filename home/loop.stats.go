package home

import (
	"fmt"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/proc"
	"github.com/leeineian/minder/sys"
)

func handleLoopStats(event *events.ApplicationCommandInteractionCreate) {
	guildID := event.GuildID()
	if guildID == nil {
		loopRespond(event, sys.MsgLoopErrGuildOnly, true)
		return
	}

	configs, err := sys.GetAllLoopConfigs(sys.AppContext)
	if err != nil {
		loopRespond(event, sys.MsgLoopErrRetrieveFail, true)
		return
	}

	activeLoops := proc.GetActiveLoops()
	var guildConfigs []*sys.LoopConfig

	for _, cfg := range configs {
		// Filter by guild
		if ch, ok := event.Client().Caches.Channel(cfg.ChannelID); ok {
			if ch.GuildID() == *guildID {
				guildConfigs = append(guildConfigs, cfg)
			}
		}
	}

	if len(guildConfigs) == 0 {
		loopRespond(event, sys.MsgLoopErrNoGuildConfigs, true)
		return
	}

	var sb strings.Builder
	sb.WriteString(sys.MsgLoopStatsHeader)

	for _, cfg := range guildConfigs {
		state := activeLoops[cfg.ChannelID]
		emoji, details := getLoopStatusDetails(cfg, state)

		intervalStr := proc.FormatDuration(proc.IntervalMsToDuration(cfg.Interval))

		sb.WriteString(fmt.Sprintf("%s **#%s**\n", emoji, cfg.ChannelName))
		sb.WriteString(fmt.Sprintf(sys.MsgLoopStatsStatus, details))
		sb.WriteString(fmt.Sprintf(sys.MsgLoopStatsInterval, intervalStr))
		sb.WriteString(fmt.Sprintf(sys.MsgLoopStatsMessage, cfg.Message))
		if cfg.WebhookAuthor != "" {
			sb.WriteString(fmt.Sprintf(sys.MsgLoopStatsAuthor, cfg.WebhookAuthor))
		}
		if cfg.WebhookAvatar != "" {
			sb.WriteString(fmt.Sprintf(sys.MsgLoopStatsAvatar, cfg.WebhookAvatar))
		}
		if cfg.UseThread {
			sb.WriteString(fmt.Sprintf(sys.MsgLoopStatsThreads, cfg.ThreadCount))
			if cfg.ThreadMessage != "" {
				sb.WriteString(fmt.Sprintf(sys.MsgLoopStatsThreadMsg, cfg.ThreadMessage))
			}
		}
		if cfg.VoteChannelID != "" {
			sb.WriteString(fmt.Sprintf(sys.MsgLoopStatsVoteChan, cfg.VoteChannelID))
			if cfg.VoteRole != "" {
				sb.WriteString(fmt.Sprintf(sys.MsgLoopStatsVoteRole, cfg.VoteRole))
			}
			if cfg.VoteMessage != "" {
				sb.WriteString(fmt.Sprintf(sys.MsgLoopStatsVoteMsg, cfg.VoteMessage))
			}
			sb.WriteString(fmt.Sprintf(sys.MsgLoopStatsVoteThreshold, cfg.VoteThreshold))
		}
		sb.WriteString("\n")
	}

	builder := discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay(sb.String()),
			),
		).
		SetEphemeral(true)

	_ = event.CreateMessage(builder.Build())
}
