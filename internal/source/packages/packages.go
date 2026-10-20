// Package packages detects application components and their declared
// dependencies (research R7, data-model 2.4). A component is a directory
// containing a dependency manifest; lockfiles and SBOM files in the same
// directory belong to the same component.
package packages

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/redact"
	"github.com/cloudowski/exitctl/internal/source"
	"github.com/cloudowski/exitctl/internal/walk"
)

// Pkg is one package found in a manifest, lockfile, or SBOM.
type Pkg struct {
	Ecosystem    model.Ecosystem
	Name         string // Maven: "groupId:artifactId"; Go: module path; PyPI: PEP 503 normalized
	Version      string
	ManifestPath string // repo-relative, '/' separators
	Line         int    // 1-based; 0 when unknown
}

// PackageDetector finds packages under root. manifests are the repo-relative
// dependency manifests found by the walk.
type PackageDetector interface {
	Packages(ctx context.Context, root string, manifests []string) ([]Pkg, error)
}

// Detector emits component nodes and PackageUses.
type Detector struct {
	// Reader parses direct-dependency manifests (exact lines).
	Reader ManifestReader
	// Extra reads lockfiles next to manifests and SBOM files. Nil disables it.
	Extra PackageDetector
}

// New returns the packages detector: the built-in manifest reader plus the
// built-in lockfile and SBOM reader.
func New() source.Detector { return &Detector{Extra: NewLockReader()} }

// NewWith returns a detector using the given extra PackageDetector (nil for
// manifest reading only). It receives lockfiles near manifests and SBOMs.
func NewWith(extra PackageDetector) source.Detector { return &Detector{Extra: extra} }

// Name implements source.Detector.
func (*Detector) Name() string { return "packages" }

// lockNames are files that pin versions (lockfiles) rather than declare
// direct dependencies. Their packages are used only when the component has
// no direct manifest of the same ecosystem; otherwise they only fill in
// resolved versions.
var lockNames = map[string]bool{
	"package-lock.json": true, "npm-shrinkwrap.json": true, "yarn.lock": true, "pnpm-lock.yaml": true,
	"bun.lock": true, "deno.lock": true, "poetry.lock": true, "Pipfile.lock": true, "uv.lock": true,
	"pdm.lock": true, "go.sum": true, "gradle.lockfile": true, "packages.lock.json": true,
	"Gemfile.lock": true, "composer.lock": true, "Cargo.lock": true,
}

func isLock(rel string) bool {
	name := path.Base(rel)
	return lockNames[name] || strings.HasPrefix(name, "gradle.lockfile") || strings.HasSuffix(name, ".deps.json")
}

// IsSBOMFile reports whether a file name looks like an SBOM document
// (CycloneDX, SPDX, and syft JSON naming conventions).
func IsSBOMFile(rel string) bool { return walk.IsSBOMName(rel) }

// directEcosystem is the ecosystem a direct manifest declares ("" if none).
func directEcosystem(rel string) model.Ecosystem {
	name := path.Base(rel)
	lower := strings.ToLower(name)
	switch {
	case name == "package.json":
		return model.EcoNPM
	case strings.HasPrefix(name, "requirements") && strings.HasSuffix(lower, ".txt"),
		name == "pyproject.toml", name == "Pipfile", name == "setup.py":
		return model.EcoPyPI
	case name == "go.mod":
		return model.EcoGo
	case name == "pom.xml", name == "build.gradle", name == "build.gradle.kts", name == "build.sbt":
		return model.EcoMaven
	case name == "Gemfile":
		return model.EcoRubyGems
	case name == "composer.json":
		return model.EcoComposer
	case name == "Cargo.toml":
		return model.EcoCargo
	case strings.HasSuffix(lower, ".csproj"):
		return model.EcoNuGet
	}
	return ""
}

func compDir(rel string) string {
	d := path.Dir(rel)
	if d == "" {
		return "."
	}
	return d
}

type fileLines struct{ cache map[string][]string }

func (f *fileLines) get(root, rel string) []string {
	if l, ok := f.cache[rel]; ok {
		return l
	}
	var lines []string
	if b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
		lines = splitLines(b)
	}
	f.cache[rel] = lines
	return lines
}

func (f *fileLines) evidence(repo, root, rel string, line int, kind model.SourceKind) model.Evidence {
	lines := f.get(root, rel)
	if line < 1 {
		line = 1
	}
	text := ""
	if line <= len(lines) {
		text = lines[line-1]
	}
	return model.NewEvidence(repo, rel, line, redact.Line(text), kind)
}

func fileKind(rel string) model.SourceKind {
	if IsSBOMFile(rel) {
		return model.KindSBOM
	}
	return model.KindPackageManifest
}

