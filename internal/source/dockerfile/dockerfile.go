package dockerfile

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/parser"

	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/redact"
	"github.com/cloudowski/exitctl/internal/source"
	"github.com/cloudowski/exitctl/internal/walk"
)

// New returns the Dockerfile detector: one container_image node per base
// image (FROM) and per registry image copied with COPY --from.
func New() source.Detector { return detector{} }

type detector struct{}

func (detector) Name() string { return "dockerfile" }

func (detector) Detect(ctx context.Context, idx walk.Index, opts source.Options) (model.Facts, error) {
	var facts model.Facts
	for _, rel := range idx.Files[model.KindDockerfile] {
		if err := ctx.Err(); err != nil {
			return facts, err
		}
		f := detectFile(idx.Root, rel, opts.Repo)
		facts.Append(f)
	}
	return facts, nil
}

func detectFile(root, rel, repo string) model.Facts {
	var facts model.Facts
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		facts.Sources = append(facts.Sources, source.FailedSource(repo, rel, model.KindDockerfile, "file could not be read"))
		return facts
	}
	res, err := parser.Parse(bytes.NewReader(data))
	if err != nil || res == nil || res.AST == nil {
		facts.Sources = append(facts.Sources, source.FailedSource(repo, rel, model.KindDockerfile, "could not be parsed as a Dockerfile"))
		return facts
	}
	lines := strings.Split(string(data), "\n")
	images := NewImageSet(repo, rel, model.KindDockerfile)
	stages := map[string]bool{}
	for _, n := range res.AST.Children {
		switch strings.ToLower(n.Value) {
		case "from":
			if n.Next == nil {
				continue
			}
			img := n.Next.Value
			// FROM <image> [AS <name>]: a FROM naming an earlier stage is
			// not an image.
			if !stages[strings.ToLower(img)] {
				images.Add(img, n.StartLine, lineAt(lines, n.StartLine))
			}
			// Stage names become visible to later instructions only.
			if a := n.Next.Next; a != nil && strings.EqualFold(a.Value, "as") && a.Next != nil {
				stages[strings.ToLower(a.Next.Value)] = true
			}
		case "copy":
			for _, fl := range n.Flags {
				v, ok := strings.CutPrefix(fl, "--from=")
				if !ok {
					continue
				}
				v = strings.Trim(v, `"'`)
				if stages[strings.ToLower(v)] || isStageIndex(v) {
					continue
				}
				images.Add(v, n.StartLine, lineAt(lines, n.StartLine))
			}
		}
	}
	facts.RawNodes = images.Nodes()
	facts.Sources = append(facts.Sources, model.Source{Repo: repo, Path: rel, Kind: model.KindDockerfile, Status: model.StatusParsed})
	return facts
}

func isStageIndex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func lineAt(lines []string, line int) string {
	if line < 1 || line > len(lines) {
		return ""
	}
	return lines[line-1]
}

// ImageSet collects container_image nodes for one file, merging repeated
// references of the same image into one node with several evidence lines.
type ImageSet struct {
	repo, path string
	kind       model.SourceKind
	order      []string
	nodes      map[string]*model.RawNode
}

// NewImageSet returns an empty set for the file at path (repo-relative).
func NewImageSet(repo, path string, kind model.SourceKind) *ImageSet {
	return &ImageSet{repo: repo, path: path, kind: kind, nodes: map[string]*model.RawNode{}}
}

// Add records an image reference seen at line (rawLine is the source line).
// Empty references, "scratch", and references containing variables are
// ignored. It reports whether the reference was recorded.
func (s *ImageSet) Add(ref string, line int, rawLine string) bool {
	ref = strings.TrimSpace(ref)
	if !Usable(ref) {
		return false
	}
	ev := model.NewEvidence(s.repo, s.path, line, redact.Line(rawLine), s.kind)
	id := model.CanonicalID(s.repo, s.path, "image/"+ref)
	if n, ok := s.nodes[id]; ok {
		n.Evidence = append(n.Evidence, ev)
		return true
	}
	s.nodes[id] = &model.RawNode{
		ID:            id,
		NodeKind:      model.NodeResource,
		SourceKind:    s.kind,
		Type:          model.TypeContainerImage,
		NativeAddress: "image/" + ref,
		Attributes: map[string]any{
			"image":    redact.Value("image", ref),
			"registry": redact.Value("registry", Registry(ref)),
		},
		Multiplicity: model.MultOne,
		Evidence:     []model.Evidence{ev},
	}
	s.order = append(s.order, id)
	return true
}

// Nodes returns the collected nodes in first-seen order.
func (s *ImageSet) Nodes() []model.RawNode {
	out := make([]model.RawNode, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, *s.nodes[id])
	}
	return out
}

// Usable reports whether ref is a concrete image reference: not empty, not
// "scratch", and free of build-arg or CI variables.
func Usable(ref string) bool {
	if ref == "" || strings.EqualFold(ref, "scratch") {
		return false
	}
	return !strings.ContainsAny(ref, "$ \t{}")
}

// Registry returns the registry host of an image reference: the first path
// component when it looks like a host (contains "." or ":" or is
// "localhost"), else "docker.io".
func Registry(ref string) string {
	first, _, ok := strings.Cut(ref, "/")
	if !ok {
		return "docker.io"
	}
	if strings.ContainsAny(first, ".:") || first == "localhost" {
		return strings.ToLower(first)
	}
	return "docker.io"
}
