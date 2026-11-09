package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	_ "image/png"
	"log"
	"math"
	"math/rand"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/jbirdkerr/jbirdkerr-net/apps/digdog/internal/board"
)

const (
	screenW   = 320
	screenH   = 180
	frameSize = 32
	dogSpeed  = 2.0 // pixels per tick
)

//go:embed assets/dog.png
var dogPNG []byte

// Animation steps through a slice of frames, advancing once every ticksPerFrame updates.
type Animation struct {
	frames        []*ebiten.Image
	ticksPerFrame int
	loop          bool
	tick          int
}

func (a *Animation) Update() { a.tick++ }
func (a *Animation) Reset()  { a.tick = 0 }

func (a *Animation) index() int {
	i := a.tick / a.ticksPerFrame
	if a.loop {
		return i % len(a.frames)
	}
	if i >= len(a.frames) {
		return len(a.frames) - 1
	}
	return i
}

func (a *Animation) Frame() *ebiten.Image { return a.frames[a.index()] }

func (a *Animation) Done() bool {
	return !a.loop && a.tick/a.ticksPerFrame >= len(a.frames)
}

func sliceRow(sheet *ebiten.Image, row, count int) []*ebiten.Image {
	frames := make([]*ebiten.Image, count)
	for i := 0; i < count; i++ {
		r := image.Rect(i*frameSize, row*frameSize, (i+1)*frameSize, (row+1)*frameSize)
		frames[i] = sheet.SubImage(r).(*ebiten.Image)
	}
	return frames
}

type Game struct {
	anims   map[string]*Animation
	current string

	board      *board.Board
	dogX, dogY float64 // centre of the dog sprite
	facing     float64 // 1 = right, -1 = left
	target     int     // card index the dog is heading for or digging at
	walking    bool
	digging    bool

	focusIndex int  // card index selected via keyboard
	showFocus  bool // whether the focus indicator is currently displayed
	ticks      int  // tick counter for animation/pulsing effects

	reducedMotion bool

	audio *AudioSystem

	sniffing    bool
	sniffTimer  int
	sniffTarget int
	sniffsLeft  int

	particles Particles
}

func newGame() (*Game, error) {
	img, _, err := image.Decode(bytes.NewReader(dogPNG))
	if err != nil {
		return nil, err
	}
	sheet := ebiten.NewImageFromImage(img)

	g := &Game{
		anims: map[string]*Animation{
			"idle":  {frames: sliceRow(sheet, 0, 4), ticksPerFrame: 15, loop: true},
			"dig":   {frames: sliceRow(sheet, 1, 4), ticksPerFrame: 6},
			"sniff": {frames: sliceRow(sheet, 2, 3), ticksPerFrame: 10},
			"happy": {frames: sliceRow(sheet, 3, 3), ticksPerFrame: 8},
			"sad":   {frames: sliceRow(sheet, 4, 2), ticksPerFrame: 12},
		},
		reducedMotion: isReducedMotionPreferred(),
		audio:         newAudioSystem(),
	}
	g.reset()
	return g, nil
}

func (g *Game) reset() {
	g.board = board.NewBoard(4, 3)
	g.dogX, g.dogY = screenW/2, screenH-18
	g.facing = 1
	g.walking, g.digging = false, false
	g.particles = g.particles[:0]
	g.focusIndex = 0
	g.sniffing = false
	g.sniffTimer = 0
	g.sniffTarget = -1
	g.sniffsLeft = 2
	g.play("idle")
}

func (g *Game) triggerSniff() {
	if g.walking || g.digging || g.sniffing || g.sniffsLeft <= 0 || g.board.Won {
		return
	}
	var buried []int
	for i := 0; i < g.board.Len(); i++ {
		if g.board.Card(i).State == board.Buried {
			buried = append(buried, i)
		}
	}
	if len(buried) == 0 {
		return
	}
	g.sniffsLeft--
	g.sniffing = true
	g.sniffTarget = buried[rand.Intn(len(buried))]
	g.play("sniff")
}

func (g *Game) play(name string) {
	g.current = name
	g.anims[name].Reset()
}

// pointerPos returns a fresh click or tap in logical screen coordinates.
func pointerPos() (int, int, bool) {
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		x, y := ebiten.CursorPosition()
		return x, y, true
	}
	for _, id := range inpututil.AppendJustPressedTouchIDs(nil) {
		x, y := ebiten.TouchPosition(id)
		return x, y, true
	}
	return 0, 0, false
}

