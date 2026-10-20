package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseBand(t *testing.T) {
	ok := map[string]Band{
		"C-D": {2, 3}, "A": {0, 0}, "a-b": {0, 1}, " D - E ": {3, 4}, "E": {4, 4},
	}
	for in, want := range ok {
		got, err := ParseBand(in)
		if err != nil || got != want {
			t.Errorf("ParseBand(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "F", "D-B", "A-", "C-D-E", "AB"} {
		if _, err := ParseBand(in); err == nil {
			t.Errorf("ParseBand(%q) must fail", in)
		}
	}
	if s := (Band{2, 3}).String(); s != "C-D" {
		t.Errorf("String = %q", s)
	}
	if s := (Band{1, 1}).String(); s != "B" {
		t.Errorf("String = %q", s)
	}
}

func TestDeviation(t *testing.T) {
	cd := Band{2, 3}
	cases := map[string]int{"A": 2, "B": 1, "C": 0, "D": 0, "E": 1}
	for g, want := range cases {
		if got := Deviation(gradePos(g), cd); got != want {
			t.Errorf("Deviation(%s, C-D) = %d, want %d", g, got, want)
		}
	}
	if got := Deviation(gradePos("E"), Band{0, 0}); got != 4 {
		t.Errorf("Deviation(E, A) = %d", got)
	}
}

func TestMedian(t *testing.T) {
	cases := []struct {
		in   []float64
		want float64
	}{
		{nil, 0}, {[]float64{0.5}, 0.5}, {[]float64{0.9, 0.1, 0.5}, 0.5}, {[]float64{1, 0.8, 0.9, 0.7}, 0.85},
	}
	for _, c := range cases {
		if got := Median(c.in); got < c.want-1e-9 || got > c.want+1e-9 {
			t.Errorf("Median(%v) = %v, want %v", c.in, got, c.want)
		}
	}
	in := []float64{3, 1, 2}
	Median(in)
	if in[0] != 3 {
		t.Error("Median must not reorder its input")
	}
}

func row(grade, band string, cov float64) Row {
	b, _ := ParseBand(band)
	r := Row{Band: b, Grade: grade, Coverage: cov, Scanned: true, Repo: Repo{ExpectedBandStatus: "confirmed"}}
	if g := gradePos(grade); g >= 0 {
		r.Deviation, r.HasDev = Deviation(g, b), true
	}
	return r
}

func rows(n int, grade, band string, cov float64) []Row {
	var out []Row
	for range n {
		out = append(out, row(grade, band, cov))
	}
	return out
}

func TestGatePasses(t *testing.T) {
	rs := append(rows(18, "C", "C-D", 0.95), row("B", "C-D", 0.92), row("D", "D-E", 0.8))
	s := Summarize(rs)
	if s.Within != 19 || s.MaxDeviation != 1 || s.WithinPct != 0.95 {
		t.Fatalf("summary %+v", s)
	}
	if f := Gate(s); len(f) != 0 {
		t.Fatalf("gate must pass: %v", f)
	}
}

func TestGateFailures(t *testing.T) {
	// 90% exactly passes the within-band rule.
	s := Summarize(append(rows(9, "A", "A-B", 0.95), row("C", "A-B", 0.95)))
	if f := Gate(s); len(f) != 0 {
		t.Fatalf("90%% within and deviation 1 must pass: %v", f)
	}
	// Too few within band.
	s = Summarize(append(rows(8, "A", "A-B", 0.95), row("C", "A-B", 0.95), row("C", "A-B", 0.95)))
	if f := Gate(s); len(f) != 1 || !strings.Contains(f[0], "SC-004") {
		t.Fatalf("want one within-band failure, got %v", f)
	}
	// Off by two grades.
	s = Summarize(append(rows(19, "C", "C-D", 0.95), row("A", "C-D", 0.95)))
	if f := Gate(s); len(f) != 1 || !strings.Contains(f[0], "2 grades") {
		t.Fatalf("want deviation failure, got %v", f)
	}
	// Low median coverage.
	s = Summarize(rows(3, "C", "C", 0.89))
	if f := Gate(s); len(f) != 1 || !strings.Contains(f[0], "SC-005") {
		t.Fatalf("want coverage failure, got %v", f)
	}
	// Errors and withheld grades count as not within band; errors have no coverage.
	errRow := Row{Err: "fetch failed", Band: Band{0, 1}}
	withheld := row("", "A-B", 0.2)
	s = Summarize(append(rows(2, "A", "A-B", 0.95), errRow, withheld))
	if s.Within != 2 || s.Ungraded != 2 || s.MedianCoverage != 0.95 {
		t.Fatalf("summary %+v", s)
	}
	if errRow.Status() != "error" || withheld.Status() != "no grade" {
		t.Fatal("status labels")
	}
	if f := Gate(Summary{}); len(f) != 1 {
		t.Fatal("empty corpus must fail")
	}
}

func TestReport(t *testing.T) {
	idx := 45
	r := row("C", "C-D", 0.931)
	r.Index, r.Confidence = &idx, "normal"
	r.Repo.URL, r.Repo.Commit = "https://github.com/acme/infra", "0123456789abcdef0123456789abcdef01234567"
	rs := []Row{r, row("A", "C-D", 0.5)}
	s := Summarize(rs)
	var b bytes.Buffer
	writeReport(&b, reportMeta{Corpus: "c.yaml", Date: "2026-10-06", Versions: "v1", Drafts: true}, rs, s, Gate(s))
	out := b.String()
	for _, want := range []string{
		"| github.com/acme/infra | `0123456789ab` | 45 | C | normal | 93% | C-D | 0 | within band |",
		"off by 2 (gate)", "**Gate: FAILED**", "drafts included",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}

func TestSelected(t *testing.T) {
	u := "https://github.com/stefanprodan/podinfo"
	for only, want := range map[string]bool{"": true, "podinfo": true, "x,podinfo": true, "x, y": false, ",": false} {
		if got := selected(u, only); got != want {
			t.Errorf("selected(%q) = %v, want %v", only, got, want)
		}
	}
}

func TestCacheName(t *testing.T) {
	if got := cacheName("https://github.com/aws-samples/eks-workshop-v2.git"); got != "github.com_aws-samples_eks-workshop-v2" {
		t.Errorf("cacheName = %q", got)
	}
}
