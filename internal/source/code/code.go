// Package code finds source-level proof of cloud SDK use (research R8): it
// scans the source files of components that depend on an SDK package with
// the catalog's line-oriented RE2 patterns.
package code

import (
	"context"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/redact"
	"github.com/cloudowski/exitctl/internal/sdkname"
	"github.com/cloudowski/exitctl/internal/source"
	"github.com/cloudowski/exitctl/internal/source/srcscan"
	"github.com/cloudowski/exitctl/internal/walk"
)

const (
	// maxMatches bounds CodeUses per (component, service).
	maxMatches = 200
)

// Pattern kinds.
const (
	KindImport = "import"
	KindUsage  = "usage"
)

// extsByEcosystem lists the source file extensions scanned per ecosystem.
var extsByEcosystem = map[model.Ecosystem][]string{
	model.EcoNPM:      {".js", ".mjs", ".cjs", ".ts", ".tsx", ".jsx"},
	model.EcoPyPI:     {".py"},
	model.EcoGo:       {".go"},
	model.EcoMaven:    {".java", ".kt", ".scala", ".sc"},
	model.EcoNuGet:    {".cs"},
	model.EcoRubyGems: {".rb", ".rake"},
	model.EcoComposer: {".php"},
	model.EcoCargo:    {".rs"},
}

// Detector emits CodeUses. It is a dependent detector: it needs
// opts.Packages (from the packages detector) and opts.Catalog.
type Detector struct {
	mu    sync.Mutex
	cache map[string]*regexp.Regexp
}

// New returns the code detector.
func New() source.Detector { return &Detector{cache: map[string]*regexp.Regexp{}} }

// Name implements source.Detector.
func (*Detector) Name() string { return "code" }

// compile returns the compiled pattern, or nil when it is not valid RE2.
// Patterns are compiled once per detector.
func (d *Detector) compile(p string) *regexp.Regexp {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cache == nil {
		d.cache = map[string]*regexp.Regexp{}
	}
	if re, ok := d.cache[p]; ok {
		return re
	}
	re, err := regexp.Compile(p)
	if err != nil {
		re = nil
	}
	d.cache[p] = re
	return re
}

// globMatch reports whether name matches a package glob (path.Match) or
// equals it.
func globMatch(pattern, name string) bool {
	if pattern == name {
		return true
	}
	ok, err := path.Match(pattern, name)
	return err == nil && ok
}

func anyMatch(patterns []string, name string) bool {
	for _, p := range patterns {
		if globMatch(p, name) {
			return true
		}
	}
	return false
}

type rule struct {
	service string
	kind    string
	re      *regexp.Regexp
	group   int // submatch recorded as the match; 0 = the whole match
}

type hit struct {
	service, kind, file, match, excerpt string
	line                                int
}

