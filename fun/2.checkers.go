package fun

import (
	"fmt"
	"math/rand"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
)

const (
	checkersRows = 8
	checkersCols = 8

	checkersEmpty         = "⬛"
	checkersWhiteTile     = "⬜"
	checkersTarget        = "🔲"
	checkersP1Piece       = "🔴"
	checkersP2Piece       = "🔵"
	checkersP1King        = "❤️"
	checkersP2King        = "💙"
	checkersP1Highlight   = "🟥"
	checkersP2Highlight   = "🟦"
	checkersStatusTurn    = MsgGameTurn
	checkersStatusWin     = MsgGameWin
	checkersStatusDraw    = MsgGameDraw
	checkersStatusForfeit = MsgGameForfeitSuccess
)

type CheckersPieceType int

const (
	PieceNone CheckersPieceType = iota
	PieceP1
	PieceP2
	PieceP1King
	PieceP2King
)

type CheckersGame struct {
	board         [checkersRows][checkersCols]CheckersPieceType
	player1ID     snowflake.ID
	player2ID     snowflake.ID
	isAI          bool
	aiDifficulty  string
	aiPlayerNum   int
	colorVariant  GameColorVariant
	currentTurn   int
	gameOver      bool
	winner        int
	moveCount     int
	lastMoveTime  time.Time
	messageID     snowflake.ID
	channelID     snowflake.ID
	selectedPiece *[2]int
	lastMoveDest  *[2]int
}

// ===========================
// Checkers Logic - Core
// ===========================

func NewCheckersGame(p1, p2 snowflake.ID, isAI bool, aiPlayerNum int, difficulty string) *CheckersGame {
	variant := VariantStandard
	if !isAI && rand.Intn(2) == 1 {
		variant = VariantInverted
	}
	game := &CheckersGame{
		player1ID:    p1,
		player2ID:    p2,
		isAI:         isAI,
		aiPlayerNum:  aiPlayerNum,
		aiDifficulty: difficulty,
		colorVariant: variant,
		currentTurn:  1,
		lastMoveTime: time.Now(),
	}
	game.InitBoard()
	return game
}

func (g *CheckersGame) InitBoard() {
	for r := range checkersRows {
		for c := range checkersCols {
			if (r+c)%2 == 1 {
				if r < 3 {
					g.board[r][c] = PieceP2
				} else if r > 4 {
					g.board[r][c] = PieceP1
				} else {
					g.board[r][c] = PieceNone
				}
			} else {
				g.board[r][c] = PieceNone
			}
		}
	}
}

// ===========================
// Checkers Interaction Handlers
// ===========================

func HandlePlayCheckers(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	defer func() {
		if r := recover(); r != nil {
			hooks.LogError("Panic in HandlePlayCheckers: %v", r)
			fmt.Printf("%s\n", debug.Stack())
		}
	}()

	var opponentID *snowflake.ID
	isAI := true
	var p2 snowflake.ID

	if opponent, ok := data.OptUser("opponent"); ok {
		opponentID = &opponent.ID
		isAI = opponent.Bot
	}

	difficulty := "normal"
	if diff, ok := data.OptString("difficulty"); ok {
		difficulty = diff
	}

	appID := event.ApplicationID()
	p1 := event.User().ID

	userActiveGameMu.Lock()
	if gid, ok := userActiveGame[p1]; ok {
		userActiveGameMu.Unlock()
		event.CreateMessage(discord.NewMessageCreate().WithContent(fmt.Sprintf(MsgGameAlreadyActive, gid)).WithEphemeral(true))
		return
	}
	if opponentID != nil && *opponentID != appID {
		if gid, ok := userActiveGame[*opponentID]; ok {
			userActiveGameMu.Unlock()
			event.CreateMessage(discord.NewMessageCreate().WithContent(fmt.Sprintf(MsgGameOpponentActive, *opponentID, gid)).WithEphemeral(true))
			return
		}
	}

	cid := event.Channel().ID()
	gameID := fmt.Sprintf(CIDCheckersGameID, cid, time.Now().UnixNano())

	userActiveGame[p1] = gameID
	if opponentID != nil && *opponentID != appID {
		userActiveGame[*opponentID] = gameID
	}
	userActiveGameMu.Unlock()

	if opponentID != nil {
		p2 = *opponentID
		if p2 == appID {
			isAI = true
		} else {
			isAI = false
		}
	} else {
		p2 = appID
		isAI = true
	}

	var player1, player2 snowflake.ID
	if rand.Intn(2) == 0 {
		player1 = p1
		player2 = p2
	} else {
		player1 = p2
		player2 = p1
	}

	aiPlayerNum := 0
	if isAI {
		if player1 == appID || (opponentID != nil && player1 == *opponentID && isAI) {
			aiPlayerNum = 1
		} else {
			aiPlayerNum = 2
		}
	}

	game := NewCheckersGame(player1, player2, isAI, aiPlayerNum, difficulty)

	activeCheckersGamesMu.Lock()
	activeCheckersGames[gameID] = game
	activeCheckersGamesMu.Unlock()

	msg := CheckersBuildMessage(game, gameID, "")
	if err := hooks.RespondInteractionContainerV2(*event.Client(), event, msg, false); err != nil {
		hooks.LogError("Failed to send checkers message: %v", err)
		activeCheckersGamesMu.Lock()
		delete(activeCheckersGames, gameID)
		activeCheckersGamesMu.Unlock()

		userActiveGameMu.Lock()
		delete(userActiveGame, game.player1ID)
		delete(userActiveGame, game.player2ID)
		userActiveGameMu.Unlock()
		return
	}

	resp, err := event.Client().Rest.GetInteractionResponse(event.ApplicationID(), event.Token())
	if err == nil && resp != nil {
		game.messageID = resp.ID
		game.channelID = cid
	} else {
		hooks.LogError("Failed to fetch interaction response: %v", err)
	}

	if game.isAI && game.aiPlayerNum == 1 {
		time.AfterFunc(1*time.Second, func() {
			CheckersMakeAIMove(*event.Client(), game, gameID)
		})
	}
}

