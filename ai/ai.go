package ai

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"
)

// start

const (
	mrkvStartToken           = "__start"
	mrkvEndToken             = "__end"
	AICleanupInterval        = 10 * time.Minute
	AIModelTTL               = 1 * time.Hour
	TargetHumanMessages      = 100
	MaxScanDepth             = 500
	ChunkSize                = 100
	MinTransitionsToGenerate = 20
	GroupingWindow           = 60 * time.Second
	HistoryDBLimit           = 200
	AICooldownDuration       = 1 * time.Second

	MsgAINotEnoughData = "Not enough data to generate a response. Keep chatting!"
	MsgAIFallback      = "-# ..."

	MsgAICleanSuccess        = "AI memory for this channel has been cleared!"
	MsgAICleanFail           = "Failed to clear AI memory: %v"
	MsgAICleanAllSuccess     = "AI memory has been cleared for ALL channels!"
	MsgInvalidChannelID      = "Invalid channel ID."
	MsgAICleanChannelSuccess = "AI memory has been cleared for <#%s>!"
	MsgAIStatsTemplate       = "### AI Engine Metrics\n**Total Tokens:** %d\n**Loaded Models:** %d\n**Total Transitions (In Memory):** %d\n**Persistence:** ENABLED"
	MsgAIInvalidRegex        = "Invalid regex: %v"
	MsgAICleanHashSuccess    = "AI memory for hash `%s` has been cleared!"
	MsgAICleanContentSuccess = "AI memory for content `%s` has been cleared!"
	MsgAICleanRegexSuccess   = "AI memory for %d items matching `%s` has been cleared!"
	MsgAICleanNoMatch        = "No AI memory matched that regex."
	MsgAIGetMemoryFail       = "Failed to get AI memory: %v"
	MsgAIMemoryEmpty         = "Memory is completely empty."
	MsgAIMemoryDump          = "Here is a complete dump of my AI memory across all channels."

	LogAIDumpFail          = "Error sending AI memory dump: %v"
	LogAITokensLoadFail    = "Failed to load AI tokens: %v"
	LogAIInit              = "Engine ready: %d tokens loaded"
	LogAIHistoryFetchFail  = "Error fetching history chunk (after %d scanned): %v"
	LogAISaveHistoryFail   = "Failed to save AI message from history: %v"
	LogAISaveProactiveFail = "Failed to proactively save AI message: %v"
	LogAITypingFail        = "Failed to send typing: %v"
	LogAIResponseSendFail  = "AI response send failed: %v"
	LogAIReactionAddFail   = "Failed to add response reaction %s: %v"
	LogAIUserReactionFail  = "Failed to add user reaction %s: %v"
	LogAISaveReactionFail  = "Failed to save AI reaction: %v"

	FileAITextMsgs     = "text_messages.txt"
	LabelAITextMsgs    = "Text Messages"
	FileAIStickers     = "stickers_emojis.txt"
	LabelAIStickers    = "Stickers and Emojis"
	FileAIAttachments  = "attachment_links.txt"
	LabelAIAttachments = "Attachment Links"

	DescAICommand       = "AI management commands"
	DescAICleanSub      = "Clean AI memory (granular options available)"
	DescAIHashOption    = "Delete specific hash from vocab"
	DescAIContentOption = "Delete specific content from vocab"
	DescAIRegexOption   = "Delete all vocab matching regex"
	DescAIMemorySub     = "Dump ALL AI memory"
	DescAIStatsSub      = "Show AI engine metrics"
)

const (
	AIMaxLength            = 15
	AIMaxKeySize           = 1
	AIAttempts             = 100
	AITemperatureMin       = 1.0
	AITemperatureMax       = 1.5
	AISeedPrefixChance     = 0.1
	AIRandomResponseChance = 0.0
)

type Hooks struct {
	DB                          *sql.DB
	SaveMessage                 func(ctx context.Context, msgID snowflake.ID, guildID snowflake.ID, channelID snowflake.ID, content string, authorID snowflake.ID, stickerID string, reactions string, attachmentID string, attachmentURL string) error
	GetRecentMessages           func(ctx context.Context, channelID snowflake.ID, limit int) ([]*AIMessageData, error)
	GetMemoryDump               func(ctx context.Context) (*AIMemoryDump, error)
	ClearMessages               func(ctx context.Context, channelID snowflake.ID) error
	ClearAllMessages            func(ctx context.Context) error
	GetAllVocab                 func(ctx context.Context) (map[string]string, error)
	ClearMessagesByHashes       func(ctx context.Context, hashes []string) error
	GetChannelsWithMemory       func(ctx context.Context) ([]string, error)
	FetchAIHistory              func(ctx context.Context, client bot.Client, channelID snowflake.ID) ([]string, error)
	SplitAIMessage              func(content string, limit int) []string
	Log                         func(format string, v ...any)
	LogError                    func(format string, v ...any)
	LogInfo                     func(format string, v ...any)
	LogApp                      func(format string, v ...any)
	RegisterCommand             func(cmd discord.ApplicationCommandCreate, handler func(event *events.ApplicationCommandInteractionCreate))
	RegisterAutocompleteHandler func(cmdName string, handler func(event *events.AutocompleteInteractionCreate))
	RegisterDaemon              func(name string, logger func(format string, v ...any), starter func(ctx context.Context) (bool, func(), func()))
	OnClientReady               func(cb func(ctx context.Context, client bot.Client))
	AdminPerm                   discord.Permissions
}

