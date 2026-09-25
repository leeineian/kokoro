package ai

import (
	"context"
	"fmt"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
)

type stickerInfo struct {
	ID     snowflake.ID
	Format discord.StickerFormatType
}

func OnMessageCreate(event *events.MessageCreate) {
	var (
		ctx              = context.Background()
		content          string
		isMentioned      bool
		isReply          bool
		author           discord.User
		s                discord.MessageSticker
		a                discord.Attachment
		guildID          snowflake.ID
		primarySticker   string
		primaryAttachID  string
		primaryAttachURL string
		err              error
	)

	if !event.Message.Author.Bot {
		content = event.Message.Content

		if (len(content) > 0 || len(event.Message.StickerItems) > 0 || len(event.Message.Attachments) > 0) && !strings.HasPrefix(content, "/") && !strings.HasPrefix(content, "!") {
			if event.GuildID != nil {
				guildID = *event.GuildID
			}

			if len(event.Message.StickerItems) > 0 {
				primarySticker = event.Message.StickerItems[0].ID.String()
			}
			if len(event.Message.Attachments) > 0 {
				primaryAttachID = event.Message.Attachments[0].ID.String()
				primaryAttachURL = event.Message.Attachments[0].URL
			}

			err = hooks.SaveMessage(ctx, event.Message.ID, guildID, event.ChannelID, content, event.Message.Author.ID, primarySticker, "", primaryAttachID, primaryAttachURL)
			if err != nil {
				hooks.LogError(LogAISaveProactiveFail, err)
			}

			trainStr := content
			if event.Message.ReferencedMessage != nil {
				trainStr = "REPLY: " + trainStr
			}
			if isMentioned {
				if !strings.HasPrefix(trainStr, "REPLY:") {
					trainStr = "MENTION: " + trainStr
				}
			}

			for _, s = range event.Message.StickerItems {
				if trainStr != "" {
					trainStr += " "
				}
				trainStr += fmt.Sprintf("STICKER:%s:%d", s.ID.String(), s.FormatType)
			}
			for _, a = range event.Message.Attachments {
				if trainStr != "" {
					trainStr += " "
				}
				trainStr += "ATTACHMENT:" + a.URL
			}

			if trainStr != "" {
				GlobalAI.Train(event.ChannelID, trainStr)
			}
		}
	}

	if event.Message.Author.Bot {
		return
	}

	isMentioned = false
	for _, author = range event.Message.Mentions {
		if author.ID == event.Client().ID() {
			isMentioned = true
			break
		}
	}

	isReply = false
	if event.Message.ReferencedMessage != nil && event.Message.ReferencedMessage.Author.ID == event.Client().ID() {
		isReply = true
	}

	if !isMentioned && !isReply {
		if AIRandomResponseChance <= 0 || rand.Float64() >= AIRandomResponseChance {
			return
		}
	}

	if GlobalAI.IsOnCooldown(event.ChannelID) {
		return
	}

	go generateAndSendAIResponse(ctx, event)
}