func HandleCheckersInteraction(event *events.ComponentInteractionCreate) {
	defer func() {
		if r := recover(); r != nil {
			hooks.LogError("Panic in HandleCheckersInteraction: %v", r)
			fmt.Printf("%s\n", debug.Stack())
		}
	}()

	parts := strings.Split(event.Data.CustomID(), ":")
	if len(parts) < 3 {
		return
	}

	gameID := parts[1]
	action := parts[2]

	activeCheckersGamesMu.Lock()
	game, exists := activeCheckersGames[gameID]
	if !exists {
		activeCheckersGamesMu.Unlock()
		event.CreateMessage(discord.NewMessageCreate().WithContent(MsgGameNotFound).WithEphemeral(true))
		return
	}

	userID := event.User().ID
	if userID != game.player1ID && userID != game.player2ID {
		activeCheckersGamesMu.Unlock()
		event.CreateMessage(discord.NewMessageCreate().WithContent("You are not part of this game.").WithEphemeral(true))
		return
	}

	isP1 := userID == game.player1ID
	if (game.currentTurn == 1 && !isP1) || (game.currentTurn == 2 && isP1) && action != "forfeit" {
		activeCheckersGamesMu.Unlock()
		event.CreateMessage(discord.NewMessageCreate().WithContent(MsgGameNotTurn).WithEphemeral(true))
		return
	}

	switch action {
	case "select_piece":
		values := event.StringSelectMenuInteractionData().Values
		if len(values) > 0 {
			coords := strings.Split(values[0], ",")
			r, _ := strconv.Atoi(coords[0])
			c, _ := strconv.Atoi(coords[1])
			game.selectedPiece = &[2]int{r, c}
		}
	case "move_to":
		values := event.StringSelectMenuInteractionData().Values
		if len(values) > 0 {
			coords := strings.Split(values[0], ",")
			r, _ := strconv.Atoi(coords[0])
			c, _ := strconv.Atoi(coords[1])
			if game.selectedPiece != nil {
				CheckersMakeMove(game, game.selectedPiece[0], game.selectedPiece[1], r, c)
				game.selectedPiece = nil
				game.currentTurn = 3 - game.currentTurn
				if CheckersCheckWin(game) {
					game.gameOver = true
					userActiveGameMu.Lock()
					delete(userActiveGame, game.player1ID)
					delete(userActiveGame, game.player2ID)
					userActiveGameMu.Unlock()
				}
			}
		}
	case "cancel_select":
		game.selectedPiece = nil
	case "forfeit":
		game.gameOver = true
		game.winner = 3 - game.currentTurn
		if isP1 {
			game.winner = 2
		} else {
			game.winner = 1
		}
		statusMsg := fmt.Sprintf(checkersStatusForfeit, userID, game.winner)
		if userID == game.player1ID {
			statusMsg = fmt.Sprintf(checkersStatusForfeit, game.player1ID, game.player2ID)
		} else {
			statusMsg = fmt.Sprintf(checkersStatusForfeit, game.player2ID, game.player1ID)
		}

		msg := CheckersBuildMessage(game, gameID, statusMsg)
		hooks.UpdateInteractionContainerV2(*event.Client(), event, msg)

		userActiveGameMu.Lock()
		delete(userActiveGame, game.player1ID)
		delete(userActiveGame, game.player2ID)
		userActiveGameMu.Unlock()
		return

	case "claim_win":
		if !CheckersIsHopeless(game) {
			event.CreateMessage(discord.NewMessageCreate().WithContent(MsgGameHopelessFail).WithEphemeral(true))
			return
		}

		game.gameOver = true
		game.winner = 1
		if userID == game.player2ID {
			game.winner = 2
		}

		statusMsg := fmt.Sprintf(MsgGameClaimWinSuccess, userID)
		msg := CheckersBuildMessage(game, gameID, statusMsg)

		hooks.UpdateInteractionContainerV2(*event.Client(), event, msg)

		userActiveGameMu.Lock()
		delete(userActiveGame, game.player1ID)
		delete(userActiveGame, game.player2ID)
		userActiveGameMu.Unlock()
		return

	}

	msg := CheckersBuildMessage(game, gameID, "")
	activeCheckersGamesMu.Unlock()

	_ = hooks.UpdateInteractionContainerV2(*event.Client(), event, msg)

	if !game.gameOver && game.isAI && game.currentTurn == game.aiPlayerNum {
		time.AfterFunc(1*time.Second, func() {
			CheckersMakeAIMove(*event.Client(), game, gameID)
		})
	}
}

