package main

import "encoding/json"

const (
	BoardSize = 10
	White     = "w"
	Black     = "b"
)

// ── Data types ────────────────────────────────────────────────────────────────

type Piece struct {
	Color string `json:"color"`
	Kind  string `json:"kind"` // K Q R B N P
}

type Move struct {
	Start     [2]int `json:"start"`
	End       [2]int `json:"end"`
	Promotion string `json:"promotion,omitempty"`
	IsCastle  bool   `json:"is_castle,omitempty"`
}

type castleRights struct {
	Kingside  bool
	Queenside bool
}

type GameState struct {
	Board        [BoardSize][BoardSize]*Piece
	Turn         string
	WhiteKingPos [2]int
	BlackKingPos [2]int
	Castle       map[string]*castleRights
}

// ── JSON serialisation (for SQLite storage) ───────────────────────────────────

type serialisedCastle struct {
	Kingside  bool `json:"kingside"`
	Queenside bool `json:"queenside"`
}

type serialisedState struct {
	Board        [BoardSize][BoardSize]*Piece    `json:"board"`
	Turn         string                          `json:"turn"`
	WhiteKingPos [2]int                          `json:"wkp"`
	BlackKingPos [2]int                          `json:"bkp"`
	Castle       map[string]serialisedCastle     `json:"castle"`
}

func (g *GameState) MarshalJSON() ([]byte, error) {
	s := serialisedState{
		Board:        g.Board,
		Turn:         g.Turn,
		WhiteKingPos: g.WhiteKingPos,
		BlackKingPos: g.BlackKingPos,
		Castle: map[string]serialisedCastle{
			White: {g.Castle[White].Kingside, g.Castle[White].Queenside},
			Black: {g.Castle[Black].Kingside, g.Castle[Black].Queenside},
		},
	}
	return json.Marshal(s)
}

func (g *GameState) UnmarshalJSON(data []byte) error {
	var s serialisedState
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	g.Board = s.Board
	g.Turn = s.Turn
	g.WhiteKingPos = s.WhiteKingPos
	g.BlackKingPos = s.BlackKingPos
	g.Castle = map[string]*castleRights{
		White: {s.Castle[White].Kingside, s.Castle[White].Queenside},
		Black: {s.Castle[Black].Kingside, s.Castle[Black].Queenside},
	}
	return nil
}

// ── Constructor ───────────────────────────────────────────────────────────────

func NewGame() *GameState {
	g := &GameState{
		Turn:         White,
		WhiteKingPos: [2]int{0, 4},
		BlackKingPos: [2]int{9, 4},
		Castle: map[string]*castleRights{
			White: {true, true},
			Black: {true, true},
		},
	}
	backRank := []string{"R", "N", "N", "B", "K", "Q", "B", "N", "N", "R"}
	for col, kind := range backRank {
		g.Board[0][col] = &Piece{White, kind}
		g.Board[9][col] = &Piece{Black, kind}
	}
	for col := range BoardSize {
		g.Board[1][col] = &Piece{White, "P"}
		g.Board[8][col] = &Piece{Black, "P"}
	}
	return g
}

// ── Clone ─────────────────────────────────────────────────────────────────────

func (g *GameState) clone() *GameState {
	c := &GameState{
		Board:        g.Board, // copies the array; *Piece values are immutable
		Turn:         g.Turn,
		WhiteKingPos: g.WhiteKingPos,
		BlackKingPos: g.BlackKingPos,
		Castle: map[string]*castleRights{
			White: {g.Castle[White].Kingside, g.Castle[White].Queenside},
			Black: {g.Castle[Black].Kingside, g.Castle[Black].Queenside},
		},
	}
	return c
}

// ── Public interface ──────────────────────────────────────────────────────────

func (g *GameState) LegalMovesFrom(r, c int) []Move {
	p := g.Board[r][c]
	if p == nil || p.Color != g.Turn {
		return nil
	}
	var out []Move
	for _, m := range g.pieceMoves(r, c, true) {
		if g.isLegal(m) {
			out = append(out, m)
		}
	}
	return out
}

func (g *GameState) AllLegalMoves() []Move {
	var out []Move
	for r := range BoardSize {
		for c := range BoardSize {
			p := g.Board[r][c]
			if p == nil || p.Color != g.Turn {
				continue
			}
			for _, m := range g.pieceMoves(r, c, true) {
				if g.isLegal(m) {
					out = append(out, m)
				}
			}
		}
	}
	return out
}

// MakeMove validates, applies the move, flips the turn, returns success.
func (g *GameState) MakeMove(m Move) bool {
	if !g.isLegal(m) {
		return false
	}
	g.applyMove(m)
	if g.Turn == White {
		g.Turn = Black
	} else {
		g.Turn = White
	}
	return true
}