func generateAndSendAIResponse(ctx context.Context, event *events.MessageCreate) {
	var (
		begin       string
		prompt      string
		generated   string
		resp        *discord.Message
		err         error
		items       responseItems
		cleaned     string
		info        stickerInfo
		ext         string
		host        string
		param       string
		stickerLink string
		r           string

		sIDs []snowflake.ID
	)

	err = event.Client().Rest.SendTyping(event.ChannelID)
	if err != nil {
		hooks.LogApp(LogAITypingFail, err)
	}

	typingDone := make(chan bool)
	go func() {
		defer func() { recover() }()
		ticker := time.NewTicker(8 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = event.Client().Rest.SendTyping(event.ChannelID)
			case <-typingDone:
				return
			}
		}
	}()
	defer func() { close(typingDone) }()

	prompt = strings.ReplaceAll(event.Message.Content, fmt.Sprintf("<@%s>", event.Client().ID()), "")
	prompt = strings.TrimSpace(prompt)
	begin = prompt

	for i := range 3 {
		seed := begin
		switch i {
		case 1:
			seed = ""
		case 2:
			words := strings.Fields(begin)
			if len(words) > 0 {
				seed = words[len(words)-1]
			} else {
				seed = ""
			}
		}

		generated = GlobalAI.Generate(ctx, *event.Client(), event.ChannelID, seed)
		if generated == "" && seed != "" {
			generated = GlobalAI.Generate(ctx, *event.Client(), event.ChannelID, "")
		}
		if generated == "" {
			generated = MsgAIFallback
		}

		items = parseResponseItems(generated)
		cleaned = items.CleanedText

		if cleaned != "" || len(items.Stickers) > 0 || len(items.ImageURLs) > 0 || len(items.ReactionIDs) > 0 {
			break
		}
		hooks.Log("Empty cleaned text generated, retrying (%s) with seed '%s'...", generated, seed)
	}

	if cleaned == "" && len(items.Stickers) == 0 && len(items.ImageURLs) == 0 && len(items.ReactionIDs) > 0 {
		for _, r = range items.ReactionIDs {
			_ = event.Client().Rest.AddReaction(event.ChannelID, event.MessageID, r)
		}
		return
	}

	if cleaned == "" && len(items.Stickers) == 0 && len(items.ImageURLs) == 0 {
		cleaned = MsgAIFallback
	}

	if items.ShouldPing && !items.ShouldReply {
		cleaned = fmt.Sprintf("<@%s> %s", event.Message.Author.ID.String(), cleaned)
	}

	chunks := hooks.SplitAIMessage(cleaned, 2000)

	for i, chunk := range chunks {
		isLast := i == len(chunks)-1

		msgCreate := discord.NewMessageCreate()

		if items.ShouldReply {
			msgCreate = msgCreate.WithMessageReference(&discord.MessageReference{MessageID: &event.MessageID})
		}

		shouldPing := items.ShouldPing && i == 0
		msgCreate = msgCreate.WithAllowedMentions(&discord.AllowedMentions{
			RepliedUser: shouldPing,
		})

		msgCreate = msgCreate.WithContent(chunk)

		if isLast && len(items.Stickers) > 0 {
			sIDs = make([]snowflake.ID, len(items.Stickers))
			for j, info := range items.Stickers {
				sIDs[j] = info.ID
			}
			msgCreate = msgCreate.WithStickers(sIDs...)
		}

		resp, err = event.Client().Rest.CreateMessage(event.ChannelID, msgCreate)
		if err != nil {
			if strings.Contains(err.Error(), "50081") && isLast {
				for _, info = range items.Stickers {
					ext = "png"
					host = "cdn.discordapp.com"
					param = ""

					switch info.Format {
					case discord.StickerFormatTypeLottie:
						ext = "json"
					case discord.StickerFormatTypeGIF:
						ext = "gif"
						host = "media.discordapp.net"
					case discord.StickerFormatTypeAPNG, discord.StickerFormatTypePNG:
						ext = "png"
					}

					stickerLink = fmt.Sprintf("https://%s/stickers/%s.%s%s", host, info.ID.String(), ext, param)
					_, _ = event.Client().Rest.CreateMessage(event.ChannelID, discord.NewMessageCreate().
						WithMessageReference(&discord.MessageReference{MessageID: &event.MessageID}).
						WithContent(stickerLink))
				}
			} else {
				hooks.LogError(LogAIResponseSendFail, err)
			}
		}

		if isLast && err == nil && len(items.ReactionIDs) > 0 {
			for _, r = range items.ReactionIDs {
				_ = event.Client().Rest.AddReaction(resp.ChannelID, resp.ID, r)
				_ = event.Client().Rest.AddReaction(event.ChannelID, event.MessageID, r)
			}
		}
	}
}

func OnMessageReactionAdd(event *events.MessageReactionAdd) {
	var (
		ctx      = context.Background()
		emojiStr string
		guildID  snowflake.ID
		authorID snowflake.ID
		msg      *discord.Message
		err      error
		name     string
	)

	if event.Emoji.ID != nil {
		name = ""
		if event.Emoji.Name != nil {
			name = *event.Emoji.Name
		}
		emojiStr = fmt.Sprintf("%s:%s", name, event.Emoji.ID.String())
	} else if event.Emoji.Name != nil {
		emojiStr = *event.Emoji.Name
	}

	if event.GuildID != nil {
		guildID = *event.GuildID
	}

	msg, err = event.Client().Rest.GetMessage(event.ChannelID, event.MessageID)
	if err == nil {
		authorID = msg.Author.ID
	}

	err = hooks.SaveMessage(ctx, event.MessageID, guildID, event.ChannelID, "", authorID, "", emojiStr, "", "")
	if err != nil {
		hooks.LogError(LogAISaveReactionFail, err)
	}

	if emojiStr != "" {
		GlobalAI.Train(event.ChannelID, "REACTION:"+emojiStr)
	}
}

type responseItems struct {
	OriginalText string
	CleanedText  string
	Stickers     []stickerInfo
	ImageURLs    []string
	ReactionIDs  []string
	ShouldPing   bool
	ShouldReply  bool
}