// ===========================
// Checkers Logic - Mechanics
// ===========================

func CheckersMakeMove(g *CheckersGame, r1, c1, r2, c2 int) {
	piece := g.board[r1][c1]
	g.board[r2][c2] = piece
	g.board[r1][c1] = PieceNone
	g.lastMoveDest = &[2]int{r2, c2}

	dr := r2 - r1
	dc := c2 - c1
	if dr*dr > 1 {
		midR := r1 + dr/2
		midC := c1 + dc/2
		g.board[midR][midC] = PieceNone
	}

	if piece == PieceP1 && r2 == 0 {
		g.board[r2][c2] = PieceP1King
	} else if piece == PieceP2 && r2 == checkersRows-1 {
		g.board[r2][c2] = PieceP2King
	}
}

func CheckersCheckWin(g *CheckersGame) bool {
	p1Count, p2Count := 0, 0
	for r := range checkersRows {
		for c := range checkersCols {
			p := g.board[r][c]
			if p == PieceP1 || p == PieceP1King {
				p1Count++
			}
			if p == PieceP2 || p == PieceP2King {
				p2Count++
			}
		}
	}

	if p1Count == 0 {
		g.winner = 2
		return true
	}
	if p2Count == 0 {
		g.winner = 1
		return true
	}

	validMoves1 := CheckersGetValidMoves(g, 1)
	if len(validMoves1) == 0 {
		g.winner = 2
		return true
	}
	validMoves2 := CheckersGetValidMoves(g, 2)
	if len(validMoves2) == 0 {
		g.winner = 1
		return true
	}

	return false
}

