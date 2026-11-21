package board

import "testing"

// newTestBoard is a 2x2 board with faces [0,1,0,1]:
//
//	cell 0 → face 0,  cell 1 → face 1
//	cell 2 → face 0,  cell 3 → face 1
func newTestBoard() *Board {
	return NewBoardFromFaces(2, 2, []int{0, 1, 0, 1})
}

// TestFirstPick verifies that the first card of a turn returns ResultFirst
// and unlocks the board so the next pick is accepted.
func TestFirstPick(t *testing.T) {
	b := newTestBoard()

	if !b.Pick(0) {
		t.Fatal("Pick(0) returned false; expected true")
	}
	r := b.Reveal(0)
	if r != ResultFirst {
		t.Fatalf("Reveal after first pick: got %v, want ResultFirst", r)
	}
	// Board should be unlocked so a second pick is possible.
	if !b.Pick(2) {
		t.Fatal("Pick(2) after ResultFirst returned false; board should be unlocked")
	}
}

// TestMatchingPair verifies that revealing a matching pair returns ResultMatch
// and marks both cards as Matched.
func TestMatchingPair(t *testing.T) {
	b := newTestBoard()

	b.Pick(0)
	b.Reveal(0) // first card (face 0)
	b.Pick(2)
	r := b.Reveal(2) // second card, same face → match

	if r != ResultMatch {
		t.Fatalf("got %v, want ResultMatch", r)
	}
	if b.Card(0).State != Matched {
		t.Errorf("card 0 state = %v, want Matched", b.Card(0).State)
	}
	if b.Card(2).State != Matched {
		t.Errorf("card 2 state = %v, want Matched", b.Card(2).State)
	}
}

// TestMismatchedPair verifies ResultMiss behaviour: the board stays locked,
// and after missTicks calls to Update both cards are Buried and the board is unlocked.
func TestMismatchedPair(t *testing.T) {
	b := newTestBoard()

	b.Pick(0)
	b.Reveal(0) // face 0
	b.Pick(1)
	r := b.Reveal(1) // face 1 → mismatch

	if r != ResultMiss {
		t.Fatalf("got %v, want ResultMiss", r)
	}
	// Board must remain locked immediately after a miss.
	if b.Pick(2) {
		t.Error("Pick succeeded while board is locked after miss")
	}

	// Tick down the miss timer.
	for i := 0; i < missTicks; i++ {
		b.Update()
	}

	if b.Card(0).State != Buried {
		t.Errorf("card 0 state = %v after miss timeout, want Buried", b.Card(0).State)
	}
	if b.Card(1).State != Buried {
		t.Errorf("card 1 state = %v after miss timeout, want Buried", b.Card(1).State)
	}
	// Board must be unlocked now.
	if !b.Pick(0) {
		t.Error("board still locked after miss timeout")
	}
}

// TestPickRestrictions verifies that Pick returns false for an out-of-range
// index, an already-revealed card, and when the board is locked.
func TestPickRestrictions(t *testing.T) {
	b := newTestBoard()

	// Out-of-range index.
	if b.Pick(-1) {
		t.Error("Pick(-1) should return false")
	}
	if b.Pick(99) {
		t.Error("Pick(99) should return false (out of range)")
	}

	// Reveal card 0 (first of a pair) — board unlocks after ResultFirst.
	b.Pick(0)
	b.Reveal(0)

	// Card 0 is now Revealed; picking it again must fail.
	if b.Pick(0) {
		t.Error("Pick of already-Revealed card should return false")
	}

	// Lock the board via a second pick.
	b.Pick(2)
	// While animating / locked (after Pick but before Reveal), another pick fails.
	if b.Pick(3) {
		t.Error("Pick should return false while board is locked")
	}
}