// Detect implements source.Detector.
func (d *Detector) Detect(ctx context.Context, idx walk.Index, opts source.Options) (model.Facts, error) {
	var facts model.Facts
	root := idx.Root
	if root == "" {
		root = opts.Root
	}
	manifests := append([]string(nil), idx.Files[model.KindPackageManifest]...)
	sort.Strings(manifests)
	lines := &fileLines{cache: map[string][]string{}}

	// 1. Direct manifests with the built-in reader.
	direct := map[string][]Pkg{} // by file
	readerFiles := map[string]bool{}
	for _, m := range manifests {
		if err := ctx.Err(); err != nil {
			return facts, err
		}
		if !d.Reader.Supports(m) {
			facts.Sources = append(facts.Sources, model.Source{Repo: opts.Repo, Path: m, Kind: model.KindPackageManifest, Status: model.StatusParsed})
			continue
		}
		readerFiles[m] = true
		ps, err := d.Reader.ReadFile(root, m)
		if err != nil {
			facts.Sources = append(facts.Sources, source.FailedSource(opts.Repo, m, model.KindPackageManifest, "could not be parsed as a dependency manifest"))
			continue
		}
		facts.Sources = append(facts.Sources, model.Source{Repo: opts.Repo, Path: m, Kind: model.KindPackageManifest, Status: model.StatusParsed})
		direct[m] = ps
	}

	// 2. Lockfiles next to the manifests and SBOM files anywhere. A file
	//    that cannot be read only loses its own packages.
	extra := map[string][]Pkg{}
	var extraPkgs []Pkg
	if d.Extra != nil {
		files := append(lockFilesNear(root, manifests), idx.Files[model.KindSBOM]...)
		sort.Strings(files)
		var err error
		extraPkgs, err = d.Extra.Packages(ctx, root, files)
		var pe ParseErrors
		if errors.As(err, &pe) {
			for _, f := range pe {
				facts.Sources = append(facts.Sources, source.FailedSource(opts.Repo, f, fileKind(f), "could not be parsed as a lockfile or SBOM"))
			}
		}
	}
	for _, p := range extraPkgs {
		if readerFiles[p.ManifestPath] {
			continue
		}
		extra[p.ManifestPath] = append(extra[p.ManifestPath], p)
	}
	extraFiles := make([]string, 0, len(extra))
	for f := range extra {
		extraFiles = append(extraFiles, f)
	}
	sort.Strings(extraFiles)
	for _, f := range extraFiles {
		if !containsString(manifests, f) {
			facts.Sources = append(facts.Sources, model.Source{Repo: opts.Repo, Path: f, Kind: fileKind(f), Status: model.StatusParsed})
		}
	}

	// 3. Components: directories of manifests (and of SBOM files).
	compFiles := map[string][]string{}
	for _, m := range manifests {
		compFiles[compDir(m)] = append(compFiles[compDir(m)], m)
	}
	for _, f := range extraFiles {
		if dir := compDir(f); compFiles[dir] == nil {
			compFiles[dir] = append(compFiles[dir], f)
		}
	}
	dirs := make([]string, 0, len(compFiles))
	for dir := range compFiles {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)

	// Direct ecosystems per directory (from file names, even without deps).
	directEco := map[string]map[model.Ecosystem]bool{}
	for _, m := range manifests {
		if eco := directEcosystem(m); eco != "" {
			dir := compDir(m)
			if directEco[dir] == nil {
				directEco[dir] = map[model.Ecosystem]bool{}
			}
			directEco[dir][eco] = true
		}
	}

	for _, dir := range dirs {
		files := compFiles[dir]
		sort.Strings(files)
		cid := model.CanonicalID(opts.Repo, dir, "component")
		first := files[0]
		facts.RawNodes = append(facts.RawNodes, model.RawNode{
			ID:            cid,
			NodeKind:      model.NodeComponent,
			SourceKind:    model.KindPackageManifest,
			Type:          model.TypeComponent,
			NativeAddress: dir,
			Attributes:    map[string]any{},
			Multiplicity:  model.MultOne,
			Evidence:      []model.Evidence{lines.evidence(opts.Repo, root, first, 1, fileKind(first))},
		})

		type key struct {
			eco  model.Ecosystem
			name string
		}
		uses := map[key]int{} // index into facts.PackageUses
		lockVersion := map[key]string{}
		var dirFiles []string
		for _, m := range manifests {
			if compDir(m) == dir {
				dirFiles = append(dirFiles, m)
			}
		}
		for _, f := range extraFiles {
			if compDir(f) == dir && !containsString(dirFiles, f) {
				dirFiles = append(dirFiles, f)
			}
		}
		sort.SliceStable(dirFiles, func(i, j int) bool {
			// direct manifests first so their evidence wins de-duplication
			li, lj := isLock(dirFiles[i]) || IsSBOMFile(dirFiles[i]), isLock(dirFiles[j]) || IsSBOMFile(dirFiles[j])
			if li != lj {
				return !li
			}
			return dirFiles[i] < dirFiles[j]
		})
		for _, f := range dirFiles {
			lockLike := isLock(f) || IsSBOMFile(f)
			ps := append(append([]Pkg(nil), direct[f]...), extra[f]...)
			for _, p := range ps {
				k := key{p.Ecosystem, p.Name}
				if lockLike && directEco[dir][p.Ecosystem] {
					if p.Version != "" {
						if _, ok := lockVersion[k]; !ok {
							lockVersion[k] = p.Version
						}
					}
					continue
				}
				if i, ok := uses[k]; ok {
					if facts.PackageUses[i].Version == "" && p.Version != "" {
						facts.PackageUses[i].Version = p.Version
					}
					continue
				}
				line := p.Line
				if line < 1 {
					line = FindLine(lines.get(root, f), p.Name)
				}
				uses[k] = len(facts.PackageUses)
				facts.PackageUses = append(facts.PackageUses, model.PackageUse{
					Component: cid,
					Ecosystem: p.Ecosystem,
					Name:      p.Name,
					Version:   p.Version,
					Manifest:  lines.evidence(opts.Repo, root, f, line, fileKind(f)),
				})
			}
		}
		// Resolved lockfile versions replace declared ranges.
		for k, i := range uses {
			if v, ok := lockVersion[k]; ok {
				facts.PackageUses[i].Version = v
			}
		}
	}
	facts.Sort()
	return facts, nil
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