var hooks Hooks

var GlobalAI *MarkovManager

var (
	keepCasePrefixes   = []string{"http:", "https:", "<a:", "<:", "<t:"}
	normalizedPrefixes = []string{"STICKER:", "REACTION:", "ATTACHMENT:", "MENTION:", "REPLY:"}
	punctuationRegex   = regexp.MustCompile(`^([.,!?;:]+)$`)
	tokenRegex         = regexp.MustCompile(`(?i)(https?://\S+|<a?:\w+:\d+>|<t:\d+(?::[a-zA-Z])?>|<@!?[0-9]+>|<@&[0-9]+>|<#[0-9]+>|(?:STICKER|REACTION|ATTACHMENT|MENTION|REPLY):\S*|[:;xX8][\-~]?[DdPpsS0()\[\]\\/|]|<3|o7|[\w']+|[.,!?;:]+)`)
)

func registerCommands() {
	adminPerm := hooks.AdminPerm
	hooks.RegisterCommand(discord.SlashCommandCreate{
		Name:                     "ai",
		Description:              DescAICommand,
		DefaultMemberPermissions: omit.New(&adminPerm),
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:        "clean",
				Description: DescAICleanSub,
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:        "hash",
						Description: DescAIHashOption,
					},
					discord.ApplicationCommandOptionString{
						Name:        "content",
						Description: DescAIContentOption,
					},
					discord.ApplicationCommandOptionString{
						Name:        "regex",
						Description: DescAIRegexOption,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "memory",
				Description: DescAIMemorySub,
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "stats",
				Description: DescAIStatsSub,
			},
		},
	}, handleAI)
}

func handleAI(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()
	if data.SubCommandName == nil {
		return
	}

	switch *data.SubCommandName {
	case "clean":
		handleAIClean(event)
	case "memory":
		handleAIMemory(event)
	case "stats":
		handleAIStats(event)
	}
}

func handleAIStats(event *events.ApplicationCommandInteractionCreate) {
	var (
		tokenCount int
		modelCount int
		transCount int
		msg        string
		model      *MarkovModel
		nexts      map[int]int
	)

	GlobalAI.mu.RLock()
	tokenCount = len(GlobalAI.Tokens.forward)
	modelCount = len(GlobalAI.Models)
	for _, model = range GlobalAI.Models {
		model.mu.RLock()
		for _, nexts = range model.Transitions {
			transCount += len(nexts)
		}
		model.mu.RUnlock()
	}
	GlobalAI.mu.RUnlock()

	msg = fmt.Sprintf(MsgAIStatsTemplate, tokenCount, modelCount, transCount)

	_ = event.CreateMessage(discord.NewMessageCreate().WithContent(msg).WithEphemeral(true))
}