// TestWin verifies that matching the last remaining pair returns ResultWin
// and sets Board.Won.
func TestWin(t *testing.T) {
	b := newTestBoard()

	// Match pair 0 (cells 0 & 2, face 0).
	b.Pick(0)
	b.Reveal(0)
	b.Pick(2)
	b.Reveal(2)

	// Match pair 1 (cells 1 & 3, face 1) — this is the last pair.
	b.Pick(1)
	b.Reveal(1)
	b.Pick(3)
	r := b.Reveal(3)

	if r != ResultWin {
		t.Fatalf("got %v, want ResultWin", r)
	}
	if !b.Won {
		t.Error("Board.Won should be true after the last match")
	}
}

// TestStreak verifies that consecutive matches increase Streak, and a miss resets it to 0.
func TestStreak(t *testing.T) {
	b := newTestBoard()

	// First match
	b.Pick(0)
	b.Reveal(0)
	b.Pick(2)
	b.Reveal(2)
	if b.Streak != 1 {
		t.Errorf("got Streak %d, want 1", b.Streak)
	}

	// Another board to test miss reset
	b2 := newTestBoard()
	b2.Pick(0)
	b2.Reveal(0)
	b2.Pick(2)
	b2.Reveal(2)
	if b2.Streak != 1 {
		t.Errorf("got Streak %d, want 1", b2.Streak)
	}
	// Now a miss
	b2.Pick(1)
	b2.Reveal(1)
	b2.Pick(3) // wait, 1 and 3 match; let's pick 1 and 0 (0 is already matched though)
	// Create board with 6 cards to test match then miss
	b6 := NewBoardFromFaces(3, 2, []int{0, 1, 2, 0, 1, 2})
	b6.Pick(0)
	b6.Reveal(0)
	b6.Pick(3)
	b6.Reveal(3) // match!
	if b6.Streak != 1 {
		t.Errorf("got Streak %d, want 1", b6.Streak)
	}
	b6.Pick(1)
	b6.Reveal(1)
	b6.Pick(5)
	b6.Reveal(5) // mismatch (face 1 vs face 2)
	if b6.Streak != 0 {
		t.Errorf("got Streak %d after miss, want 0", b6.Streak)
	}
	if b6.MaxStreak != 1 {
		t.Errorf("got MaxStreak %d, want 1", b6.MaxStreak)
	}
}

// TestNewBoardDealsEachFaceTwice verifies that a shuffled deal contains every
// face exactly twice, starts with all cards buried, and has the requested shape.
// It runs several deals so a shuffle bug is unlikely to slip through by chance.
func TestNewBoardDealsEachFaceTwice(t *testing.T) {
	const cols, rows = 4, 3
	for deal := 0; deal < 20; deal++ {
		b := NewBoard(cols, rows)
		if b.Cols() != cols || b.Rows() != rows || b.Len() != cols*rows {
			t.Fatalf("deal %d: got %dx%d with %d cards, want %dx%d with %d",
				deal, b.Cols(), b.Rows(), b.Len(), cols, rows, cols*rows)
		}
		counts := map[int]int{}
		for i := 0; i < b.Len(); i++ {
			c := b.Card(i)
			if c.State != Buried {
				t.Fatalf("deal %d: card %d starts in state %v, want Buried", deal, i, c.State)
			}
			counts[c.Face]++
		}
		if len(counts) != cols*rows/2 {
			t.Fatalf("deal %d: got %d distinct faces, want %d", deal, len(counts), cols*rows/2)
		}
		for face, n := range counts {
			if face < 0 || face >= cols*rows/2 {
				t.Fatalf("deal %d: face %d out of range [0,%d)", deal, face, cols*rows/2)
			}
			if n != 2 {
				t.Fatalf("deal %d: face %d dealt %d times, want 2", deal, face, n)
			}
		}
		if b.Moves != 0 || b.Streak != 0 || b.Won {
			t.Fatalf("deal %d: new board has Moves=%d Streak=%d Won=%v", deal, b.Moves, b.Streak, b.Won)
		}
	}
}

// TestNewBoardOddCellsPanics verifies that a board with an odd number of
// cells is rejected, since every card needs a partner.
func TestNewBoardOddCellsPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewBoard(3, 3) did not panic; want panic for odd cell count")
		}
	}()
	NewBoard(3, 3)
}
