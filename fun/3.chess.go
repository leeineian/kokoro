package fun

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/corentings/chess/v2"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
)

const (
	chessWhiteSquare = "▫️"
	chessBlackSquare = "▪️"
	chessSelected    = "🟩"

	chessTargetWhite = "◻️"
	chessTargetBlack = "◼️"

	chessWhiteKing   = "🤟🏻"
	chessWhiteQueen  = "🖐🏻"
	chessWhiteRook   = "✊🏻"
	chessWhiteBishop = "👐🏻"
	chessWhiteKnight = "🤙🏻"
	chessWhitePawn   = "☝🏻"

	chessBlackKing   = "🤟🏿"
	chessBlackQueen  = "🖐🏿"
	chessBlackRook   = "✊🏿"
	chessBlackBishop = "👐🏿"
	chessBlackKnight = "🤙🏿"
	chessBlackPawn   = "☝🏿"

	chessStatusTurn    = MsgGameTurn
	chessStatusWin     = MsgGameWin
	chessStatusDraw    = MsgGameDraw
	chessStatusForfeit = MsgGameForfeitSuccess
)

// ChessGame represents a Chess game session
type ChessGame struct {
	game          *chess.Game
	player1ID     snowflake.ID
	player2ID     snowflake.ID
	isAI          bool
	aiDifficulty  string
	aiPlayerNum   int
	colorVariant  GameColorVariant
	currentTurn   int
	gameOver      bool
	winner        int
	messageID     snowflake.ID
	channelID     snowflake.ID
	selectedPiece *chess.Square
	lastMove      *chess.Move
	p1Icon        string
	p2Icon        string
}

func (g *ChessGame) GetPieceIcon(p chess.Piece) string {
	wKing, bKing := chessWhiteKing, chessBlackKing
	wQueen, bQueen := chessWhiteQueen, chessBlackQueen
	wRook, bRook := chessWhiteRook, chessBlackRook
	wBishop, bBishop := chessWhiteBishop, chessBlackBishop
	wKnight, bKnight := chessWhiteKnight, chessBlackKnight
	wPawn, bPawn := chessWhitePawn, chessBlackPawn

	if g.colorVariant == VariantInverted {
		wKing, bKing = chessBlackKing, chessWhiteKing
		wQueen, bQueen = chessBlackQueen, chessWhiteQueen
		wRook, bRook = chessBlackRook, chessWhiteRook
		wBishop, bBishop = chessBlackBishop, chessWhiteBishop
		wKnight, bKnight = chessBlackKnight, chessWhiteKnight
		wPawn, bPawn = chessBlackPawn, chessWhitePawn
	}

	switch p.Type() {
	case chess.Pawn:
		if p.Color() == chess.White {
			return wPawn
		}
		return bPawn
	case chess.Knight:
		if p.Color() == chess.White {
			return wKnight
		}
		return bKnight
	case chess.Bishop:
		if p.Color() == chess.White {
			return wBishop
		}
		return bBishop
	case chess.Rook:
		if p.Color() == chess.White {
			return wRook
		}
		return bRook
	case chess.Queen:
		if p.Color() == chess.White {
			return wQueen
		}
		return bQueen
	case chess.King:
		if p.Color() == chess.White {
			return wKing
		}
		return bKing
	}
	return ""
}

func NewChessGame(p1, p2 snowflake.ID, isAI bool, aiPlayerNum int, difficulty string) *ChessGame {
	variant := VariantStandard
	if !isAI && rand.Intn(2) == 1 {
		variant = VariantInverted
	}
	game := &ChessGame{
		game:         chess.NewGame(),
		player1ID:    p1,
		player2ID:    p2,
		isAI:         isAI,
		aiDifficulty: difficulty,
		aiPlayerNum:  aiPlayerNum,
		colorVariant: variant,
		currentTurn:  1,
	}
	game.p1Icon = game.GetPieceIcon(chess.Piece(chess.WhiteKing))
	game.p2Icon = game.GetPieceIcon(chess.Piece(chess.BlackKing))
	return game
}

