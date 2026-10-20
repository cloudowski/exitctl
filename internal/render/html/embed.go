package html

import _ "embed"

// cytoscapeJS is the vendored Cytoscape.js build (assets/README.md).
//
//go:embed assets/cytoscape.min.js
var cytoscapeJS string

// cytoscapeLicense is the Cytoscape.js MIT license, kept next to the code it
// covers inside every report.
//
//go:embed assets/LICENSE.cytoscape
var cytoscapeLicense string

//go:embed assets/report.css
var reportCSS string

//go:embed assets/report.js
var reportJS string

//go:embed report.tmpl.html
var reportTemplate string