func (g *GameState) IsInCheck(color string) bool {
	kp := g.WhiteKingPos
	if color == Black {
		kp = g.BlackKingPos
	}
	enemy := Black
	if color == Black {
		enemy = White
	}
	return g.squareAttackedBy(kp[0], kp[1], enemy)
}

func (g *GameState) IsCheckmate() bool {
	return g.IsInCheck(g.Turn) && len(g.AllLegalMoves()) == 0
}

func (g *GameState) IsStalemate() bool {
	return !g.IsInCheck(g.Turn) && len(g.AllLegalMoves()) == 0
}

// ── Internal helpers ──────────────────────────────────────────────────────────

func inBounds(r, c int) bool {
	return r >= 0 && r < BoardSize && c >= 0 && c < BoardSize
}

func enemy(color string) string {
	if color == White {
		return Black
	}
	return White
}

func homeRow(color string) int {
	if color == White {
		return 0
	}
	return BoardSize - 1
}

func (g *GameState) isLegal(m Move) bool {
	c := g.clone()
	c.applyMove(m)
	return !c.IsInCheck(g.Turn)
}

func (g *GameState) applyMove(m Move) {
	sr, sc := m.Start[0], m.Start[1]
	er, ec := m.End[0], m.End[1]
	piece := g.Board[sr][sc]
	if piece == nil {
		return
	}
	captured := g.Board[er][ec]
	g.Board[sr][sc] = nil

	if m.IsCastle && piece.Kind == "K" {
		g.Board[er][ec] = piece
		if piece.Color == White {
			g.WhiteKingPos = [2]int{er, ec}
		} else {
			g.BlackKingPos = [2]int{er, ec}
		}
		var rs, re [2]int
		if ec > sc { // kingside
			rs = [2]int{sr, BoardSize - 1}
			re = [2]int{sr, ec - 1}
		} else { // queenside
			rs = [2]int{sr, 0}
			re = [2]int{sr, ec + 1}
		}
		rook := g.Board[rs[0]][rs[1]]
		g.Board[rs[0]][rs[1]] = nil
		g.Board[re[0]][re[1]] = rook
	} else {
		if piece.Kind == "P" && m.Promotion != "" {
			piece = &Piece{piece.Color, m.Promotion}
		}
		g.Board[er][ec] = piece
		if piece.Kind == "K" {
			if piece.Color == White {
				g.WhiteKingPos = [2]int{er, ec}
			} else {
				g.BlackKingPos = [2]int{er, ec}
			}
		}
	}
	g.updateCastleRights(m, piece, captured)
}

func (g *GameState) updateCastleRights(m Move, moved, captured *Piece) {
	sr, sc := m.Start[0], m.Start[1]
	er, ec := m.End[0], m.End[1]
	color := moved.Color
	opp := enemy(color)
	hr := homeRow(color)
	ehr := homeRow(opp)

	if moved.Kind == "K" {
		g.Castle[color].Kingside = false
		g.Castle[color].Queenside = false
	} else if moved.Kind == "R" && sr == hr {
		if sc == 0 {
			g.Castle[color].Queenside = false
		}
		if sc == BoardSize-1 {
			g.Castle[color].Kingside = false
		}
	}
	if captured != nil && captured.Kind == "R" && er == ehr {
		if ec == 0 {
			g.Castle[opp].Queenside = false
		}
		if ec == BoardSize-1 {
			g.Castle[opp].Kingside = false
		}
	}
}

// ── Move generation ───────────────────────────────────────────────────────────

func (g *GameState) pieceMoves(r, c int, castle bool) []Move {
	p := g.Board[r][c]
	if p == nil {
		return nil
	}
	switch p.Kind {
	case "P":
		return g.pawnMoves(r, c, p.Color)
	case "N":
		return g.knightMoves(r, c, p.Color)
	case "B":
		return g.slidingMoves(r, c, p.Color, [][2]int{{-1, -1}, {-1, 1}, {1, -1}, {1, 1}})
	case "R":
		return g.slidingMoves(r, c, p.Color, [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}})
	case "Q":
		return g.slidingMoves(r, c, p.Color, [][2]int{
			{-1, -1}, {-1, 1}, {1, -1}, {1, 1}, {-1, 0}, {1, 0}, {0, -1}, {0, 1},
		})
	case "K":
		return g.kingMoves(r, c, p.Color, castle)
	}
	return nil
}

