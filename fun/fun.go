package fun

import (
	"context"
	"net/http"
	"sync"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
)

type Hooks struct {
	LogInfo                       func(format string, v ...any)
	LogError                      func(format string, v ...any)
	RegisterCommand               func(cmd discord.ApplicationCommandCreate, handler func(event *events.ApplicationCommandInteractionCreate))
	RegisterAutocompleteHandler   func(commandName string, handler func(event *events.AutocompleteInteractionCreate))
	RegisterComponentHandler      func(customID string, handler func(event *events.ComponentInteractionCreate))
	RespondInteractionV2          func(client bot.Client, interaction discord.Interaction, content string, ephemeral bool) error
	RespondInteractionContainerV2 func(client bot.Client, interaction discord.Interaction, container any, ephemeral bool) error
	EditInteractionV2             func(client bot.Client, interaction discord.Interaction, content string) error
	EditInteractionContainerV2    func(client bot.Client, interaction discord.Interaction, container any) error
	UpdateInteractionContainerV2  func(client bot.Client, interaction discord.Interaction, container any) error
	EditContainerV2               func(client bot.Client, channelID snowflake.ID, messageID snowflake.ID, container any, stickers []snowflake.ID, embeds []discord.Embed) (*discord.Message, error)
	NewV2Container                func(components ...interface{}) any
	NewTextDisplay                func(content string) any
	NewMediaGallery               func(urls ...string) any
	NewSeparator                  func(divider bool) any
	AppContext                    context.Context
	HttpClient                    interface {
		Get(url string) (*http.Response, error)
	}
}

var hooks Hooks

// ===========================
// Global State & Initialization
// ===========================

