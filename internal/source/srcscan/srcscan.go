// Package srcscan reads source files line by line for the detectors that
// match code with line-oriented patterns. It skips comments, so commented
// out code never counts as proof, and long lines (minified code).
package srcscan

import (
	"bufio"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/cloudowski/exitctl/internal/model"
)

const (
	// maxLineBuffer is the largest line the scanner can hold.
	maxLineBuffer = 1 << 20
	// MaxLine is the longest line passed on; longer lines (minified code,
	// embedded data) are skipped.
	MaxLine = 4096
)

// Comments describes the comment syntax of a language family.
type Comments struct {
	Hash  bool // '#' starts a line comment (not '#[' attributes)
	Slash bool // '//' line comments and '/* */' blocks
	Ruby  bool // =begin / =end blocks
}

// ForEcosystem returns the comment syntax of an ecosystem's languages.
func ForEcosystem(eco model.Ecosystem) Comments {
	switch eco {
	case model.EcoPyPI:
		return Comments{Hash: true}
	case model.EcoRubyGems:
		return Comments{Hash: true, Ruby: true}
	case model.EcoComposer:
		return Comments{Hash: true, Slash: true}
	}
	return Comments{Slash: true}
}

// ForFile returns the comment syntax of a source file by its extension.
func ForFile(rel string) Comments {
	switch strings.ToLower(path.Ext(rel)) {
	case ".py":
		return ForEcosystem(model.EcoPyPI)
	case ".rb", ".rake":
		return ForEcosystem(model.EcoRubyGems)
	case ".php":
		return ForEcosystem(model.EcoComposer)
	}
	return Comments{Slash: true}
}

// Lines calls fn for every non-blank, non-comment line of a file with its
// 1-based line number. A file that cannot be read yields no lines (the walk
// already accounted for it).
func Lines(root, rel string, cm Comments, fn func(no int, line string)) {
	scan(root, rel, cm, fn)
}

// maxJoin is the number of following lines joined to a line that ends with
// an open parenthesis.
const maxJoin = 3

// LinesJoined is Lines for matching calls whose arguments continue on the
// next lines (formatter output). joined is the line followed by up to three
// more non-comment lines, space-joined, when the line ends with '(';
// otherwise it is the line itself.
func LinesJoined(root, rel string, cm Comments, fn func(no int, line, joined string)) {
	type entry struct {
		no   int
		text string
	}
	var all []entry
	scan(root, rel, cm, func(no int, line string) { all = append(all, entry{no, line}) })
	for i, e := range all {
		joined := e.text
		if t := strings.TrimSpace(e.text); strings.HasSuffix(t, "(") {
			for j := i + 1; j < len(all) && j <= i+maxJoin; j++ {
				joined += " " + strings.TrimSpace(all[j].text)
				if strings.Contains(all[j].text, ")") {
					break
				}
			}
		}
		fn(e.no, e.text, joined)
	}
}

func scan(root, rel string, cm Comments, fn func(no int, line string)) {
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), maxLineBuffer)
	inBlock := false
	no := 0
	for sc.Scan() {
		no++
		line := sc.Text()
		if len(line) > MaxLine {
			continue
		}
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if cm.Ruby {
			if inBlock {
				if strings.HasPrefix(t, "=end") {
					inBlock = false
				}
				continue
			}
			if strings.HasPrefix(t, "=begin") {
				inBlock = true
				continue
			}
		}
		if cm.Hash && strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "#[") {
			continue
		}
		if cm.Slash {
			if inBlock {
				if strings.Contains(t, "*/") {
					inBlock = false
				}
				continue
			}
			if strings.HasPrefix(t, "//") {
				continue
			}
			if strings.HasPrefix(t, "/*") {
				if !strings.Contains(t[2:], "*/") {
					inBlock = true
				}
				continue
			}
		}
		fn(no, line)
	}
}