func (g *Game) handleInput() {
	cols := g.board.Cols()
	rows := g.board.Rows()
	col := g.focusIndex % cols
	row := g.focusIndex / cols

	navigated := false
	if inpututil.IsKeyJustPressed(ebiten.KeyArrowLeft) || inpututil.IsKeyJustPressed(ebiten.KeyLeft) || inpututil.IsKeyJustPressed(ebiten.KeyA) {
		if col > 0 {
			col--
			navigated = true
		}
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyArrowRight) || inpututil.IsKeyJustPressed(ebiten.KeyRight) || inpututil.IsKeyJustPressed(ebiten.KeyD) {
		if col < cols-1 {
			col++
			navigated = true
		}
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyArrowUp) || inpututil.IsKeyJustPressed(ebiten.KeyUp) || inpututil.IsKeyJustPressed(ebiten.KeyW) {
		if row > 0 {
			row--
			navigated = true
		}
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyArrowDown) || inpututil.IsKeyJustPressed(ebiten.KeyDown) || inpututil.IsKeyJustPressed(ebiten.KeyS) {
		if row < rows-1 {
			row++
			navigated = true
		}
	}

	if navigated {
		g.focusIndex = row*cols + col
		g.showFocus = true
	}

	// Space or Enter acts on the focused card
	keyboardDig := inpututil.IsKeyJustPressed(ebiten.KeySpace) ||
		inpututil.IsKeyJustPressed(ebiten.KeyEnter) ||
		inpututil.IsKeyJustPressed(ebiten.KeyNumpadEnter)

	if inpututil.IsKeyJustPressed(ebiten.KeyS) {
		g.triggerSniff()
	}

	if keyboardDig {
		g.showFocus = true
		if !g.walking && !g.digging && !g.sniffing {
			if g.board.Pick(g.focusIndex) {
				g.target = g.focusIndex
				g.walking = true
			}
		}
	}

	// Mouse or touch input
	if x, y, ok := pointerPos(); ok {
		// Click on top-right mute toggle
		if x >= screenW-64 && y <= 16 {
			g.audio.ToggleMute()
			return
		}
		// Click on sniff power-up button
		if x >= screenW-160 && x < screenW-64 && y <= 16 {
			g.triggerSniff()
			return
		}
		if i := cellAt(g.board, x, y); i >= 0 {
			g.focusIndex = i
			if !g.walking && !g.digging && !g.sniffing {
				if g.board.Pick(i) {
					g.target = i
					g.walking = true
				}
			}
		}
	}
}

func (g *Game) Update() error {
	g.ticks++
	if inpututil.IsKeyJustPressed(ebiten.KeyR) {
		g.reset()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyM) {
		g.audio.ToggleMute()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyP) {
		g.reducedMotion = !g.reducedMotion
	}
	if g.board.Won {
		if _, _, ok := pointerPos(); ok {
			g.reset()
			return nil
		}
		if inpututil.IsKeyJustPressed(ebiten.KeySpace) || inpututil.IsKeyJustPressed(ebiten.KeyEnter) || inpututil.IsKeyJustPressed(ebiten.KeyNumpadEnter) {
			g.reset()
			return nil
		}
	}
	g.board.Update()
	g.particles.Update()

	if g.sniffTimer > 0 {
		g.sniffTimer--
		if g.sniffTimer == 0 {
			g.sniffTarget = -1
		}
	}

	// Spawn dirt particles while the dog is digging (unless reduced motion is active).
	if g.digging && !g.reducedMotion {
		px, py := cellCenter(g.board, g.target)
		py += 6 // just below the mound
		g.particles.Spawn(px, py)
		g.particles.Spawn(px, py)
	}

	g.handleInput()

	if g.walking {
		g.walk()
	}

	anim := g.anims[g.current]
	anim.Update()
	if anim.Done() {
		if g.digging {
			g.digging = false
			g.afterReveal(g.board.Reveal(g.target))
		} else if g.sniffing {
			g.sniffing = false
			g.sniffTimer = 45
			g.audio.PlayReveal()
			g.play("idle")
		} else {
			g.play("idle")
		}
	}
	return nil
}

// targetPos returns the destination position and facing for the dog when digging mound i.
// The dog approaches from either the left or right side so the card remains completely visible.
func (g *Game) targetPos(i int) (float64, float64, float64) {
	cx, cy := cellCenter(g.board, i)
	sideOffset := 24.0
	facing := 1.0
	tx := cx - sideOffset
	if g.dogX > cx {
		tx = cx + sideOffset
		facing = -1.0
	}
	ty := cy + 8.0 // stand just beside the mound
	return tx, ty, facing
}