// ===========================
// Chess Interaction Handlers
// ===========================

func HandlePlayChess(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	defer func() {
		if r := recover(); r != nil {
			hooks.LogError("Panic in HandlePlayChess: %v", r)
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
	gameID := fmt.Sprintf("chess_%d_%d", cid, time.Now().UnixNano())

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

	var whitePlayer, blackPlayer snowflake.ID
	if rand.Intn(2) == 0 {
		whitePlayer = p1
		blackPlayer = p2
	} else {
		whitePlayer = p2
		blackPlayer = p1
	}

	aiPlayerNum := 0
	if isAI {
		if whitePlayer == appID || (opponentID != nil && whitePlayer == *opponentID && isAI) {
			aiPlayerNum = 1
		} else {
			aiPlayerNum = 2
		}
	}

	game := NewChessGame(whitePlayer, blackPlayer, isAI, aiPlayerNum, difficulty)

	activeChessGamesMu.Lock()
	activeChessGames[gameID] = game
	activeChessGamesMu.Unlock()

	msg := ChessBuildMessage(game, gameID, "")
	if err := hooks.RespondInteractionContainerV2(*event.Client(), event, msg, false); err != nil {
		activeChessGamesMu.Lock()
		delete(activeChessGames, gameID)
		activeChessGamesMu.Unlock()

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
	}

	if game.isAI && game.aiPlayerNum == 1 {
		time.AfterFunc(1*time.Second, func() {
			ChessMakeAIMove(*event.Client(), game, gameID)
		})
	}
}

func HandleChessInteraction(event *events.ComponentInteractionCreate) {
	defer func() {
		if r := recover(); r != nil {
			hooks.LogError("Panic in HandleChessInteraction: %v", r)
			fmt.Printf("%s\n", debug.Stack())
		}
	}()

	parts := strings.Split(event.Data.CustomID(), ":")
	if len(parts) < 3 {
		return
	}

	gameID := parts[1]
	action := parts[2]
	statusMsg := ""

	activeChessGamesMu.Lock()
	game, exists := activeChessGames[gameID]
	if !exists {
		activeChessGamesMu.Unlock()
		event.CreateMessage(discord.NewMessageCreate().WithContent(MsgGameNotFound).WithEphemeral(true))
		return
	}

	userID := event.User().ID
	if userID != game.player1ID && userID != game.player2ID {
		activeChessGamesMu.Unlock()
		event.CreateMessage(discord.NewMessageCreate().WithContent("You are not part of this game.").WithEphemeral(true))
		return
	}

	isWhite := userID == game.player1ID
	isTurn := (game.currentTurn == 1 && isWhite) || (game.currentTurn == 2 && !isWhite)

	if !isTurn && action != "forfeit" {
		activeChessGamesMu.Unlock()
		event.CreateMessage(discord.NewMessageCreate().WithContent(MsgGameNotTurn).WithEphemeral(true))
		return
	}

	switch action {
	case "select_piece":
		values := event.StringSelectMenuInteractionData().Values
		if len(values) > 0 {
			sqVal, _ := strconv.Atoi(values[0])
			sq := chess.Square(sqVal)
			game.selectedPiece = &sq
		}
	case "move_to":
		values := event.StringSelectMenuInteractionData().Values
		if len(values) > 0 {
			toSqVal, _ := strconv.Atoi(values[0])
			toSq := chess.Square(toSqVal)

			if game.selectedPiece != nil {
				validMoves := game.game.ValidMoves()
				var selectedMove *chess.Move
				for _, m := range validMoves {
					if m.S1() == *game.selectedPiece && m.S2() == toSq {
						if m.Promo() != chess.NoPieceType && m.Promo() != chess.Queen {
							continue
						}
						selectedMove = &m
						break
					}
				}

				if selectedMove != nil {
					game.game.PushNotationMove(chess.UCINotation{}.Encode(game.game.Position(), selectedMove), chess.UCINotation{}, nil)

					icon := game.GetPieceIcon(game.game.Position().Board().Piece(selectedMove.S2()))
					if isWhite {
						game.p1Icon = icon
					} else {
						game.p2Icon = icon
					}

					game.lastMove = selectedMove
					game.selectedPiece = nil
					game.currentTurn = 3 - game.currentTurn

					outcome := game.game.Outcome()
					if outcome != chess.NoOutcome {
						game.gameOver = true
						switch outcome {
						case chess.WhiteWon:
							game.winner = 1
						case chess.BlackWon:
							game.winner = 2
						default:
							game.winner = 0
						}

						userActiveGameMu.Lock()
						delete(userActiveGame, game.player1ID)
						delete(userActiveGame, game.player2ID)
						userActiveGameMu.Unlock()
					}
				}
			}
		}
	case "forfeit":
		game.gameOver = true
		winnerID := game.player1ID
		loserID := game.player2ID
		if isWhite {
			game.winner = 2
			winnerID = game.player2ID
			loserID = game.player1ID
		} else {
			game.winner = 1
		}
		statusMsg = fmt.Sprintf(chessStatusForfeit, loserID, winnerID)

		userActiveGameMu.Lock()
		delete(userActiveGame, game.player1ID)
		delete(userActiveGame, game.player2ID)
		userActiveGameMu.Unlock()
	}

	msg := ChessBuildMessage(game, gameID, statusMsg)
	activeChessGamesMu.Unlock()

	_ = hooks.UpdateInteractionContainerV2(*event.Client(), event, msg)

	if !game.gameOver && game.isAI && game.currentTurn == game.aiPlayerNum {
		time.AfterFunc(1*time.Second, func() {
			ChessMakeAIMove(*event.Client(), game, gameID)
		})
	}
}

// ===========================
// Chess Rendering & Helpers
// ===========================

func ChessBuildMessage(game *ChessGame, gameID string, statusMsg string) any {
	var sb strings.Builder

	corner := BoardCorner
	cols := BoardColumnEmojis
	rows := BoardRowEmojis

	reverse := false
	if game.isAI {
		if game.aiPlayerNum == 1 {
			reverse = true
		}
	} else if game.currentTurn == 2 {
		reverse = true
	}

	sb.WriteString(corner)
	if !reverse {
		for _, c := range cols {
			sb.WriteString(c)
		}
	} else {
		for i := len(cols) - 1; i >= 0; i-- {
			sb.WriteString(cols[i])
		}
	}
	sb.WriteString(corner)
	sb.WriteString("\n")

	rStart, rEnd, rStep := 7, -1, -1
	cStart, cEnd, cStep := 0, 8, 1

	if reverse {
		rStart, rEnd, rStep = 0, 8, 1
		cStart, cEnd, cStep = 7, -1, -1
	}

	board := game.game.Position().Board()
	validMoves := game.game.ValidMoves()
	validTargets := make(map[chess.Square]bool)

	if game.selectedPiece != nil {
		for _, m := range validMoves {
			if m.S1() == *game.selectedPiece {
				validTargets[m.S2()] = true
			}
		}
	}

	for r := rStart; r != rEnd; r += rStep {
		sb.WriteString(rows[r])
		for c := cStart; c != cEnd; c += cStep {
			sq := chess.Square(r*8 + c)
			p := board.Piece(sq)

			bg := chessWhiteSquare
			if (r+c)%2 == 0 {
				bg = chessBlackSquare
			}

			isSelected := game.selectedPiece != nil && *game.selectedPiece == sq

			isLastMoveSrc := game.lastMove != nil && game.lastMove.S1() == sq
			isLastMoveDst := game.lastMove != nil && game.lastMove.S2() == sq
			isTarget := validTargets[sq]

			display := bg

			if isSelected {
				display = chessSelected
			} else if isTarget || isLastMoveSrc || isLastMoveDst {
				if bg == chessWhiteSquare {
					display = chessTargetWhite
				} else {
					display = chessTargetBlack
				}
			}

			pieceIcon := game.GetPieceIcon(p)
			if pieceIcon != "" {
				display = pieceIcon
			}

			sb.WriteString(display)
		}
		sb.WriteString(rows[r])
		sb.WriteString("\n")
	}
	sb.WriteString(corner)

	wCount, bCount := 0, 0
	wMap := board.SquareMap()
	for _, p := range wMap {
		if p.Color() == chess.White {
			wCount++
		} else {
			bCount++
		}
	}

	p1Disp := fmt.Sprintf("%s <@%d>: **%d**", game.p1Icon, game.player1ID, wCount)
	p2Disp := fmt.Sprintf("%s <@%d>: **%d**", game.p2Icon, game.player2ID, bCount)

	scoreStr := fmt.Sprintf("\n-# %s | %s", p1Disp, p2Disp)
	sb.WriteString(scoreStr)

	var statusSB strings.Builder
	if game.gameOver {
		if statusMsg != "" {
			statusSB.WriteString(statusMsg)
		} else {
			outcome := game.game.Outcome()
			winnerID := game.player1ID
			loserID := game.player2ID
			if game.winner == 2 {
				winnerID = game.player2ID
				loserID = game.player1ID
			}

			if outcome == chess.WhiteWon || outcome == chess.BlackWon {
				statusSB.WriteString(fmt.Sprintf(chessStatusWin, loserID, winnerID))
			} else {
				statusSB.WriteString(fmt.Sprintf(chessStatusDraw, game.player1ID, game.player2ID))
			}
		}
	} else {
		currentPlayer := game.player1ID
		statusIcon := game.p1Icon
		if game.currentTurn == 2 {
			currentPlayer = game.player2ID
			statusIcon = game.p2Icon
		}
		statusSB.WriteString(fmt.Sprintf(chessStatusTurn, currentPlayer, statusIcon))

		if statusMsg != "" {
			statusSB.WriteString("\n")
			statusSB.WriteString(statusMsg)
		}
		if game.game.Position().Status() == chess.Checkmate {
			statusSB.WriteString(" (CHECKMATE)")
		} else if game.lastMove != nil && game.lastMove.HasTag(chess.Check) {
			statusSB.WriteString(" (CHECK)")
		}
	}

	var components []interface{}
	components = append(components, hooks.NewTextDisplay(sb.String()))
	components = append(components, hooks.NewTextDisplay(statusSB.String()))

	if !game.gameOver {
		seen := make(map[chess.Square]bool)
		var pieceOpts []discord.StringSelectMenuOption

		for _, m := range validMoves {
			s1 := m.S1()
			if !seen[s1] {
				seen[s1] = true

				label := fmt.Sprintf("%c%d", 'A'+s1.File(), int(s1.Rank())+1)
				val := strconv.Itoa(int(s1))

				p := board.Piece(s1)
				icon := game.GetPieceIcon(p)

				pieceOpts = append(pieceOpts, discord.NewStringSelectMenuOption(icon+" "+label, val))
			}
		}
		sort.Slice(pieceOpts, func(i, j int) bool { return pieceOpts[i].Label < pieceOpts[j].Label })

		if game.selectedPiece != nil {
			var destOpts []discord.StringSelectMenuOption
			for _, m := range validMoves {
				if m.S1() == *game.selectedPiece {
					s2 := m.S2()
					label := fmt.Sprintf("To %c%d", 'A'+s2.File(), int(s2.Rank())+1)
					val := strconv.Itoa(int(s2))

					if m.Promo() != chess.NoPieceType && m.Promo() != chess.Queen {
						continue
					}
					if m.Promo() == chess.Queen {
						label += " (Queen)"
					}

					destOpts = append(destOpts, discord.NewStringSelectMenuOption(label, val))
				}
			}

			if len(destOpts) > 0 {
				if len(destOpts) > 25 {
					destOpts = destOpts[:25]
				}
				menu := discord.NewStringSelectMenu(fmt.Sprintf(CIDChessMoveTo, gameID), LabelSelectDest, destOpts...)
				components = append(components, discord.NewActionRow(menu))

				cancelBtn := discord.NewButton(discord.ButtonStyleSecondary, "Cancel Selection", fmt.Sprintf(CIDChessCancelSelect, gameID), "", 0)
				components = append(components, discord.NewActionRow(cancelBtn))
			}
		}

		if len(pieceOpts) > 0 {
			if len(pieceOpts) > 25 {
				pieceOpts = pieceOpts[:25]
			}
			placeholder := LabelSelectPiece
			if game.selectedPiece != nil {
				s := *game.selectedPiece
				placeholder = fmt.Sprintf("Selected: %c%d", 'A'+s.File(), int(s.Rank())+1)
			}
			menu := discord.NewStringSelectMenu(fmt.Sprintf(CIDChessSelectPiece, gameID), placeholder, pieceOpts...)
			components = append(components, discord.NewActionRow(menu))
		}

		row := discord.NewActionRow(
			discord.NewButton(discord.ButtonStyleDanger, LabelForfeit, fmt.Sprintf(CIDChessForfeit, gameID), "", 0),
		)
		components = append(components, row)
	}

	return hooks.NewV2Container(components...)
}
func ChessMakeAIMove(client bot.Client, game *ChessGame, gameID string) {
	activeChessGamesMu.Lock()
	if game.gameOver || game.currentTurn != game.aiPlayerNum {
		activeChessGamesMu.Unlock()
		return
	}

	difficulty := game.aiDifficulty
	fen := game.game.Position().String()
	moves := game.game.ValidMoves()
	activeChessGamesMu.Unlock()

	if len(moves) == 0 {
		return
	}

	var selectedMove *chess.Move
	isHardFallback := false

	if difficulty == "hard" {
		reqBody, _ := json.Marshal(map[string]string{"fen": fen})
		resp, err := http.Post("https://chess-api.com/v1", "application/json", bytes.NewBuffer(reqBody))
		if err == nil {
			defer resp.Body.Close()
			var apiResp struct {
				Move string `json:"move"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&apiResp); err == nil {
				activeChessGamesMu.Lock()
				move, err := chess.UCINotation{}.Decode(game.game.Position(), apiResp.Move)
				if err == nil {
					selectedMove = move
				}
				activeChessGamesMu.Unlock()
			}
		}

		if selectedMove == nil {
			isHardFallback = true
			difficulty = "normal"
		}
	}

	activeChessGamesMu.Lock()
	defer activeChessGamesMu.Unlock()

	if game.gameOver || game.currentTurn != game.aiPlayerNum {
		return
	}

	if selectedMove == nil {
		var captureMoves []*chess.Move
		for _, m := range moves {
			mCopy := m
			if m.HasTag(chess.Capture) {
				captureMoves = append(captureMoves, &mCopy)
			}
		}

		if len(captureMoves) > 0 && difficulty != "easy" {
			selectedMove = captureMoves[rand.Intn(len(captureMoves))]
		} else {
			mCopy := moves[rand.Intn(len(moves))]
			selectedMove = &mCopy
		}
	}

	game.game.PushNotationMove(chess.UCINotation{}.Encode(game.game.Position(), selectedMove), chess.UCINotation{}, nil)
	game.lastMove = selectedMove

	icon := game.GetPieceIcon(game.game.Position().Board().Piece(selectedMove.S2()))
	if game.aiPlayerNum == 1 {
		game.p1Icon = icon
	} else {
		game.p2Icon = icon
	}

	game.currentTurn = 3 - game.currentTurn

	outcome := game.game.Outcome()
	if outcome != chess.NoOutcome {
		game.gameOver = true
		switch outcome {
		case chess.WhiteWon:
			game.winner = 1
		case chess.BlackWon:
			game.winner = 2
		default:
			game.winner = 0
		}

		userActiveGameMu.Lock()
		delete(userActiveGame, game.player1ID)
		delete(userActiveGame, game.player2ID)
		userActiveGameMu.Unlock()
	}

	statusMsg := ""
	if isHardFallback {
		statusMsg = MsgGameAIHardFallback
	}

	msg := ChessBuildMessage(game, gameID, statusMsg)
	_, _ = hooks.EditContainerV2(client, game.channelID, game.messageID, msg, nil, nil)
}
