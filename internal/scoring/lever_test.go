package scoring

import (
	"testing"

	"github.com/cloudowski/exitctl/internal/catalog"
)

func TestBetterLeverTieBreak(t *testing.T) {
	a := &Lever{Service: "aws.b", Difficulty: "low", Delta: 5}
	b := &Lever{Service: "aws.a", Difficulty: "medium", Delta: 5}
	if !better(a, b) {
		t.Fatal("equal delta: lower difficulty must win")
	}
	c := &Lever{Service: "aws.a", Difficulty: "low", Delta: 5}
	if !better(c, a) {
		t.Fatal("equal delta and difficulty: lower service ID must win")
	}
	if better(&Lever{Delta: 4, Difficulty: "low"}, &Lever{Delta: 5, Difficulty: "very-high"}) {
		t.Fatal("larger delta must win")
	}
	if catalog.DifficultyRank("low") >= catalog.DifficultyRank("high") {
		t.Fatal("difficulty rank order")
	}
}
