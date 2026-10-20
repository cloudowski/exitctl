package result

import (
	"strings"
	"testing"
)

func TestGradeMeaningCoversEveryGrade(t *testing.T) {
	for _, g := range []string{"A", "B", "C", "D", "E"} {
		m := GradeMeaning(g)
		if m == "" || !strings.HasSuffix(m, ".") {
			t.Errorf("grade %s: want a sentence, got %q", g, m)
		}
	}
	if GradeMeaning("F") != "" || GradeMeaning("") != "" {
		t.Error("unknown grades must have no meaning")
	}
}