// walk moves the dog toward the target mound and starts digging on arrival.
func (g *Game) walk() {
	tx, ty, targetFacing := g.targetPos(g.target)
	dx, dy := tx-g.dogX, ty-g.dogY
	dist := math.Hypot(dx, dy)

	if dist <= dogSpeed {
		g.dogX, g.dogY = tx, ty
		g.facing = targetFacing
		g.walking = false
		g.digging = true
		g.play("dig")
		g.audio.PlayDig()
		return
	}
	g.dogX += dx / dist * dogSpeed
	g.dogY += dy / dist * dogSpeed
	if math.Abs(dx) > 0.5 {
		g.facing = math.Copysign(1, dx)
	}
}

func (g *Game) afterReveal(r board.Result) {
	switch r {
	case board.ResultFirst:
		g.audio.PlayReveal()
		g.play("idle")
	case board.ResultMatch:
		g.audio.PlayMatch()
		g.play("happy")
	case board.ResultWin:
		g.audio.PlayWin()
		g.play("happy")
	case board.ResultMiss:
		g.audio.PlayMiss()
		g.play("sad")
	default:
		g.play("idle")
	}
}

func (g *Game) Draw(screen *ebiten.Image) {
	screen.Fill(color.RGBA{R: 0x1a, G: 0x12, B: 0x0c, A: 0xff})
	drawBoard(screen, g.board, g.focusIndex, g.showFocus, g.sniffTarget, g.sniffTimer > 0)
	g.particles.Draw(screen)

	op := &ebiten.DrawImageOptions{}
	op.Filter = ebiten.FilterNearest
	if g.facing < 0 {
		op.GeoM.Scale(-1, 1)
		op.GeoM.Translate(frameSize, 0)
	}
	op.GeoM.Translate(math.Round(g.dogX)-frameSize/2, math.Round(g.dogY)-frameSize/2)
	screen.DrawImage(g.anims[g.current].Frame(), op)

	msg := fmt.Sprintf("moves: %d", g.board.Moves)
	if g.board.Streak > 1 {
		msg += fmt.Sprintf("  streak: x%d", g.board.Streak)
	}
	ebitenutil.DebugPrintAt(screen, msg, 4, 2)

	sniffLabel := fmt.Sprintf("[S] SNIFF (%d)", g.sniffsLeft)
	ebitenutil.DebugPrintAt(screen, sniffLabel, screenW-155, 2)

	muteLabel := "[M] AUDIO"
	if !g.audio.IsAvailable() {
		muteLabel = "[M] NO SOUND"
	} else if g.audio.IsMuted() {
		muteLabel = "[M] MUTED"
	}
	ebitenutil.DebugPrintAt(screen, muteLabel, screenW-76, 2)

	// Victory banner centered cleanly on screen when round is won
	if g.board.Won {
		bannerW, bannerH := 264, 40
		bx := (screenW - bannerW) / 2
		by := (screenH-bannerH)/2 - 4

		// Drop shadow & dark slate background
		fillRect(screen, bx+2, by+2, bannerW, bannerH, color.RGBA{0x00, 0x00, 0x00, 0xcc})
		fillRect(screen, bx, by, bannerW, bannerH, color.RGBA{0x0a, 0x0e, 0x27, 0xf6})

		// Gold border with corner highlights
		gold := color.RGBA{0xfd, 0xe0, 0x47, 0xff}
		fillRect(screen, bx, by, bannerW, 1, gold)
		fillRect(screen, bx, by+bannerH-1, bannerW, 1, gold)
		fillRect(screen, bx, by, 1, bannerH, gold)
		fillRect(screen, bx+bannerW-1, by, 1, bannerH, gold)

		line1 := fmt.Sprintf("ALL MATCHED IN %d MOVES!", g.board.Moves)
		if g.board.MaxStreak > 1 {
			line1 += fmt.Sprintf(" (Streak x%d)", g.board.MaxStreak)
		}
		line2 := "Click, tap, or press Space to play again"

		tx1 := bx + (bannerW-len(line1)*6)/2
		if tx1 < bx+6 {
			tx1 = bx + 6
		}
		tx2 := bx + (bannerW-len(line2)*6)/2
		if tx2 < bx+6 {
			tx2 = bx + 6
		}

		ebitenutil.DebugPrintAt(screen, line1, tx1, by+7)
		ebitenutil.DebugPrintAt(screen, line2, tx2, by+21)
	}
}

func (g *Game) Layout(outsideW, outsideH int) (int, int) {
	return screenW, screenH
}

func main() {
	ebiten.SetWindowSize(screenW*3, screenH*3)
	ebiten.SetWindowTitle("DigDog")

	g, err := newGame()
	if err != nil {
		log.Fatal(err)
	}
	if err := ebiten.RunGame(g); err != nil {
		log.Fatal(err)
	}
}