// GetValidMoves returns a map of "r,c" -> list of valid target "r,c"
func CheckersGetValidMoves(g *CheckersGame, player int) map[string][][2]int {
	moves := make(map[string][][2]int)

	for r := range checkersRows {
		for c := range checkersCols {
			p := g.board[r][c]
			isP1 := p == PieceP1 || p == PieceP1King
			isP2 := p == PieceP2 || p == PieceP2King

			if (player == 1 && isP1) || (player == 2 && isP2) {
				var dirs [][2]int
				if p == PieceP1 {
					dirs = [][2]int{{-1, -1}, {-1, 1}}
				}
				if p == PieceP2 {
					dirs = [][2]int{{1, -1}, {1, 1}}
				}
				if p == PieceP1King || p == PieceP2King {
					dirs = [][2]int{{-1, -1}, {-1, 1}, {1, -1}, {1, 1}}
				}

				for _, d := range dirs {
					targetR, targetC := r+d[0], c+d[1]
					if CheckersIsValidPos(targetR, targetC) && g.board[targetR][targetC] == PieceNone {
						k := fmt.Sprintf("%d,%d", r, c)
						moves[k] = append(moves[k], [2]int{targetR, targetC})
					}

					jumpR, jumpC := r+2*d[0], c+2*d[1]
					if CheckersIsValidPos(jumpR, jumpC) && g.board[jumpR][jumpC] == PieceNone {
						midR, midC := r+d[0], c+d[1]
						midP := g.board[midR][midC]
						isOpponent := false
						if player == 1 && (midP == PieceP2 || midP == PieceP2King) {
							isOpponent = true
						}
						if player == 2 && (midP == PieceP1 || midP == PieceP1King) {
							isOpponent = true
						}

						if isOpponent {
							k := fmt.Sprintf("%d,%d", r, c)
							moves[k] = append(moves[k], [2]int{jumpR, jumpC})
						}
					}
				}
			}
		}
	}
	return moves
}

func CheckersIsValidPos(r, c int) bool {
	return r >= 0 && r < checkersRows && c >= 0 && c < checkersCols
}

// CheckersIsHopeless checks if the AI is in a hopeless position
func CheckersIsHopeless(game *CheckersGame) bool {
	if !game.isAI {
		return false
	}

	aiPiece := PieceP2
	aiKingPiece := PieceP2King
	playerPiece := PieceP1
	playerKingPiece := PieceP1King

	if game.aiPlayerNum == 1 {
		aiPiece = PieceP1
		aiKingPiece = PieceP1King
		playerPiece = PieceP2
		playerKingPiece = PieceP2King
	}

	aiCount := 0
	aiKingCount := 0
	playerCount := 0
	playerKingCount := 0

	for r := 0; r < checkersRows; r++ {
		for c := 0; c < checkersCols; c++ {
			p := game.board[r][c]
			switch p {
			case aiPiece:
				aiCount++
			case aiKingPiece:
				aiKingCount++
			case playerPiece:
				playerCount++
			case playerKingPiece:
				playerKingCount++
			}
		}
	}

	totalAI := aiCount + aiKingCount
	totalPlayer := playerCount + playerKingCount

	if aiKingCount == 0 && playerKingCount > 0 && totalAI < totalPlayer-1 {
		return true
	}
	if totalAI <= 2 && totalPlayer >= 3 {
		return true
	}

	return false
}

// ===========================
// Checkers Rendering & Helpers
// ===========================

