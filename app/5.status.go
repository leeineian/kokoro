package app

import (
	"context"
	"fmt"
	"math/rand"
	"regexp"
	"strconv"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
)

func GetRotationInterval() time.Duration {
	return time.Duration(15+rand.Intn(46)) * time.Second
}

func StartStatusRotator(ctx context.Context, client bot.Client) (bool, func(), func()) {
	statusMap = map[string]func(context.Context, bot.Client) string{
		"Reminders": GetRemindersStatus,
		"Color":     GetColorStatus,
		"Uptime":    GetUptimeStatus,
		"Latency":   GetLatencyStatus,
		"Time":      GetTimeStatus,
	}

	statusKeys = []string{StatusDisableAll, StatusEnableAll}
	for k := range statusMap {
		statusKeys = append(statusKeys, k)
	}

	next := GetRotationInterval()
	updateStatus(ctx, client, next)

	return true, func() {
			for {
				select {
				case <-time.After(next):
					next = GetRotationInterval()
					updateStatus(ctx, client, next)
				case <-ctx.Done():
					return
				}
			}
		}, func() {
			hooks.LogApp(MsgStatusRotatorShutdown)
		}
}

func updateStatus(ctx context.Context, client bot.Client, nextInterval time.Duration) {
	visibleStr, err := hooks.GetAppConfig(ctx, configKeyStatus)
	if err != nil || visibleStr == "false" {
		err := client.SetPresence(ctx, gateway.WithOnlineStatus(discord.OnlineStatusOnline), gateway.WithPlayingActivity(""))
		if err != nil {
			hooks.LogApp(MsgStatusClearFail, err)
		}
		return
	}

	var (
		availableStatuses []string
		finalChoices      []string
		selectedStatus    string
	)

	pinnedStatus, _ := hooks.GetAppConfig(ctx, configKeyPin)
	if pinnedStatus != "" {
		if gen, ok := statusMap[pinnedStatus]; ok {
			text := gen(ctx, client)
			if text != "" {
				client.SetPresence(ctx,
					gateway.WithOnlineStatus(discord.OnlineStatusOnline),
					gateway.WithStreamingActivity(text, hooks.StreamingURL),
				)
				return
			}
		}
	}

	for _, gen := range statusMap {
		if text := gen(ctx, client); text != "" {
			availableStatuses = append(availableStatuses, text)
		}
	}

	if len(availableStatuses) == 0 {
		availableStatuses = append(availableStatuses, GetUptimeStatus(ctx, client))
	}

	statusMu.RLock()
	last := lastStatusText
	statusMu.RUnlock()

	for _, s := range availableStatuses {
		if s != last {
			finalChoices = append(finalChoices, s)
		}
	}

	if len(finalChoices) > 0 {
		selectedStatus = finalChoices[rand.Intn(len(finalChoices))]
	} else {
		selectedStatus = availableStatuses[0]
	}

	statusMu.Lock()
	lastStatusText = selectedStatus
	statusMu.Unlock()

	err = client.SetPresence(ctx,
		gateway.WithOnlineStatus(discord.OnlineStatusOnline),
		gateway.WithStreamingActivity(selectedStatus, hooks.StreamingURL),
	)

	if err != nil {
		hooks.LogApp(MsgStatusUpdateFail, err)
	} else {
		logStatus := selectedStatus
		re := regexp.MustCompile(`#([A-Fa-f0-9]{6})`)
		logStatus = re.ReplaceAllStringFunc(selectedStatus, func(match string) string {
			colorInt, _ := strconv.ParseUint(match[1:], 16, 64)
			return ColorizeHex(int(colorInt))
		})

		if nextInterval > 0 {
			hooks.LogApp(MsgStatusRotated, logStatus, nextInterval)
		} else {
			hooks.LogApp(MsgStatusRotatedNoInterval, logStatus)
		}
	}
}

func GetRemindersStatus(ctx context.Context, client bot.Client) string {
	count, _ := hooks.GetRemindersCount(ctx)
	if count == 0 {
		return ""
	}
	return fmt.Sprintf("Reminder: %d", count)
}

func GetColorStatus(ctx context.Context, client bot.Client) string {
	nextUpdate, guildID, found := GetNextUpdate(ctx)
	if !found {
		return ""
	}
	if guildID == 0 { // Snowflake ID is uint64, check for 0 not nil
		return ""
	}
	currentColor := GetCurrentColor(ctx, client, guildID)
	if currentColor == "" {
		return ""
	}
	diff := time.Until(nextUpdate)
	return fmt.Sprintf("Color: %s in %dm", currentColor, int(diff.Minutes()))
}

func GetUptimeStatus(ctx context.Context, client bot.Client) string {
	uptime := time.Since(hooks.StartupTime)
	return fmt.Sprintf("Uptime: %dh %dm %ds", int(uptime.Hours()), int(uptime.Minutes())%60, int(uptime.Seconds())%60)
}

func GetLatencyStatus(ctx context.Context, client bot.Client) string {
	ping := client.Gateway.Latency()
	if ping == 0 {
		return ""
	}
	return fmt.Sprintf("Ping: %dms", ping.Milliseconds())
}

func GetTimeStatus(ctx context.Context, client bot.Client) string {
	return fmt.Sprintf(MsgStatusTime, time.Now().Local().Format("15:04:05"))
}

func handleBotStatus(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	selection := data.String("select")
	var (
		msg string
	)

	switch selection {
	case StatusDisableAll:
		hooks.SetAppConfig(hooks.AppContext, configKeyStatus, "false")
		hooks.SetAppConfig(hooks.AppContext, configKeyPin, "")
		msg = MsgBotStatusDisabled
	case StatusEnableAll:
		hooks.SetAppConfig(hooks.AppContext, configKeyStatus, "true")
		hooks.SetAppConfig(hooks.AppContext, configKeyPin, "")
		msg = MsgBotStatusEnabled
	default:
		if _, ok := statusMap[selection]; ok {
			hooks.SetAppConfig(hooks.AppContext, configKeyStatus, "true")
			hooks.SetAppConfig(hooks.AppContext, configKeyPin, selection)
			msg = fmt.Sprintf(MsgBotStatusPinned, selection)
		} else {
			msg = MsgBotStatusInvalid
		}
	}

	safeGo(func() {
		updateStatus(hooks.AppContext, *event.Client(), 0)
	})

	err := hooks.RespondInteractionV2(*event.Client(), event, msg, true)
	if err != nil {
		hooks.LogDebug(MsgDebugStatusCmdFail, err)
	}
}
