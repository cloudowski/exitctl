// Command genfixture writes a synthetic repository for performance testing
// (T097, SC-001). Dev-only: it is not part of the exitctl binary.
//
//	go run ./tools/genfixture -out /tmp/perf -resources 5000 -files 20000
//
// The output is deterministic for a given set of flags and -seed.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/cloudowski/exitctl/tools/genfixture/fixture"
)

func main() {
	var o fixture.Options
	out := flag.String("out", "", "target directory (required; created if missing)")
	flag.IntVar(&o.Resources, "resources", 5000, "number of Terraform resources")
	flag.IntVar(&o.Modules, "modules", 0, "number of local Terraform modules (0: one per 50 resources)")
	flag.IntVar(&o.SourceFiles, "files", 20000, "number of source code files")
	flag.IntVar(&o.FilesPerComponent, "files-per-component", 100, "source files per application component")
	flag.Float64Var(&o.SDKShare, "sdk-share", 0.2, "fraction of files importing an AWS SDK in SDK-using components")
	flag.Uint64Var(&o.Seed, "seed", 1, "random seed")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "genfixture: -out is required")
		flag.Usage()
		os.Exit(2)
	}
	if entries, err := os.ReadDir(*out); err == nil && len(entries) > 0 {
		fmt.Fprintf(os.Stderr, "genfixture: %s is not empty\n", *out)
		os.Exit(2)
	}
	st, err := fixture.Generate(*out, o)
	if err != nil {
		fmt.Fprintf(os.Stderr, "genfixture: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s: %d resources in %d modules, %d components, %d source files, %d manifests, %d bytes\n",
		*out, st.Resources, st.Modules, st.Components, st.SourceFiles, st.Manifests, st.Bytes)
}