func CheckersBuildMessage(game *CheckersGame, gameID string, statusMsg string) any {
	var sb strings.Builder

	// Determine visual mapping based on randomization
	p1Piece, p2Piece := checkersP1Piece, checkersP2Piece
	p1King, p2King := checkersP1King, checkersP2King
	p1Highlight, p2Highlight := checkersP1Highlight, checkersP2Highlight

	if game.colorVariant == VariantInverted {
		p1Piece, p2Piece = checkersP2Piece, checkersP1Piece
		p1King, p2King = checkersP2King, checkersP1King
		p1Highlight, p2Highlight = checkersP2Highlight, checkersP1Highlight
	}

	reverse := false
	if game.isAI {
		if game.aiPlayerNum == 1 {
			reverse = true
		}
	} else if game.currentTurn == 2 {
		reverse = true
	}

	getHeader := func(rev bool) string {
		var hsb strings.Builder
		if !rev {
			for i := 0; i < checkersCols; i++ {
				hsb.WriteString(BoardColumnEmojis[i])
			}
		} else {
			for i := checkersCols - 1; i >= 0; i-- {
				hsb.WriteString(BoardColumnEmojis[i])
			}
		}
		return hsb.String()
	}

	headerStr := BoardCorner + getHeader(reverse) + BoardCorner + "\n"
	sb.WriteString(headerStr)

	rStart, rEnd, rStep := 0, checkersRows, 1
	cStart, cEnd, cStep := 0, checkersCols, 1

	if reverse {
		rStart, rEnd, rStep = checkersRows-1, -1, -1
		cStart, cEnd, cStep = checkersCols-1, -1, -1
	}

	for r := rStart; r != rEnd; r += rStep {
		sb.WriteString(BoardRowEmojis[r])
		for c := cStart; c != cEnd; c += cStep {
			p := game.board[r][c]

			isTarget := false
			isSelected := (game.selectedPiece != nil && game.selectedPiece[0] == r && game.selectedPiece[1] == c)
			isLastMove := (game.lastMoveDest != nil && game.lastMoveDest[0] == r && game.lastMoveDest[1] == c)

			if game.selectedPiece != nil {
				k := fmt.Sprintf("%d,%d", game.selectedPiece[0], game.selectedPiece[1])
				validMoves := CheckersGetValidMoves(game, game.currentTurn)
				if targets, ok := validMoves[k]; ok {
					for _, t := range targets {
						if t[0] == r && t[1] == c {
							isTarget = true
							break
						}
					}
				}
			}

			if isSelected || isLastMove {
				switch p {
				case PieceP1, PieceP1King:
					sb.WriteString(p1Highlight)
				case PieceP2, PieceP2King:
					sb.WriteString(p2Highlight)
				default:
					if (r+c)%2 == 1 {
						sb.WriteString(checkersEmpty)
					} else {
						sb.WriteString(checkersWhiteTile)
					}
				}
			} else if isTarget {
				sb.WriteString(checkersTarget)
			} else {
				switch p {
				case PieceNone:
					if (r+c)%2 == 1 {
						sb.WriteString(checkersEmpty)
					} else {
						sb.WriteString(checkersWhiteTile)
					}
				case PieceP1:
					sb.WriteString(p1Piece)
				case PieceP2:
					sb.WriteString(p2Piece)
				case PieceP1King:
					sb.WriteString(p1King)
				case PieceP2King:
					sb.WriteString(p2King)
				}
			}
		}
		sb.WriteString(BoardRowEmojis[r])
		sb.WriteString("\n")
	}
	sb.WriteString(headerStr)

	p1Count, p2Count := 0, 0
	for r := range checkersRows {
		for c := range checkersCols {
			p := game.board[r][c]
			if p == PieceP1 || p == PieceP1King {
				p1Count++
			}
			if p == PieceP2 || p == PieceP2King {
				p2Count++
			}
		}
	}
	scoreStr := fmt.Sprintf("-# %s <@%d>: **%d** | %s <@%d>: **%d**",
		p1Piece, game.player1ID, p1Count,
		p2Piece, game.player2ID, p2Count)

	var statusSB strings.Builder
	if game.gameOver {
		if statusMsg != "" {
			statusSB.WriteString(statusMsg)
		} else if game.winner == 0 {
			statusSB.WriteString(fmt.Sprintf(checkersStatusDraw, game.player1ID, game.player2ID))
		} else {
			winnerID := game.player1ID
			loserID := game.player2ID
			if game.winner == 2 {
				winnerID = game.player2ID
				loserID = game.player1ID
			}
			statusSB.WriteString(fmt.Sprintf(checkersStatusWin, loserID, winnerID))
		}
	} else {
		currentPlayer := game.player1ID
		statusIcon := p1Piece
		hasKing := false

		checkType := PieceP1King
		if game.currentTurn == 2 {
			currentPlayer = game.player2ID
			statusIcon = p2Piece
			checkType = PieceP2King
		}

		for r := range checkersRows {
			for c := range checkersCols {
				if game.board[r][c] == checkType {
					hasKing = true
					break
				}
			}
			if hasKing {
				break
			}
		}

		if hasKing {
			if game.currentTurn == 1 {
				statusIcon = p1King
			} else {
				statusIcon = p2King
			}
		}
		statusSB.WriteString(fmt.Sprintf(checkersStatusTurn, currentPlayer, statusIcon))

		if statusMsg != "" {
			statusSB.WriteString("\n")
			statusSB.WriteString(statusMsg)
		}
	}

	var components []interface{}
	components = append(components, hooks.NewTextDisplay(sb.String()))
	components = append(components, hooks.NewTextDisplay(scoreStr))
	components = append(components, hooks.NewTextDisplay(statusSB.String()))

	if !game.gameOver {
		validMoves := CheckersGetValidMoves(game, game.currentTurn)
		selectedKey := ""
		if game.selectedPiece != nil {
			selectedKey = fmt.Sprintf("%d,%d", game.selectedPiece[0], game.selectedPiece[1])
		}

		var pieceOptions []discord.StringSelectMenuOption
		for k, targets := range validMoves {
			if len(targets) > 0 {
				if selectedKey != "" && k == selectedKey {
					continue
				}

				coords := strings.Split(k, ",")
				r, _ := strconv.Atoi(coords[0])
				c, _ := strconv.Atoi(coords[1])
				label := fmt.Sprintf("Row %d, Col %c", r+1, 'A'+c)
				pieceOptions = append(pieceOptions, discord.NewStringSelectMenuOption(label, k))
			}
		}
		sort.Slice(pieceOptions, func(i, j int) bool { return pieceOptions[i].Label < pieceOptions[j].Label })

		if game.selectedPiece != nil {
			k := fmt.Sprintf("%d,%d", game.selectedPiece[0], game.selectedPiece[1])
			if targets, ok := validMoves[k]; ok {
				var targetOptions []discord.StringSelectMenuOption
				for _, t := range targets {
					label := fmt.Sprintf("Row %d, Col %c", t[0]+1, 'A'+t[1])
					val := fmt.Sprintf("%d,%d", t[0], t[1])
					targetOptions = append(targetOptions, discord.NewStringSelectMenuOption(label, val))
				}
				if len(targetOptions) > 25 {
					targetOptions = targetOptions[:25]
				}
				menu := discord.NewStringSelectMenu(fmt.Sprintf("checkers:%s:move_to", gameID), LabelSelectDest, targetOptions...)
				components = append(components, discord.NewActionRow(menu))

				cancelBtn := discord.NewButton(discord.ButtonStyleSecondary, "Cancel Selection", fmt.Sprintf("checkers:%s:cancel_select", gameID), "", 0)
				components = append(components, discord.NewActionRow(cancelBtn))
			}
		} else {
			if len(pieceOptions) > 25 {
				pieceOptions = pieceOptions[:25]
			}
			placeholder := LabelSelectPieceMove
			if game.selectedPiece != nil {
				r, c := game.selectedPiece[0], game.selectedPiece[1]
				placeholder = fmt.Sprintf("Selected: Row %d, Col %c", r+1, 'A'+c)
			}
			if len(pieceOptions) > 0 {
				menu := discord.NewStringSelectMenu(fmt.Sprintf("checkers:%s:select_piece", gameID), placeholder, pieceOptions...)
				components = append(components, discord.NewActionRow(menu))
			}
		}

		var utilityRow []discord.InteractiveComponent
		utilityRow = append(utilityRow, discord.NewButton(discord.ButtonStyleDanger, LabelForfeit, fmt.Sprintf(CIDCheckersForfeit, gameID), "", 0))

		if CheckersIsHopeless(game) {
			utilityRow = append(utilityRow, discord.NewButton(discord.ButtonStyleSuccess, LabelClaimWin, fmt.Sprintf(CIDCheckersClaimWin, gameID), "", 0))
		}

		components = append(components, discord.NewActionRow(utilityRow...))
	} else {
		components = append(components, hooks.NewSeparator(true))
		components = append(components, discord.NewActionRow(
			discord.NewButton(discord.ButtonStyleSecondary, LabelPlayAgain, "checkers:disabled", "", 0).WithDisabled(true),
		))
	}

	return hooks.NewV2Container(components...)
}

