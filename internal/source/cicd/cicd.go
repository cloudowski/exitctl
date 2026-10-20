package cicd

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/redact"
	"github.com/cloudowski/exitctl/internal/source"
	"github.com/cloudowski/exitctl/internal/source/dockerfile"
	"github.com/cloudowski/exitctl/internal/walk"
)

// New returns the CI/CD detector for GitHub Actions workflows and GitLab CI
// files: ci_action nodes for `uses:` actions and container_image nodes for
// job, service, and step images.
func New() source.Detector { return detector{} }

type detector struct{}

func (detector) Name() string { return "cicd" }

func (detector) Detect(ctx context.Context, idx walk.Index, opts source.Options) (model.Facts, error) {
	var facts model.Facts
	for _, kind := range []model.SourceKind{model.KindGitHubActions, model.KindGitLabCI} {
		for _, rel := range idx.Files[kind] {
			if err := ctx.Err(); err != nil {
				return facts, err
			}
			facts.Append(detectFile(idx.Root, rel, opts.Repo, kind))
		}
	}
	return facts, nil
}

type fileCtx struct {
	repo, path string
	kind       model.SourceKind
	lines      []string
	images     *dockerfile.ImageSet
	actions    map[string]*model.RawNode
	order      []string
}

func detectFile(root, rel, repo string, kind model.SourceKind) model.Facts {
	var facts model.Facts
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		facts.Sources = append(facts.Sources, source.FailedSource(repo, rel, kind, "file could not be read"))
		return facts
	}
	doc, err := firstDoc(data)
	if err != nil {
		facts.Sources = append(facts.Sources, source.FailedSource(repo, rel, kind, "could not be parsed as YAML"))
		return facts
	}
	fc := &fileCtx{
		repo: repo, path: rel, kind: kind,
		lines:   strings.Split(string(data), "\n"),
		images:  dockerfile.NewImageSet(repo, rel, kind),
		actions: map[string]*model.RawNode{},
	}
	if doc != nil {
		if kind == model.KindGitHubActions {
			fc.github(doc)
		} else {
			fc.gitlab(doc)
		}
	}
	for _, id := range fc.order {
		facts.RawNodes = append(facts.RawNodes, *fc.actions[id])
	}
	facts.RawNodes = append(facts.RawNodes, fc.images.Nodes()...)
	facts.Sources = append(facts.Sources, model.Source{Repo: repo, Path: rel, Kind: kind, Status: model.StatusParsed})
	return facts
}

// firstDoc returns the top-level mapping of the first YAML document, nil for
// an empty file or a non-mapping document.
func firstDoc(data []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(data)).Decode(&doc); err != nil {
		if err == io.EOF {
			return nil, nil
		}
		return nil, err
	}
	n := &doc
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil, nil
	}
	return n, nil
}

func (fc *fileCtx) lineAt(l int) string {
	if l < 1 || l > len(fc.lines) {
		return ""
	}
	return fc.lines[l-1]
}

func get(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func scalar(n *yaml.Node) (string, bool) {
	if n == nil || n.Kind != yaml.ScalarNode || n.Tag == "!!null" {
		return "", false
	}
	return strings.TrimSpace(n.Value), true
}

func (fc *fileCtx) addImage(n *yaml.Node) {
	if s, ok := scalar(n); ok {
		fc.images.Add(s, n.Line, fc.lineAt(n.Line))
	}
}

// --- GitHub Actions ---

func (fc *fileCtx) github(doc *yaml.Node) {
	jobs := get(doc, "jobs")
	if jobs == nil || jobs.Kind != yaml.MappingNode {
		return
	}
	for i := 1; i < len(jobs.Content); i += 2 {
		job := jobs.Content[i]
		if job.Kind != yaml.MappingNode {
			continue
		}
		// Reusable workflow call: jobs.<id>.uses.
		fc.uses(get(job, "uses"))
		if c := get(job, "container"); c != nil {
			if c.Kind == yaml.MappingNode {
				fc.addImage(get(c, "image"))
			} else {
				fc.addImage(c)
			}
		}
		if svcs := get(job, "services"); svcs != nil && svcs.Kind == yaml.MappingNode {
			for j := 1; j < len(svcs.Content); j += 2 {
				s := svcs.Content[j]
				if s.Kind == yaml.MappingNode {
					fc.addImage(get(s, "image"))
				} else {
					fc.addImage(s)
				}
			}
		}
		if steps := get(job, "steps"); steps != nil && steps.Kind == yaml.SequenceNode {
			for _, st := range steps.Content {
				fc.uses(get(st, "uses"))
			}
		}
	}
}

func (fc *fileCtx) uses(n *yaml.Node) {
	u, ok := scalar(n)
	if !ok || u == "" || strings.HasPrefix(u, "./") || strings.HasPrefix(u, "../") {
		return
	}
	if img, ok := strings.CutPrefix(u, "docker://"); ok {
		fc.images.Add(img, n.Line, fc.lineAt(n.Line))
		return
	}
	if strings.Contains(u, "${{") {
		return
	}
	action, ref, _ := strings.Cut(u, "@")
	if action == "" {
		return
	}
	addr := "action/" + action
	id := model.CanonicalID(fc.repo, fc.path, addr)
	if _, seen := fc.actions[id]; seen {
		return // evidence is the first `uses:` line
	}
	attrs := map[string]any{"action": redact.Value("action", action)}
	if ref != "" {
		attrs["ref"] = redact.Value("ref", ref)
	}
	fc.actions[id] = &model.RawNode{
		ID:            id,
		NodeKind:      model.NodeResource,
		SourceKind:    fc.kind,
		Type:          model.TypeCIAction,
		NativeAddress: addr,
		Attributes:    attrs,
		Multiplicity:  model.MultOne,
		Evidence:      []model.Evidence{model.NewEvidence(fc.repo, fc.path, n.Line, redact.Line(fc.lineAt(n.Line)), fc.kind)},
	}
	fc.order = append(fc.order, id)
}

// --- GitLab CI ---

// gitlabGlobals are top-level keys that are not jobs.
var gitlabGlobals = map[string]bool{
	"stages": true, "variables": true, "include": true, "workflow": true, "default": true,
	"before_script": true, "after_script": true, "cache": true, "image": true, "services": true,
}

func (fc *fileCtx) gitlab(doc *yaml.Node) {
	fc.gitlabJob(doc)
	fc.gitlabJob(get(doc, "default"))
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if !gitlabGlobals[doc.Content[i].Value] {
			fc.gitlabJob(doc.Content[i+1])
		}
	}
}

// gitlabJob records `image:` (string or {name}) and `services:` entries
// (string or {name}) of a job or of the global/default section.
func (fc *fileCtx) gitlabJob(m *yaml.Node) {
	if m == nil || m.Kind != yaml.MappingNode {
		return
	}
	if img := get(m, "image"); img != nil {
		if img.Kind == yaml.MappingNode {
			fc.addImage(get(img, "name"))
		} else {
			fc.addImage(img)
		}
	}
	if svcs := get(m, "services"); svcs != nil && svcs.Kind == yaml.SequenceNode {
		for _, s := range svcs.Content {
			if s.Kind == yaml.MappingNode {
				fc.addImage(get(s, "name"))
			} else {
				fc.addImage(s)
			}
		}
	}
}