func (g *GameState) pawnMoves(r, c int, color string) []Move {
	dir, startR, promoR := 1, 1, BoardSize-1
	if color == Black {
		dir, startR, promoR = -1, BoardSize-2, 0
	}
	pos := [2]int{r, c}
	var moves []Move

	// Forward one
	if nr := r + dir; inBounds(nr, c) && g.Board[nr][c] == nil {
		end := [2]int{nr, c}
		if nr == promoR {
			moves = append(moves, Move{pos, end, "Q", false})
		} else {
			moves = append(moves, Move{pos, end, "", false})
			// Forward two from start row
			if r == startR {
				if nr2 := r + 2*dir; inBounds(nr2, c) && g.Board[nr2][c] == nil {
					moves = append(moves, Move{pos, [2]int{nr2, c}, "", false})
				}
			}
		}
	}
	// Captures
	nr := r + dir
	for _, dc := range []int{-1, 1} {
		nc := c + dc
		if !inBounds(nr, nc) {
			continue
		}
		t := g.Board[nr][nc]
		if t != nil && t.Color != color {
			end := [2]int{nr, nc}
			if nr == promoR {
				moves = append(moves, Move{pos, end, "Q", false})
			} else {
				moves = append(moves, Move{pos, end, "", false})
			}
		}
	}
	return moves
}

func (g *GameState) knightMoves(r, c int, color string) []Move {
	pos := [2]int{r, c}
	var moves []Move
	for _, off := range [][2]int{{-2, -1}, {-2, 1}, {-1, -2}, {-1, 2}, {1, -2}, {1, 2}, {2, -1}, {2, 1}} {
		nr, nc := r+off[0], c+off[1]
		if !inBounds(nr, nc) {
			continue
		}
		if t := g.Board[nr][nc]; t == nil || t.Color != color {
			moves = append(moves, Move{pos, [2]int{nr, nc}, "", false})
		}
	}
	return moves
}

func (g *GameState) slidingMoves(r, c int, color string, dirs [][2]int) []Move {
	pos := [2]int{r, c}
	var moves []Move
	for _, d := range dirs {
		nr, nc := r+d[0], c+d[1]
		for inBounds(nr, nc) {
			t := g.Board[nr][nc]
			if t == nil {
				moves = append(moves, Move{pos, [2]int{nr, nc}, "", false})
			} else {
				if t.Color != color {
					moves = append(moves, Move{pos, [2]int{nr, nc}, "", false})
				}
				break
			}
			nr += d[0]
			nc += d[1]
		}
	}
	return moves
}

func (g *GameState) kingMoves(r, c int, color string, castle bool) []Move {
	pos := [2]int{r, c}
	var moves []Move
	for dr := -1; dr <= 1; dr++ {
		for dc := -1; dc <= 1; dc++ {
			if dr == 0 && dc == 0 {
				continue
			}
			nr, nc := r+dr, c+dc
			if !inBounds(nr, nc) {
				continue
			}
			if t := g.Board[nr][nc]; t == nil || t.Color != color {
				moves = append(moves, Move{pos, [2]int{nr, nc}, "", false})
			}
		}
	}
	if castle {
		moves = append(moves, g.castleMoves(r, c, color)...)
	}
	return moves
}

func (g *GameState) castleMoves(r, c int, color string) []Move {
	if g.IsInCheck(color) || color != g.Turn || c != 4 {
		return nil
	}
	hr := homeRow(color)
	if r != hr {
		return nil
	}
	opp := enemy(color)
	rights := g.Castle[color]
	var moves []Move

	// Kingside: king 4 → 7, rook 9 → 6
	if rights.Kingside {
		rook := g.Board[hr][BoardSize-1]
		if rook != nil && rook.Color == color && rook.Kind == "R" {
			clear := true
			for col := c + 1; col < BoardSize-1; col++ {
				if g.Board[hr][col] != nil {
					clear = false
					break
				}
			}
			safe := true
			for _, col := range []int{5, 6, 7} {
				if g.squareAttackedBy(hr, col, opp) {
					safe = false
					break
				}
			}
			if clear && safe {
				moves = append(moves, Move{[2]int{hr, 4}, [2]int{hr, 7}, "", true})
			}
		}
	}
	// Queenside: king 4 → 1, rook 0 → 2
	if rights.Queenside {
		rook := g.Board[hr][0]
		if rook != nil && rook.Color == color && rook.Kind == "R" {
			clear := true
			for col := 1; col < c; col++ {
				if g.Board[hr][col] != nil {
					clear = false
					break
				}
			}
			safe := true
			for _, col := range []int{3, 2, 1} {
				if g.squareAttackedBy(hr, col, opp) {
					safe = false
					break
				}
			}
			if clear && safe {
				moves = append(moves, Move{[2]int{hr, 4}, [2]int{hr, 1}, "", true})
			}
		}
	}
	return moves
}

func (g *GameState) squareAttackedBy(r, c int, attacker string) bool {
	dir := 1
	if attacker == Black {
		dir = -1
	}
	for ar := range BoardSize {
		for ac := range BoardSize {
			p := g.Board[ar][ac]
			if p == nil || p.Color != attacker {
				continue
			}
			if p.Kind == "P" {
				for _, dc := range []int{-1, 1} {
					if ar+dir == r && ac+dc == c {
						return true
					}
				}
				continue
			}
			for _, m := range g.pieceMoves(ar, ac, false) {
				if m.End[0] == r && m.End[1] == c {
					return true
				}
			}
		}
	}
	return false
}