func handleAIClean(event *events.ApplicationCommandInteractionCreate) {
	var (
		data       = event.SlashCommandInteractionData()
		hashStr    = data.String("hash")
		contentStr = data.String("content")
		regexStr   = data.String("regex")
		ctx        = context.Background()
		err        error
		msg        string
		toDelete   []string
		hash       [32]byte
		hStr       string
		re         *regexp.Regexp
		rErr       error
		vocab      map[string]string
		vErr       error
		h          string
		c          string
	)

	if hashStr != "" {
		err = hooks.ClearMessagesByHashes(ctx, []string{hashStr})
		msg = fmt.Sprintf(MsgAICleanHashSuccess, hashStr)
	} else if contentStr != "" {
		hash = sha256.Sum256([]byte(contentStr))
		hStr = hex.EncodeToString(hash[:])
		err = hooks.ClearMessagesByHashes(ctx, []string{hStr})
		msg = fmt.Sprintf(MsgAICleanContentSuccess, contentStr)
	} else if regexStr != "" {
		re, rErr = regexp.Compile(regexStr)
		if rErr != nil {
			_ = event.CreateMessage(discord.NewMessageCreate().WithContent(fmt.Sprintf(MsgAIInvalidRegex, rErr)).WithEphemeral(true))
			return
		}
		vocab, vErr = hooks.GetAllVocab(ctx)
		if vErr != nil {
			err = vErr
		} else {
			for h, c = range vocab {
				if re.MatchString(c) {
					toDelete = append(toDelete, h)
				}
			}
			if len(toDelete) > 0 {
				err = hooks.ClearMessagesByHashes(ctx, toDelete)
				msg = fmt.Sprintf(MsgAICleanRegexSuccess, len(toDelete), regexStr)
			} else {
				msg = MsgAICleanNoMatch
			}
		}
	} else {
		err = hooks.ClearAllMessages(ctx)
		msg = MsgAICleanAllSuccess
	}

	if err != nil {
		_ = event.CreateMessage(discord.NewMessageCreate().WithContent(fmt.Sprintf(MsgAICleanFail, err)).WithEphemeral(true))
		return
	}

	GlobalAI.Reset()

	_ = event.CreateMessage(discord.NewMessageCreate().WithContent(msg).WithEphemeral(true))
}

func handleAIMemory(event *events.ApplicationCommandInteractionCreate) {
	var (
		dump             *AIMemoryDump
		err              error
		textBuffer       strings.Builder
		stickerBuffer    strings.Builder
		attachmentBuffer strings.Builder
		files            []*discord.File
		msgData          string
		sStr             string
		rStr             string
		url              string
	)

	dump, err = hooks.GetMemoryDump(context.Background())
	if err != nil {
		_ = event.CreateMessage(discord.NewMessageCreate().WithContent(fmt.Sprintf(MsgAIGetMemoryFail, err)).WithEphemeral(true))
		return
	}

	seenText := make(map[string]struct{})
	for _, msgData = range dump.TextMessages {
		if _, ok := seenText[msgData]; !ok {
			textBuffer.WriteString(msgData)
			textBuffer.WriteString("\n")
			seenText[msgData] = struct{}{}
		}
	}

	seenStickers := make(map[string]struct{})
	for _, sStr = range dump.StickerIDs {
		if _, ok := seenStickers[sStr]; !ok {
			stickerBuffer.WriteString(sStr)
			stickerBuffer.WriteString("\n")
			seenStickers[sStr] = struct{}{}
		}
	}
	for _, rStr = range dump.ReactionEmojis {
		if _, ok := seenStickers[rStr]; !ok {
			stickerBuffer.WriteString(rStr)
			stickerBuffer.WriteString("\n")
			seenStickers[rStr] = struct{}{}
		}
	}

	seenAttachments := make(map[string]struct{})
	for _, url = range dump.AttachmentURLs {
		if _, ok := seenAttachments[url]; !ok {
			attachmentBuffer.WriteString(url)
			attachmentBuffer.WriteString("\n")
			seenAttachments[url] = struct{}{}
		}
	}

	if textBuffer.Len() > 0 {
		files = append(files, discord.NewFile(FileAITextMsgs, LabelAITextMsgs, strings.NewReader(textBuffer.String())))
	}
	if stickerBuffer.Len() > 0 {
		files = append(files, discord.NewFile(FileAIStickers, LabelAIStickers, strings.NewReader(stickerBuffer.String())))
	}
	if attachmentBuffer.Len() > 0 {
		files = append(files, discord.NewFile(FileAIAttachments, LabelAIAttachments, strings.NewReader(attachmentBuffer.String())))
	}

	if len(files) == 0 {
		_ = event.CreateMessage(discord.NewMessageCreate().WithContent(MsgAIMemoryEmpty).WithEphemeral(true))
		return
	}

	_ = event.CreateMessage(discord.NewMessageCreate().
		WithContent(MsgAIMemoryDump).
		WithFiles(files...).
		WithEphemeral(true))
}

func Init(h Hooks) {
	hooks = h

	GlobalAI = NewMarkovManager()

	hooks.OnClientReady(func(ctx context.Context, client bot.Client) {
		hooks.RegisterDaemon("AI", hooks.Log, func(ctx context.Context) (bool, func(), func()) {
			return true, nil, func() {
				GlobalAI.StartCleanup()
			}
		})
	})

	registerCommands()
}

// end

type AIMessageData struct {
	MessageID snowflake.ID
	Content   string
	AuthorID  snowflake.ID
	CreatedAt time.Time
}

type AIMemoryDump struct {
	TextMessages   []string
	StickerIDs     []string
	ReactionEmojis []string
	AttachmentURLs []string
}

// utils start

// utils end
