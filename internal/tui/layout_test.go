package tui

import (
	"strings"
	"testing"

	"github.com/JustSteveKing/taskgo/internal/store"
	"github.com/charmbracelet/lipgloss"
)

// The panels are sized by arithmetic on the terminal dimensions, and getting
// that wrong pushes the footer off the bottom where nobody sees it. This
// checks the whole view fits, at sizes from cramped to generous.
//
// Worth knowing: on a fractionally-scaled display some terminals report more
// rows than they actually draw, which makes every TUI overflow — btop included.
// That is the terminal lying about its size, not this arithmetic being wrong,
// and this test is how you tell the two apart.
func TestViewFitsTerminal(t *testing.T) {
	s, _ := store.Open(t.TempDir())
	for i := 0; i < 5; i++ {
		_, _ = s.Create(store.ActorHuman, store.NewTask{Title: "Task", Project: "p"})
	}

	for _, size := range [][2]int{{129, 63}, {120, 40}, {80, 24}, {200, 60}, {60, 15}} {
		m := New(s, "test")
		m.width, m.height = size[0], size[1]
		msg := m.load()().(loadedMsg)
		mm, _ := m.Update(msg)
		m = mm.(model)

		out := m.View()
		lines := strings.Split(out, "\n")
		widest := 0
		for _, l := range lines {
			if w := lipgloss.Width(l); w > widest {
				widest = w
			}
		}
		detailAt := -1
		for i, l := range lines {
			if strings.Contains(l, "Detail") {
				detailAt = i
			}
		}
		t.Logf("term %dx%d -> %d lines, widest %d, Detail box starts at line %d (%.0f%%)",
			size[0], size[1], len(lines), widest, detailAt, float64(detailAt)/float64(size[1])*100)
		if len(lines) > size[1] {
			t.Errorf("term height %d but view is %d lines", size[1], len(lines))
		}
		if widest > size[0] {
			t.Errorf("term width %d but view is %d wide", size[0], widest)
		}
	}
}

// Adding a view must never be able to push the layout off the bottom. This is
// the regression that adding "Needs you" caused: the side column insisted on
// its natural height and overran a 15-row terminal by a line.
func TestSideColumnFitsWhateverTheViewCountIs(t *testing.T) {
	s, _ := store.Open(t.TempDir())

	for _, height := range []int{10, 12, 15, 20, 40} {
		m := New(s, "test")
		m.width, m.height = 100, height

		body := height - 2
		side := m.sideColumn(body)
		if got := len(strings.Split(side, "\n")); got != body {
			t.Errorf("height %d: side column is %d lines, want %d", height, got, body)
		}
	}
}
