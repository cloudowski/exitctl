package source

import (
	"path"
	"sort"
	"strings"

	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/walk"
)

// ComponentFiles is one application component with the code files it owns.
type ComponentFiles struct {
	Component string // component node ID
	Dir       string // repo-relative component directory ("" for the root)
	Packages  []model.PackageUse
	Files     []string // repo-relative code files, sorted
}

// ComponentDir extracts the directory from a component ID
// "<repo>//<dir>#<address>" ("" for the root). ok is false for IDs of
// another repository.
func ComponentDir(repo, id string) (string, bool) {
	prefix := repo + "//"
	if !strings.HasPrefix(id, prefix) {
		return "", false
	}
	rest := id[len(prefix):]
	i := strings.LastIndex(rest, "#")
	if i < 0 {
		return "", false
	}
	return model.NormalizeDir(rest[:i]), true
}

// owner returns the deepest component directory containing file.
func owner(file string, dirs []string) (string, bool) {
	best, found := "", false
	for _, d := range dirs {
		if d == "" || strings.HasPrefix(file, d+"/") {
			if !found || len(d) > len(best) {
				best, found = d, true
			}
		}
	}
	return best, found
}

// CodeComponents groups the code files of the walk by the component that
// owns them (the deepest directory holding a dependency manifest) and
// returns every component that has packages, sorted by component ID. It is
// the shared input of the detectors that read source code.
func CodeComponents(idx walk.Index, opts Options) []ComponentFiles {
	byComp := map[string][]model.PackageUse{}
	compDir := map[string]string{}
	dirSet := map[string]bool{}
	for _, pu := range opts.Packages {
		dir, ok := ComponentDir(opts.Repo, pu.Component)
		if !ok {
			continue
		}
		byComp[pu.Component] = append(byComp[pu.Component], pu)
		compDir[pu.Component] = dir
		dirSet[dir] = true
	}
	// Every manifest directory counts for nested-component exclusion.
	for _, m := range idx.Files[model.KindPackageManifest] {
		dirSet[model.NormalizeDir(path.Dir(m))] = true
	}
	allDirs := make([]string, 0, len(dirSet))
	for dir := range dirSet {
		allDirs = append(allDirs, dir)
	}
	sort.Strings(allDirs)

	filesByDir := map[string][]string{}
	for _, f := range idx.Files[model.KindCode] {
		if dir, ok := owner(f, allDirs); ok {
			filesByDir[dir] = append(filesByDir[dir], f)
		}
	}

	comps := make([]string, 0, len(byComp))
	for c := range byComp {
		comps = append(comps, c)
	}
	sort.Strings(comps)
	out := make([]ComponentFiles, 0, len(comps))
	for _, c := range comps {
		out = append(out, ComponentFiles{Component: c, Dir: compDir[c], Packages: byComp[c], Files: filesByDir[compDir[c]]})
	}
	return out
}