// ===========================
// Checkers AI

// CheckersAIMove represents a possible move for the AI
type CheckersAIMove struct {
	r1, c1, r2, c2 int
	isJump         bool
}

func CheckersMakeAIMove(client bot.Client, game *CheckersGame, gameID string) {
	activeCheckersGamesMu.Lock()
	defer activeCheckersGamesMu.Unlock()

	if game.gameOver || game.currentTurn != game.aiPlayerNum || !game.isAI {
		return
	}

	validMoves := CheckersGetValidMoves(game, game.aiPlayerNum)
	if len(validMoves) == 0 {
		game.gameOver = true
		game.winner = 3 - game.aiPlayerNum

		userActiveGameMu.Lock()
		delete(userActiveGame, game.player1ID)
		delete(userActiveGame, game.player2ID)
		userActiveGameMu.Unlock()

		msg := CheckersBuildMessage(game, gameID, MsgGameAINoMoves)
		_, _ = hooks.EditContainerV2(client, game.channelID, game.messageID, msg, nil, nil)
		return
	}

	var candidateMoves []CheckersAIMove

	for k, targets := range validMoves {
		coords := strings.Split(k, ",")
		r1, _ := strconv.Atoi(coords[0])
		c1, _ := strconv.Atoi(coords[1])
		for _, t := range targets {
			m := CheckersAIMove{r1: r1, c1: c1, r2: t[0], c2: t[1]}
			if (t[0]-r1)*(t[0]-r1) > 1 {
				m.isJump = true
			}
			candidateMoves = append(candidateMoves, m)
		}
	}

	if len(candidateMoves) > 0 {
		var jumpMoves []CheckersAIMove
		for _, m := range candidateMoves {
			if m.isJump {
				jumpMoves = append(jumpMoves, m)
			}
		}

		var possibleMoves []CheckersAIMove
		if len(jumpMoves) > 0 {
			possibleMoves = jumpMoves
		} else {
			possibleMoves = candidateMoves
		}

		var selectedMove CheckersAIMove
		switch game.aiDifficulty {
		case "easy":
			selectedMove = possibleMoves[rand.Intn(len(possibleMoves))]
		case "normal":
			selectedMove = checkersAINormalMove(game, possibleMoves)
		case "hard":
			selectedMove = checkersAIHardMove(game, possibleMoves)
		default:
			selectedMove = possibleMoves[rand.Intn(len(possibleMoves))]
		}

		CheckersMakeMove(game, selectedMove.r1, selectedMove.c1, selectedMove.r2, selectedMove.c2)
		game.lastMoveTime = time.Now()
		game.currentTurn = 3 - game.aiPlayerNum

		if CheckersCheckWin(game) {
			game.gameOver = true
			userActiveGameMu.Lock()
			delete(userActiveGame, game.player1ID)
			delete(userActiveGame, game.player2ID)
			userActiveGameMu.Unlock()
		}
	}

	msg := CheckersBuildMessage(game, gameID, "")
	hooks.EditContainerV2(client, game.channelID, game.messageID, msg, nil, nil)
}