func Init(h Hooks) {
	hooks = h

	hooks.RegisterCommand(discord.SlashCommandCreate{
		Name:        "fun",
		Description: "Fun commands and games",
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommandGroup{
				Name:        "cat",
				Description: "Cat related commands",
				Options: []discord.ApplicationCommandOptionSubCommand{
					{
						Name:        "fact",
						Description: "Get a random cat fact",
					},
					{
						Name:        "image",
						Description: "Get a random cat image",
					},
					{
						Name:        "say",
						Description: "Cowsay but cat",
						Options: []discord.ApplicationCommandOption{
							discord.ApplicationCommandOptionString{
								Name:        "message",
								Description: "The message for the cat to say",
								Required:    true,
								MaxLength:   intPtr(2000),
								MinLength:   intPtr(1),
							},
							discord.ApplicationCommandOptionString{
								Name:        "msgcolor",
								Description: "Color of the message text",
								Required:    false,
								Choices:     getCatColorChoices(),
							},
							discord.ApplicationCommandOptionString{
								Name:        "bubcolor",
								Description: "Color of the speech bubble",
								Required:    false,
								Choices:     getCatColorChoices(),
							},
							discord.ApplicationCommandOptionString{
								Name:        "catcolor",
								Description: "Color of the cat",
								Required:    false,
								Choices:     getCatColorChoices(),
							},
							discord.ApplicationCommandOptionString{
								Name:        "expression",
								Description: "The cat's facial expression",
								Required:    false,
								Choices:     getCatExpressionChoices(),
							},
						},
					},
					{
						Name:        "stats",
						Description: "View cat system status and details",
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        CmdConnect4,
				Description: CmdConnect4Desc,
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionUser{
						Name:        OptOpponent,
						Description: OptOpponentDesc,
						Required:    false,
					},
					discord.ApplicationCommandOptionString{
						Name:        OptDifficulty,
						Description: OptDifficultyDesc,
						Required:    false,
						Choices: []discord.ApplicationCommandOptionChoiceString{
							{Name: "Easy", Value: ChoiceEasy},
							{Name: "Normal", Value: ChoiceNormal},
							{Name: "Hard", Value: ChoiceHard},
						},
					},
					discord.ApplicationCommandOptionInt{
						Name:        OptTimer,
						Description: OptTimerDesc,
						Required:    false,
					},
					discord.ApplicationCommandOptionString{
						Name:        OptSize,
						Description: OptSizeDesc,
						Required:    false,
						Choices: []discord.ApplicationCommandOptionChoiceString{
							{Name: "Small (5x4)", Value: ChoiceSmall},
							{Name: "Classic (7x6)", Value: ChoiceClassic},
							{Name: "Large (9x8)", Value: ChoiceLarge},
							{Name: "Master (10x10)", Value: ChoiceMaster},
						},
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        CmdCheckers,
				Description: CmdCheckersDesc,
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionUser{
						Name:        OptOpponent,
						Description: OptOpponentDesc,
						Required:    false,
					},
					discord.ApplicationCommandOptionString{
						Name:        OptDifficulty,
						Description: OptDifficultyDesc,
						Required:    false,
						Choices: []discord.ApplicationCommandOptionChoiceString{
							{Name: "Easy", Value: ChoiceEasy},
							{Name: "Normal", Value: ChoiceNormal},
							{Name: "Hard", Value: ChoiceHard},
						},
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        CmdChess,
				Description: CmdChessDesc,
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionUser{
						Name:        OptOpponent,
						Description: OptOpponentDesc,
						Required:    false,
					},
					discord.ApplicationCommandOptionString{
						Name:        OptDifficulty,
						Description: OptDifficultyDesc,
						Required:    false,
						Choices: []discord.ApplicationCommandOptionChoiceString{
							{Name: "Easy", Value: ChoiceEasy},
							{Name: "Normal", Value: ChoiceNormal},
							{Name: "Hard", Value: ChoiceHard},
						},
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "undertext",
				Description: "Generate an Undertale/Deltarune style text box image",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:        "message",
						Description: "The text to display in the box",
						Required:    true,
					},
					discord.ApplicationCommandOptionString{
						Name:         "character",
						Description:  "Character to display (e.g. sans, toriel, papyrus)",
						Autocomplete: true,
					},
					discord.ApplicationCommandOptionString{
						Name:        "expression",
						Description: "Character expression (e.g. wink, blush)",
					},
					discord.ApplicationCommandOptionString{
						Name:        "box",
						Description: "Box style",
						Choices: []discord.ApplicationCommandOptionChoiceString{
							{Name: "Undertale", Value: "undertale"},
							{Name: "Underswap", Value: "underswap"},
							{Name: "Underfell", Value: "underfell"},
							{Name: "Octagonal", Value: "octagonal"},
							{Name: "Derp", Value: "derp"},
						},
					},
					discord.ApplicationCommandOptionString{
						Name:        "mode",
						Description: "Text mode",
						Choices: []discord.ApplicationCommandOptionChoiceString{
							{Name: "Normal", Value: "normal"},
							{Name: "Dark World (Deltarune)", Value: "darkworld"},
						},
					},
					discord.ApplicationCommandOptionInt{
						Name:        "size",
						Description: "Box size (1-3)",
						Choices: []discord.ApplicationCommandOptionChoiceInt{
							{Name: "Small", Value: 1},
							{Name: "Medium", Value: 2},
							{Name: "Large", Value: 3},
						},
					},
					discord.ApplicationCommandOptionString{
						Name:        "custom_url",
						Description: "URL for custom character sprite (set character to 'Custom URL')",
					},
					discord.ApplicationCommandOptionString{
						Name:        "boxcolor",
						Description: "Color of the box outline (HEX or name)",
					},
					discord.ApplicationCommandOptionString{
						Name:        "charcolor",
						Description: "Color of the character sprite (HEX)",
					},
					discord.ApplicationCommandOptionString{
						Name:        "font",
						Description: "Font name",
					},
					discord.ApplicationCommandOptionBool{
						Name:        "margin",
						Description: "Whether to have a black margin around the box",
					},
					discord.ApplicationCommandOptionString{
						Name:        "asterisk",
						Description: "Asterisk setting (true/false/color)",
					},
					discord.ApplicationCommandOptionBool{
						Name:        "animated",
						Description: "Generate animated GIF instead of static image",
					},
					discord.ApplicationCommandOptionAttachment{
						Name:        "image",
						Description: "Custom character image (overrides character selection)",
					},
					discord.ApplicationCommandOptionUser{
						Name:        "user",
						Description: "Use a user's avatar as the character",
					},
				},
			},
		},
	}, func(event *events.ApplicationCommandInteractionCreate) {
		data := event.SlashCommandInteractionData()

		subCmdName := data.SubCommandName
		if subCmdName == nil && data.SubCommandGroupName != nil {
			subCmdName = data.SubCommandName
		}

		if data.SubCommandGroupName != nil && *data.SubCommandGroupName == "cat" {
			if subCmdName == nil {
				return
			}
			switch *subCmdName {
			case "stats":
				handleCatStats(event)
			case "fact":
				handleCatFact(event)
			case "image":
				handleCatImage(event)
			case "say":
				handleCatSay(event, data)
			}
			return
		}

		if subCmdName == nil {
			return
		}

		switch *subCmdName {
		case CmdConnect4:
			handlePlayConnect4(event, data)
		case CmdCheckers:
			HandlePlayCheckers(event, data)
		case CmdChess:
			HandlePlayChess(event, data)
		case "undertext":
			HandleUndertext(event)
		}
	})

	hooks.RegisterAutocompleteHandler("fun", func(event *events.AutocompleteInteractionCreate) {
		data := event.Data
		subCmdName := data.SubCommandName
		if subCmdName != nil && *subCmdName == "undertext" {
			UndertextAutocomplete(event)
		}
	})

	hooks.RegisterComponentHandler(CmdConnect4+":", connect4HandleMove)
	hooks.RegisterComponentHandler(CmdCheckers+":", HandleCheckersInteraction)
	hooks.RegisterComponentHandler(CmdChess+":", HandleChessInteraction)
}

const (
	CmdGame         = "game"
	CmdGameDesc     = "Good games."
	CmdConnect4     = "connect4"
	CmdConnect4Desc = "Play Connect Four against another player or AI"
	CmdCheckers     = "checkers"
	CmdCheckersDesc = "Play Checkers (Draughts) against another player or AI"
	CmdChess        = "chess"
	CmdChessDesc    = "Play Chess against another player or AI"

	OptOpponent       = "opponent"
	OptOpponentDesc   = "Challenge another user (leave empty to play against AI)"
	OptDifficulty     = "difficulty"
	OptDifficultyDesc = "AI difficulty level (only for AI games)"
	OptTimer          = "timer"
	OptTimerDesc      = "Turn timer in seconds (leave empty or 0 to disable)"
	OptSize           = "size"
	OptSizeDesc       = "Board size"

	ChoiceEasy    = "easy"
	ChoiceNormal  = "normal"
	ChoiceHard    = "hard"
	ChoiceSmall   = "small"
	ChoiceClassic = "classic"
	ChoiceLarge   = "large"
	ChoiceMaster  = "master"

	MsgGamePanic           = "Panic in %s: %v"
	MsgGameNotFound        = "Game not found or expired."
	MsgGameNotPlayer       = "You're not a player in this game!"
	MsgGameNotTurn         = "It's not your turn!"
	MsgGameAlreadyActive   = "You are already in a game! (ID: %s)"
	MsgGameOpponentActive  = "<@%d> is already in a game! (ID: %s)"
	MsgGameChallengeSelf   = "You cannot challenge yourself!"
	MsgGameForfeitSuccess  = "**<@%d> Forfeited 🛑 - <@%d> Won! 🎉**"
	MsgGameClaimWinSuccess = "**<@%d> Claimed Victory! 🏆**"
	MsgGameDraw            = "**<@%d> and <@%d> ended with a Draw!**"
	MsgGameWin             = "**<@%d> Lost 💩 - <@%d> Won! 🎉**"
	MsgGameTurn            = "**<@%d>'s Turn** %s"
	MsgGameAIHardFallback  = "⚠️ *Hard AI service unavailable, using Normal AI.*"
	MsgGameAINoMoves       = "AI has no moves!"
	MsgGameHopelessFail    = "The AI is not in a hopeless position yet!"
	MsgGameRestarted       = "🔄 Game Restarted!"

	LabelForfeit         = "Forfeit"
	LabelClaimWin        = "Claim Win"
	LabelPlayAgain       = "Play Again?"
	LabelYes             = "Yes!"
	LabelNo              = "No."
	LabelSelectPiece     = "Select Piece..."
	LabelSelectDest      = "Select Destination..."
	LabelSelectPieceMove = "Select a piece to move..."

	CIDConnect4Prefix   = "connect4"
	CIDConnect4Yes      = "connect4:%s:yes"
	CIDConnect4No       = "connect4:%s:no"
	CIDConnect4Forfeit  = "connect4:%s:forfeit"
	CIDConnect4Move     = "connect4:%s:%d"
	CIDConnect4Disabled = "connect4:disabled:info"

	CIDCheckersPrefix   = "checkers"
	CIDCheckersGameID   = "checkers_%d_%d"
	CIDCheckersClaimWin = "checkers:%s:claim_win"
	CIDCheckersForfeit  = "checkers:%s:forfeit"
	CIDCheckersYes      = "checkers:%s:yes"
	CIDCheckersNo       = "checkers:%s:no"

	CIDChessPrefix       = "chess"
	CIDChessMoveTo       = "chess:%s:move_to"
	CIDChessSelectPiece  = "chess:%s:select_piece"
	CIDChessCancelSelect = "chess:%s:cancel_select"
	CIDChessForfeit      = "chess:%s:forfeit"
)

var (
	BoardCorner       = "⏺️"
	BoardColumnEmojis = []string{"🇦\u200b", "🇧\u200b", "🇨\u200b", "🇩\u200b", "🇪\u200b", "🇫\u200b", "🇬\u200b", "🇭\u200b"}
	BoardRowEmojis    = []string{"1️⃣", "2️⃣", "3️⃣", "4️⃣", "5️⃣", "6️⃣", "7️⃣", "8️⃣"}
)

var (
	activeConnect4Games   = make(map[string]*connect4Game)
	activeConnect4GamesMu sync.RWMutex

	activeCheckersGames   = make(map[string]*CheckersGame)
	activeCheckersGamesMu sync.RWMutex

	activeChessGames   = make(map[string]*ChessGame)
	activeChessGamesMu sync.RWMutex

	userActiveGame   = make(map[snowflake.ID]string)
	userActiveGameMu sync.RWMutex
)

type GameColorVariant int

const (
	VariantStandard GameColorVariant = iota
	VariantInverted
)

const (
	connect4ToWin = 4
	connect4P1    = "🔵"
	connect4P2    = "🔴"
	connect4Empty = "⚫"
	connect4P1Win = "🟦"
	connect4P2Win = "🟥"

	connect4StatusTurn     = MsgGameTurn
	connect4StatusDraw     = MsgGameDraw
	connect4StatusWin      = MsgGameWin
	connect4StatusForfeit  = MsgGameForfeitSuccess
	connect4StatusTimeout  = "**<@%d> Took Too Long ⏱️ - <@%d> Won! 🎉**"
	connect4StatusInactive = "**❌ `This game is no longer active.`**"
	connect4StatusFull     = "**❌ `Column is full.`**"
)

var (
	connect4ColumnEmojis = []string{"1️⃣", "2️⃣", "3️⃣", "4️⃣", "5️⃣", "6️⃣", "7️⃣", "8️⃣", "9️⃣", "🔟"}
)

type connect4Difficulty int

const (
	connect4Easy connect4Difficulty = iota
	connect4Normal
	connect4Hard
)