// Detect implements source.Detector.
func (d *Detector) Detect(ctx context.Context, idx walk.Index, opts source.Options) (model.Facts, error) {
	var facts model.Facts
	if opts.Catalog == nil || len(opts.Packages) == 0 {
		return facts, nil
	}
	root := idx.Root
	if root == "" {
		root = opts.Root
	}

	patterns := map[model.Ecosystem][]catalog.ServicePattern{}
	for _, cf := range source.CodeComponents(idx, opts) {
		comp, uses, files := cf.Component, cf.Packages, cf.Files
		sort.SliceStable(uses, func(i, j int) bool {
			if uses[i].Name != uses[j].Name {
				return uses[i].Name < uses[j].Name
			}
			return uses[i].Manifest.Less(uses[j].Manifest)
		})
		ecos := map[model.Ecosystem][]model.PackageUse{}
		for _, u := range uses {
			ecos[u.Ecosystem] = append(ecos[u.Ecosystem], u)
		}
		ecoList := make([]string, 0, len(ecos))
		for e := range ecos {
			ecoList = append(ecoList, string(e))
		}
		sort.Strings(ecoList)

		count := map[string]int{} // CodeUses per service in this component
		for _, es := range ecoList {
			if err := ctx.Err(); err != nil {
				return facts, err
			}
			eco := model.Ecosystem(es)
			pus := ecos[eco]
			sps, ok := patterns[eco]
			if !ok {
				sps = opts.Catalog.SDKPatterns(es)
				patterns[eco] = sps
			}
			var rules []rule
			specific := map[string]model.PackageUse{} // service -> first matched package
			var specificOrder []string
			for _, sp := range sps {
				var pkgHit *model.PackageUse
				gate := false
				for i := range pus {
					if pkgHit == nil && anyMatch(sp.Pattern.Packages, pus[i].Name) {
						pkgHit = &pus[i]
					}
					if !gate && anyMatch(sp.Pattern.Gate, pus[i].Name) {
						gate = true
					}
				}
				if pkgHit != nil {
					if _, seen := specific[sp.ServiceID]; !seen {
						specific[sp.ServiceID] = *pkgHit
						specificOrder = append(specificOrder, sp.ServiceID)
					}
					for _, p := range sp.Pattern.Imports {
						if re := d.compile(p); re != nil {
							rules = append(rules, rule{service: sp.ServiceID, kind: KindImport, re: re})
						}
					}
				}
				if gate {
					for _, p := range sp.Pattern.Usage {
						if re := d.compile(p); re != nil {
							rules = append(rules, rule{service: sp.ServiceID, kind: KindUsage, re: re})
						}
					}
				}
			}
			if eco == model.EcoPyPI && hasBotoClient(pus) {
				// Names the service of every boto3 client/resource call so
				// the graph can report the ones the catalog does not map.
				rules = append(rules, rule{kind: model.CodeKindSDKName, re: sdkname.BotoClient, group: 1})
			}
			if len(rules) > 0 {
				for _, f := range files {
					if !hasExt(f, extsByEcosystem[eco]) {
						continue
					}
					hits := scanFile(root, f, rules, srcscan.ForEcosystem(eco))
					mapped := map[int]bool{} // lines with a catalog service hit
					for _, h := range hits {
						if h.service != "" {
							mapped[h.line] = true
						}
					}
					for _, h := range hits {
						if h.kind == model.CodeKindSDKName && mapped[h.line] {
							continue
						}
						if count[h.service] >= maxMatches {
							continue
						}
						count[h.service]++
						facts.CodeUses = append(facts.CodeUses, model.CodeUse{
							Component:   comp,
							Ecosystem:   eco,
							PatternKind: h.kind,
							Service:     h.service,
							Match:       h.match,
							Evidence:    model.NewEvidence(opts.Repo, h.file, h.line, h.excerpt, model.KindCode),
						})
					}
				}
			}
			// Service-specific package without any source hit: the
			// dependency itself is the proof, at the manifest line.
			for _, svc := range specificOrder {
				if count[svc] > 0 {
					continue
				}
				pu := specific[svc]
				count[svc]++
				facts.CodeUses = append(facts.CodeUses, model.CodeUse{
					Component:   comp,
					Ecosystem:   eco,
					PatternKind: KindImport,
					Service:     svc,
					Match:       pu.Name,
					Evidence:    pu.Manifest,
				})
			}
		}
	}
	facts.Sort()
	return facts, nil
}

// hasBotoClient reports whether any package is a boto3-style generic client.
func hasBotoClient(pus []model.PackageUse) bool {
	for _, p := range pus {
		switch p.Name {
		case "boto3", "aioboto3", "botocore", "aiobotocore":
			return true
		}
	}
	return false
}

func hasExt(f string, exts []string) bool {
	ext := strings.ToLower(path.Ext(f))
	for _, e := range exts {
		if ext == e {
			return true
		}
	}
	return false
}

// scanFile applies rules to each non-comment line of a file.
func scanFile(root, rel string, rules []rule, cm srcscan.Comments) []hit {
	var out []hit
	type seenKey struct {
		service, kind string
	}
	srcscan.Lines(root, rel, cm, func(lineNo int, line string) {
		seen := map[seenKey]bool{}
		for _, r := range rules {
			k := seenKey{r.service, r.kind}
			if seen[k] {
				continue
			}
			loc := r.re.FindStringSubmatchIndex(line)
			if loc == nil {
				continue
			}
			lo, hi := loc[0], loc[1]
			if r.group > 0 && len(loc) > 2*r.group+1 && loc[2*r.group] >= 0 {
				lo, hi = loc[2*r.group], loc[2*r.group+1]
			}
			seen[k] = true
			out = append(out, hit{
				service: r.service,
				kind:    r.kind,
				file:    rel,
				line:    lineNo,
				match:   redact.Line(line[lo:hi]),
				excerpt: redact.Line(line),
			})
		}
	})
	return out
}