// checkersAINormalMove prioritizes promotion (Kings), then random
func checkersAINormalMove(game *CheckersGame, moves []CheckersAIMove) CheckersAIMove {
	var promoteMoves []CheckersAIMove

	targetRow := 0
	if game.aiPlayerNum == 2 {
		targetRow = checkersRows - 1
	}

	for _, m := range moves {
		if m.r2 == targetRow {
			p := game.board[m.r1][m.c1]
			isKing := (p == PieceP1King || p == PieceP2King)
			if !isKing {
				promoteMoves = append(promoteMoves, m)
			}
		}
	}

	if len(promoteMoves) > 0 {
		return promoteMoves[rand.Intn(len(promoteMoves))]
	}
	return moves[rand.Intn(len(moves))]
}

// checkersAIHardMove prioritizes Safety -> Promotion -> Random
func checkersAIHardMove(game *CheckersGame, moves []CheckersAIMove) CheckersAIMove {
	var safeMoves []CheckersAIMove

	for _, m := range moves {
		if !checkersIsVulnerable(game, m.r2, m.c2) {
			safeMoves = append(safeMoves, m)
		}
	}

	candidates := safeMoves
	if len(candidates) == 0 {
		candidates = moves
	}

	return checkersAINormalMove(game, candidates)
}

// checkersIsVulnerable checks if a piece at r,c can be jumped by the opponent
func checkersIsVulnerable(game *CheckersGame, r, c int) bool {
	opponent := 1
	if game.aiPlayerNum == 1 {
		opponent = 2
	}

	dirs := [][2]int{{-1, -1}, {-1, 1}, {1, -1}, {1, 1}}

	for _, d := range dirs {
		attackerR, attackerC := r+d[0], c+d[1]

		if !CheckersIsValidPos(attackerR, attackerC) {
			continue
		}

		piece := game.board[attackerR][attackerC]
		if piece == PieceNone {
			continue
		}

		isOpponent := false
		isKing := (piece == PieceP1King || piece == PieceP2King)

		if opponent == 1 && (piece == PieceP1 || piece == PieceP1King) {
			isOpponent = true
		} else if opponent == 2 && (piece == PieceP2 || piece == PieceP2King) {
			isOpponent = true
		}

		if !isOpponent {
			continue
		}

		canAttackDir := false
		if isKing {
			canAttackDir = true
		} else if opponent == 1 && d[0] == 1 {
			canAttackDir = true
		} else if opponent == 2 && d[0] == -1 {
			canAttackDir = true
		}

		if canAttackDir {
			landR, landC := r-d[0], c-d[1]
			if CheckersIsValidPos(landR, landC) && game.board[landR][landC] == PieceNone {
				return true
			}
		}
	}
	return false
}

// ===========================
// Chess Game Constants & Types
// ===========================