func parseResponseItems(content string) responseItems {
	res := responseItems{OriginalText: content}
	cleaned := content

	if strings.Contains(strings.ToUpper(cleaned), "MENTION:") {
		res.ShouldPing = true
		cleaned = regexp.MustCompile(`(?i)MENTION:`).ReplaceAllString(cleaned, "")
	}

	if strings.Contains(strings.ToUpper(cleaned), "REPLY:") {
		res.ShouldReply = true
		cleaned = regexp.MustCompile(`(?i)REPLY:`).ReplaceAllString(cleaned, "")
	}

	stickerRegex := regexp.MustCompile(`(?i)STICKER:([\w-]+)(?::(\d+))?`)
	matches := stickerRegex.FindAllStringSubmatch(cleaned, 1)
	if len(matches) > 0 {
		id, err := snowflake.Parse(matches[0][1])
		if err == nil {
			info := stickerInfo{ID: id, Format: discord.StickerFormatTypePNG}
			if matches[0][2] != "" {
				fmtVal, _ := strconv.Atoi(matches[0][2])
				info.Format = discord.StickerFormatType(fmtVal)
			}
			res.Stickers = []stickerInfo{info}
		}
		cleaned = stickerRegex.ReplaceAllString(cleaned, "")
	}

	attachmentRegex := regexp.MustCompile(`(?i)ATTACHMENT:([^\s>]+)`)
	attachmentMatches := attachmentRegex.FindAllStringSubmatch(cleaned, -1)
	for _, m := range attachmentMatches {
		res.ImageURLs = append(res.ImageURLs, m[1])
	}
	cleaned = attachmentRegex.ReplaceAllString(cleaned, "")

	reactionRegex := regexp.MustCompile(`(?i)REACTION:([^\s]+)`)
	reactionMatches := reactionRegex.FindAllStringSubmatch(cleaned, -1)
	for _, m := range reactionMatches {
		res.ReactionIDs = append(res.ReactionIDs, m[1])
	}
	cleaned = reactionRegex.ReplaceAllString(cleaned, "")

	cleaned = strings.TrimSpace(cleaned)
	cleaned = strings.TrimLeft(cleaned, ".,!?;: ")
	cleaned = strings.TrimRight(cleaned, ".,!?;: ")

	if strings.Count(cleaned, "```")%2 != 0 {
		idx := strings.LastIndex(cleaned, "```")
		if idx != -1 {
			cleaned = cleaned[:idx]
		}
	}

	for {
		cleaned = strings.TrimSpace(cleaned)
		if cleaned == "" {
			break
		}
		lastChar := cleaned[len(cleaned)-1]
		if strings.ContainsRune("*_~`[(:-,", rune(lastChar)) {
			cleaned = cleaned[:len(cleaned)-1]
			continue
		}
		break
	}

	tokenSuffixes := []string{"STICKER:", "ATTACHMENT:", "REACTION:", "MENTION:", "REPLY:"}
	for _, t := range tokenSuffixes {
		upperCleaned := strings.ToUpper(cleaned)
		stem := strings.TrimSuffix(t, ":")
		if strings.HasSuffix(upperCleaned, stem) || strings.HasSuffix(upperCleaned, t) {
			idx := strings.LastIndex(upperCleaned, stem)
			if idx != -1 {
				cleaned = cleaned[:idx]
			}
		}
	}

	numberRegex := regexp.MustCompile(`\n?\s*\d+\.$`)
	cleaned = numberRegex.ReplaceAllString(cleaned, "")

	if strings.Contains(cleaned, "<@") {
		lastOpenIdx := strings.LastIndex(cleaned, "<@")
		lastCloseIdx := strings.LastIndex(cleaned, ">")
		if lastOpenIdx > lastCloseIdx {
			cleaned = cleaned[:lastOpenIdx]
		}
	}

	if strings.Contains(cleaned, "[") {
		lastOpenBracket := strings.LastIndex(cleaned, "[")
		lastCloseBracket := strings.LastIndex(cleaned, "]")
		lastOpenParen := strings.LastIndex(cleaned, "(")
		lastCloseParen := strings.LastIndex(cleaned, ")")

		if lastOpenParen > lastCloseBracket && lastOpenParen > lastCloseParen {
			cleaned = cleaned[:lastOpenParen]
		}
		if lastOpenBracket > lastCloseBracket {
			cleaned = cleaned[:lastOpenBracket]
		}
	}

	lines := strings.Split(cleaned, "\n")
	if len(lines) > 0 {
		lastLine := lines[len(lines)-1]
		for _, wrap := range []string{"***", "**", "*", "__", "_", "`"} {
			if strings.Count(lastLine, wrap)%2 != 0 {
				trimmed := strings.TrimSpace(lastLine)
				if (wrap == "*" || wrap == "_" || wrap == "-") &&
					(strings.HasPrefix(trimmed, wrap+" ") || strings.HasPrefix(trimmed, wrap+"\t")) {
					continue
				}

				idx := strings.LastIndex(lastLine, wrap)
				if idx != -1 {
					lastLine = lastLine[:idx]
				}
			}
		}
		lines[len(lines)-1] = lastLine
		cleaned = strings.Join(lines, "\n")
	}

	cleaned = strings.TrimSpace(cleaned)
	res.CleanedText = cleaned
	return res
}
