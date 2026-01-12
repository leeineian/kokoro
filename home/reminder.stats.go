package home

import (
	"fmt"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/sys"
)

func handleReminderStats(event *events.ApplicationCommandInteractionCreate) {
	userID := event.User().ID
	reminders, err := sys.GetRemindersForUser(sys.AppContext, userID)
	if err != nil {
		reminderRespondImmediate(event, sys.ErrReminderFetchFailed)
		return
	}

	if len(reminders) == 0 {
		reminderRespondImmediate(event, sys.MsgReminderNoActive)
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(sys.MsgReminderStatsHeader, len(reminders)))

	for i, r := range reminders {
		if i >= 5 {
			sb.WriteString(fmt.Sprintf(sys.MsgReminderStatsMore, len(reminders)-5))
			break
		}

		relTime := formatReminderRelativeTime(time.Now().UTC(), r.RemindAt)
		truncatedMsg := reminderTruncate(r.Message, 50)

		sb.WriteString(fmt.Sprintf("**%d.** \"%s\"\n", i+1, truncatedMsg))
		sb.WriteString(fmt.Sprintf(sys.MsgReminderStatsDue, relTime, r.RemindAt.Format("Jan 02, 15:04")))
		if r.SendTo == "dm" {
			sb.WriteString(sys.MsgReminderStatsDM)
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
