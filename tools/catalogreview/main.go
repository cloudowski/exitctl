// Command catalogreview is a local, dev-only web tool for maintainers to
// review the exitctl catalog: change entry status (draft, verified,
// deprecated), mark recommended alternatives, remove alternative mappings
// from a service, and hide an alternative everywhere by deprecating it.
//
// It edits the YAML files in place (keeping their layout) and validates the
// whole catalog after every change, rolling the change back when it would
// make the catalog invalid. Review the result with `git diff` and commit it.
//
//	go run ./tools/catalogreview [-catalog catalog] [-reviewer <maintainer>] [-addr 127.0.0.1:7788] [-open]
package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"time"

	"github.com/cloudowski/exitctl/internal/catalog"
)

func main() {
	dir := flag.String("catalog", "catalog", "catalog directory to review")
	reviewer := flag.String("reviewer", "", "your maintainer name, recorded as reviewed_by and approved_by (default: the only maintainer)")
	addr := flag.String("addr", "127.0.0.1:7788", "listen address (loopback only)")
	open := flag.Bool("open", false, "open the review page in the default browser")
	flag.Parse()

	cat, _, err := catalog.Load(os.DirFS(*dir))
	if err != nil {
		log.Fatalf("cannot load catalog %s: %v", *dir, err)
	}
	ms := cat.Manifest.Maintainers
	if *reviewer == "" {
		if len(ms) != 1 {
			log.Fatalf("pass -reviewer (catalog maintainers: %v)", ms)
		}
		*reviewer = ms[0]
	}
	if !slices.Contains(ms, *reviewer) {
		fmt.Fprintf(os.Stderr, "note: %s is not a catalog maintainer %v; recommending alternatives is disabled\n", *reviewer, ms)
	}
	host, _, err := net.SplitHostPort(*addr)
	if err != nil || !net.ParseIP(host).IsLoopback() && host != "localhost" {
		log.Fatalf("-addr must be a loopback address, got %q", *addr)
	}

	tok := make([]byte, 16)
	if _, err := rand.Read(tok); err != nil {
		log.Fatal(err)
	}
	st := &Store{dir: *dir, reviewer: *reviewer, now: time.Now}
	srv := &http.Server{Addr: *addr, Handler: newServer(st, hex.EncodeToString(tok), *addr).routes(),
		ReadHeaderTimeout: 10 * time.Second}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	url := "http://" + *addr + "/"
	fmt.Printf("Reviewing %s as %s\nOpen %s  (Ctrl+C to stop)\n", *dir, *reviewer, url)
	if *open {
		openBrowser(url)
	}
	log.Fatal(srv.Serve(ln))
}

func openBrowser(url string) {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	if err := exec.Command(name, url).Start(); err != nil {
		fmt.Fprintf(os.Stderr, "cannot open a browser: %v\n", err)
	}
}
