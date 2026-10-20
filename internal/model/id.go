package model

import (
	"bufio"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// CanonicalID builds the stable node ID `<repo>//<dir>#<native-address>`
// (research R16). dir is the directory within the repository; it is
// normalized to '/' separators, cleaned, and never absolute. The repository
// root is the empty dir.
func CanonicalID(repo, dir, nativeAddress string) string {
	return repo + "//" + NormalizeDir(dir) + "#" + nativeAddress
}

// NormalizeDir turns a repo-relative directory into the canonical form used
// in IDs: '/' separators, cleaned, no leading "/" or "./", "" for the root.
func NormalizeDir(dir string) string {
	d := strings.ReplaceAll(dir, "\\", "/")
	if d == "" {
		return ""
	}
	d = path.Clean(d)
	d = strings.TrimLeft(d, "/")
	for strings.HasPrefix(d, "../") {
		d = strings.TrimPrefix(d, "../")
	}
	if d == "." || d == ".." {
		return ""
	}
	return d
}

// ImpliedServiceID returns the ID of an implied node for a catalog service
// that is used (e.g. via SDK) but not declared anywhere.
func ImpliedServiceID(serviceID string) string {
	return "implied//" + serviceID
}

// DetectRepoName returns the repository name: override when set, else the
// normalized `remote "origin"` URL from <root>/.git/config (e.g.
// github.com/acme/shop), else the base name of root.
func DetectRepoName(root, override string) string {
	if o := strings.TrimSpace(override); o != "" {
		return o
	}
	if u := originURL(root); u != "" {
		if n := NormalizeRemoteURL(u); n != "" {
			return n
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	base := filepath.Base(abs)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return "repo"
	}
	return base
}

// NormalizeRemoteURL strips scheme, credentials, port, and a trailing ".git"
// from a git remote URL: "https://user:pw@github.com/acme/shop.git" and
// "git@github.com:acme/shop.git" both become "github.com/acme/shop".
func NormalizeRemoteURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	scp := true
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
		scp = false
	}
	// Split host part from path.
	slash := strings.Index(u, "/")
	host, rest := u, ""
	if slash >= 0 {
		host, rest = u[:slash], u[slash:]
	}
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	if scp {
		// scp-like syntax: [user@]host:path (or a local path).
		if c := strings.Index(host, ":"); c >= 0 {
			rest = "/" + host[c+1:] + rest
			host = host[:c]
		}
	} else if c := strings.LastIndex(host, ":"); c >= 0 && !strings.Contains(host[c:], "]") {
		host = host[:c] // drop port
	}
	rest = strings.TrimRight(rest, "/")
	rest = strings.TrimSuffix(rest, ".git")
	out := strings.Trim(strings.ToLower(host), "/") + "/" + strings.Trim(rest, "/")
	out = strings.Trim(out, "/")
	return path.Clean("/" + out)[1:]
}

// originURL reads the url of remote "origin" from the repository's git
// configuration. It supports .git as a directory or as a "gitdir:" file.
func originURL(root string) string {
	gitPath := filepath.Join(root, ".git")
	fi, err := os.Stat(gitPath)
	if err != nil {
		return ""
	}
	dir := gitPath
	if !fi.IsDir() {
		b, err := os.ReadFile(gitPath)
		if err != nil {
			return ""
		}
		line := strings.TrimSpace(string(b))
		if !strings.HasPrefix(line, "gitdir:") {
			return ""
		}
		dir = strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(root, dir)
		}
		if c, err := os.ReadFile(filepath.Join(dir, "commondir")); err == nil {
			cd := strings.TrimSpace(string(c))
			if !filepath.IsAbs(cd) {
				cd = filepath.Join(dir, cd)
			}
			dir = cd
		}
	}
	f, err := os.Open(filepath.Join(dir, "config"))
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	inOrigin := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			sec := strings.Trim(line, "[]")
			sec = strings.Join(strings.Fields(sec), " ")
			inOrigin = sec == `remote "origin"`
			continue
		}
		if !inOrigin {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(k), "url") {
			continue
		}
		return strings.Trim(strings.TrimSpace(v), `"`)
	}
	return ""
}
