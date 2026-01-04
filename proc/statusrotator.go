package proc

import (
	"context"
	"fmt"
	"math/rand"
	"regexp"
	"strconv"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/gateway"
	"github.com/leeineian/minder/sys"
)

func init() {
	sys.OnClientReady(func(ctx context.Context, client *bot.Client) {
		sys.RegisterDaemon(sys.LogStatusRotator, func(ctx context.Context) { StartStatusRotator(ctx, client) })
	})
}

func GetRotationInterval() time.Duration {
	return time.Duration(15+rand.Intn(46)) * time.Second
}

var (
	StartTime       = time.Now().UTC()
	statusList      []func(context.Context, *bot.Client) string
	lastStatusText  string
	configKeyStatus = "status_visible"
)

func StartStatusRotator(ctx context.Context, client *bot.Client) {
	statusList = []func(context.Context, *bot.Client) string{
		GetRemindersStatus,
		GetColorStatus,
		GetUptimeStatus,
		GetLatencyStatus,
		GetTimeStatus,
	}

	go func() {
		for {
			next := GetRotationInterval()
			updateStatus(ctx, client, next)
			select {
			case <-time.After(next):
			case <-ctx.Done():
				return
			}
		}
	}()
}

func updateStatus(ctx context.Context, client *bot.Client, nextInterval time.Duration) {
	if client == nil {
		return
	}

	visibleStr, err := sys.GetBotConfig(ctx, configKeyStatus)
	if err != nil || visibleStr == "false" {
		client.SetPresence(ctx, gateway.WithOnlineStatus(discord.OnlineStatusOnline))
		return
	}

	// 1. Gather all non-empty statuses
	var availableStatuses []string
	for _, gen := range statusList {
		if text := gen(ctx, client); text != "" {
			availableStatuses = append(availableStatuses, text)
		}
	}

	// 2. Fallback to Uptime if everything is empty (shouldn't happen as Uptime is always non-empty)
	if len(availableStatuses) == 0 {
		availableStatuses = append(availableStatuses, GetUptimeStatus(ctx, client))
	}

	// 3. Filter out the last shown status to prevent repeats
	var finalChoices []string
	for _, s := range availableStatuses {
		if s != lastStatusText {
			finalChoices = append(finalChoices, s)
		}
	}

	// 4. Pick a status
	var selectedStatus string
	if len(finalChoices) > 0 {
		selectedStatus = finalChoices[rand.Intn(len(finalChoices))]
	} else {
		// If lastStatusText was the only option, we have to use it
		selectedStatus = availableStatuses[0]
	}
	lastStatusText = selectedStatus

	err = client.SetPresence(ctx,
		gateway.WithOnlineStatus(discord.OnlineStatusOnline),
		gateway.WithStreamingActivity(selectedStatus, sys.GlobalConfig.StreamingURL),
	)

	if err != nil {
		sys.LogStatusRotator(sys.MsgStatusUpdateFail, err)
	} else {
		// Colorize hex codes in the log but not the presence
		logStatus := selectedStatus
		re := regexp.MustCompile(`#([A-Fa-f0-9]{6})`)
		logStatus = re.ReplaceAllStringFunc(selectedStatus, func(match string) string {
			colorInt, _ := strconv.ParseUint(match[1:], 16, 64)
			return sys.ColorizeHex(int(colorInt))
		})

		if nextInterval > 0 {
			sys.LogStatusRotator(sys.MsgStatusRotated, logStatus, nextInterval)
		} else {
			sys.LogStatusRotator(sys.MsgStatusRotatedNoInterval, logStatus)
		}
	}
}

// Generators

func GetRemindersStatus(ctx context.Context, client *bot.Client) string {
	count, _ := sys.GetRemindersCount(ctx)
	if count == 0 {
		return ""
	}
	return fmt.Sprintf("Reminder: %d", count)
}

func GetColorStatus(ctx context.Context, client *bot.Client) string {
	nextUpdate, guildID, found := GetNextUpdate(ctx)
	if !found {
		return ""
	}
	currentColor := GetCurrentColor(ctx, client, guildID)
	if currentColor == "" {
		return ""
	}
	diff := time.Until(nextUpdate)
	return fmt.Sprintf("Color: %s in %dm", currentColor, int(diff.Minutes()))
}

func GetUptimeStatus(ctx context.Context, client *bot.Client) string {
	uptime := time.Since(StartTime)
	return fmt.Sprintf("Uptime: %dh %dm %ds", int(uptime.Hours()), int(uptime.Minutes())%60, int(uptime.Seconds())%60)
}

func GetLatencyStatus(ctx context.Context, client *bot.Client) string {
	ping := client.Gateway.Latency()
	if ping == 0 {
		return ""
	}
	return fmt.Sprintf("Ping: %dms", ping.Milliseconds())
}

func GetTimeStatus(ctx context.Context, client *bot.Client) string {
	return "Time: " + time.Now().Local().Format("15:04:05") + " (Local)"
}
