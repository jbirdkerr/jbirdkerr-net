package main

import (
	"image"
	"image/color"
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/jbirdkerr/jbirdkerr-net/apps/digdog/internal/board"
)

const (
	cellW = 44
	cellH = 44
	cardW = 36
	cardH = 36
)

// boardOrigin returns the top-left pixel of the board grid.
func boardOrigin(b *board.Board) (int, int) {
	// Nudged up a little to leave room for the dog at the bottom.
	return (screenW - b.Cols()*cellW) / 2, (screenH-b.Rows()*cellH)/2 - 8
}

// cellAt returns the card index under the point (x, y) in screen coordinates, or -1.
func cellAt(b *board.Board, x, y int) int {
	ox, oy := boardOrigin(b)
	cx, cy := x-ox, y-oy
	if cx < 0 || cy < 0 {
		return -1
	}
	col, row := cx/cellW, cy/cellH
	if col >= b.Cols() || row >= b.Rows() {
		return -1
	}
	return row*b.Cols() + col
}

// cellCenter returns the pixel centre of card i.
func cellCenter(b *board.Board, i int) (float64, float64) {
	ox, oy := boardOrigin(b)
	return float64(ox + (i%b.Cols())*cellW + cellW/2),
		float64(oy + (i/b.Cols())*cellH + cellH/2)
}

// --- Placeholder drawing (swap for real sprites from cards.png later) ---

var faceColors = []color.RGBA{
	{0xe5, 0x48, 0x4d, 0xff},
	{0x3b, 0x82, 0xf6, 0xff},
	{0x34, 0xd3, 0x99, 0xff},
	{0xfb, 0xbf, 0x24, 0xff},
	{0xa8, 0x55, 0xf7, 0xff},
	{0x0e, 0xa5, 0xe9, 0xff},
}

func fillRect(dst *ebiten.Image, x, y, w, h int, c color.Color) {
	dst.SubImage(image.Rect(x, y, x+w, y+h)).(*ebiten.Image).Fill(c)
}

// drawFaceShape renders a high-contrast iconic symbol in the center of the card.
func drawFaceShape(screen *ebiten.Image, cx, cy int, face int) {
	col := color.RGBA{0x0a, 0x0e, 0x27, 0xff}

	switch face % 6 {
	case 0: // Diamond
		for dy := -5; dy <= 5; dy++ {
			span := 5 - int(math.Abs(float64(dy)))
			fillRect(screen, cx-span, cy+dy, span*2+1, 1, col)
		}
	case 1: // Cross / Plus
		fillRect(screen, cx-2, cy-6, 5, 13, col)
		fillRect(screen, cx-6, cy-2, 13, 5, col)
	case 2: // Box frame
		fillRect(screen, cx-6, cy-6, 13, 13, col)
		fillRect(screen, cx-3, cy-3, 7, 7, color.RGBA{0xff, 0xff, 0xff, 0xee})
	case 3: // Upward Triangle
		for dy := 0; dy <= 10; dy++ {
			span := dy / 2
			fillRect(screen, cx-span, cy-5+dy, span*2+1, 1, col)
		}
	case 4: // Heart
		fillRect(screen, cx-5, cy-5, 4, 4, col)
		fillRect(screen, cx+1, cy-5, 4, 4, col)
		fillRect(screen, cx-6, cy-2, 12, 4, col)
		fillRect(screen, cx-4, cy+2, 8, 2, col)
		fillRect(screen, cx-2, cy+4, 4, 2, col)
	case 5: // Circle
		for dy := -5; dy <= 5; dy++ {
			r := 5.0
			dx := int(math.Sqrt(math.Max(0, r*r-float64(dy*dy))))
			fillRect(screen, cx-dx, cy+dy, dx*2+1, 1, col)
		}
	}
}

func drawFocusRing(screen *ebiten.Image, x, y, w, h int) {
	// Crisp cyan corner brackets around the selected mound
	bracketCol := color.RGBA{0x38, 0xbd, 0xf8, 0xff}
	bLen := 6
	bThick := 2

	// Top-left
	fillRect(screen, x, y, bLen, bThick, bracketCol)
	fillRect(screen, x, y, bThick, bLen, bracketCol)
	// Top-right
	fillRect(screen, x+w-bLen, y, bLen, bThick, bracketCol)
	fillRect(screen, x+w-bThick, y, bThick, bLen, bracketCol)
	// Bottom-left
	fillRect(screen, x, y+h-bThick, bLen, bThick, bracketCol)
	fillRect(screen, x, y+h-bLen, bThick, bLen, bracketCol)
	// Bottom-right
	fillRect(screen, x+w-bLen, y+h-bThick, bLen, bThick, bracketCol)
	fillRect(screen, x+w-bThick, y+h-bLen, bThick, bLen, bracketCol)
}

func drawBoard(screen *ebiten.Image, b *board.Board, focusIndex int, showFocus bool, sniffTarget int, sniffActive bool) {
	ox, oy := boardOrigin(b)
	for i := 0; i < b.Len(); i++ {
		c := b.Card(i)
		x := ox + (i%b.Cols())*cellW + (cellW-cardW)/2
		y := oy + (i/b.Cols())*cellH + (cellH-cardH)/2

		state := c.State
		isPeeked := sniffActive && i == sniffTarget && state == board.Buried

		if isPeeked {
			state = board.Revealed
		}

		switch state {
		case board.Buried:
			fillRect(screen, x, y, cardW, cardH, color.RGBA{0x5a, 0x3d, 0x24, 0xff})
			fillRect(screen, x+4, y+4, cardW-8, 8, color.RGBA{0x7a, 0x55, 0x33, 0xff})
			fillRect(screen, x+8, y+20, 4, 4, color.RGBA{0x3f, 0x2a, 0x18, 0xff})
			fillRect(screen, x+22, y+26, 4, 4, color.RGBA{0x3f, 0x2a, 0x18, 0xff})
		case board.Revealed:
			// Card base & border
			fillRect(screen, x, y, cardW, cardH, color.RGBA{0xf3, 0xf4, 0xf6, 0xff})
			fillRect(screen, x+2, y+2, cardW-4, cardH-4, faceColors[c.Face%len(faceColors)])

			// Draw unique center shape
			drawFaceShape(screen, x+cardW/2, y+cardH/2-2, c.Face)

			// Pips along bottom so pairs are doubly distinguishable without colour vision
			for k := 0; k <= c.Face; k++ {
				fillRect(screen, x+5+k*5, y+cardH-6, 3, 3, color.RGBA{0x0a, 0x0e, 0x27, 0xff})
			}

			if isPeeked {
				// Golden peek aura
				fillRect(screen, x-1, y-1, cardW+2, 1, color.RGBA{0xfd, 0xe0, 0x47, 0xff})
				fillRect(screen, x-1, y+cardH, cardW+2, 1, color.RGBA{0xfd, 0xe0, 0x47, 0xff})
				fillRect(screen, x-1, y-1, 1, cardH+2, color.RGBA{0xfd, 0xe0, 0x47, 0xff})
				fillRect(screen, x+cardW, y-1, 1, cardH+2, color.RGBA{0xfd, 0xe0, 0x47, 0xff})
			}
		case board.Matched:
			fillRect(screen, x+6, y+6, cardW-12, cardH-12, color.RGBA{0x12, 0x0c, 0x08, 0xff})
		}

		if showFocus && i == focusIndex {
			drawFocusRing(screen, x-2, y-2, cardW+4, cardH+4)
		}
	}
}
