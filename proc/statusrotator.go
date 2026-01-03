package proc

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/gateway"
	"github.com/leeineian/minder/sys"
)

func init() {
	sys.OnClientReady(func(client *bot.Client) {
		sys.RegisterDaemon(sys.LogStatusRotator, func() { StartStatusRotator(client) })
	})
}

func GetRotationInterval() time.Duration {
	return time.Duration(15+rand.Intn(46)) * time.Second
}

var (
	StartTime       = time.Now()
	statusList      []func(*bot.Client) string
	statusStopChan  chan struct{}
	lastStatusIdx   int = -1
	configKeyStatus     = "status_visible"
)

func StartStatusRotator(client *bot.Client) {
	statusList = []func(*bot.Client) string{
		GetRemindersStatus,
		GetColorStatus,
		GetUptimeStatus,
		GetLatencyStatus,
		GetTimeStatus,
	}

	if statusStopChan != nil {
		close(statusStopChan)
	}
	statusStopChan = make(chan struct{})

	go func() {
		for {
			next := GetRotationInterval()
			updateStatus(client, next)
			select {
			case <-time.After(next):
			case <-statusStopChan:
				return
			}
		}
	}()
}

func TriggerStatusUpdate(client *bot.Client) {
	updateStatus(client, 0)
}

func updateStatus(client *bot.Client, nextInterval time.Duration) {
	if client == nil {
		return
	}

	visibleStr, err := sys.GetBotConfig(context.Background(), configKeyStatus)
	if err != nil || visibleStr == "false" {
		client.SetPresence(context.Background(), gateway.WithOnlineStatus(discord.OnlineStatusOnline))
		return
	}

	idx := rand.Intn(len(statusList))
	if lastStatusIdx != -1 && len(statusList) > 1 && idx == lastStatusIdx {
		idx = (idx + 1) % len(statusList)
	}
	lastStatusIdx = idx

	gen := statusList[idx]
	text := gen(client)
	if text == "" {
		text = GetUptimeStatus(client)
	}

	err = client.SetPresence(context.Background(),
		gateway.WithOnlineStatus(discord.OnlineStatusOnline),
		gateway.WithStreamingActivity(text, sys.GlobalConfig.StreamingURL),
	)

	if err != nil {
		sys.LogStatusRotator(sys.MsgStatusUpdateFail, err)
	} else {
		if nextInterval > 0 {
			sys.LogStatusRotator(sys.MsgStatusRotated, text, nextInterval)
		} else {
			sys.LogStatusRotator(sys.MsgStatusRotatedNoInterval, text)
		}
	}
}

// Generators

func GetRemindersStatus(client *bot.Client) string {
	count, _ := sys.GetRemindersCount(context.Background())
	if count == 0 {
		return ""
	}
	return fmt.Sprintf("Reminder: %d", count)
}

func GetColorStatus(client *bot.Client) string {
	nextUpdate, guildID, found := GetNextUpdate()
	if !found {
		return ""
	}
	currentColor := GetCurrentColor(client, guildID)
	if currentColor == "" {
		return ""
	}
	diff := time.Until(nextUpdate)
	return fmt.Sprintf("Color: %s in %dm", currentColor, int(diff.Minutes()))
}

func GetUptimeStatus(client *bot.Client) string {
	uptime := time.Since(StartTime)
	return fmt.Sprintf("Uptime: %dh %dm", int(uptime.Hours()), int(uptime.Minutes())%60)
}

func GetLatencyStatus(client *bot.Client) string {
	ping := client.Gateway.Latency()
	if ping == 0 {
		return ""
	}
	return fmt.Sprintf("Ping: %dms", ping.Milliseconds())
}

func GetTimeStatus(client *bot.Client) string {
	return "Time: " + time.Now().UTC().Format("15:04") + " UTC"
}
