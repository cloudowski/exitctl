/* exitctl report: interactive dependency graph.
 * Reads the embedded JSON payload (script#data), builds a Cytoscape graph with
 * expandable groups, highlights the precomputed blast radius of a clicked node,
 * and labels migration waves on request. No network requests. */
(function () {
  "use strict";

  var dataEl = document.getElementById("data");
  var container = document.getElementById("cy");
  if (!dataEl || !container || typeof cytoscape !== "function") {
    return;
  }
  var data;
  try {
    data = JSON.parse(dataEl.textContent);
  } catch (e) {
    return;
  }
  var nodes = data.nodes || [];
  var edges = data.edges || [];
  var groups = data.groups || [];
  var side = document.getElementById("graph-side");
  var MAX_LABEL = 34;

  if (nodes.length === 0) {
    container.textContent = "No resources to show.";
    container.className += " noscript";
    return;
  }

  /* ---------- group structure ---------- */

  function groupChain(g) {
    // innermost to outermost, with a depth guard against malformed parents
    var chain = [];
    var guard = 0;
    while (g !== undefined && g !== null && g >= 0 && g < groups.length && guard < 64) {
      chain.push(g);
      g = groups[g].parent;
      guard++;
    }
    return chain;
  }

  var nodeChains = nodes.map(function (n) { return groupChain(n.group); });
  var groupChains = groups.map(function (g, i) { return groupChain(i); });
  var descendants = groups.map(function () { return []; });
  nodeChains.forEach(function (chain, i) {
    chain.forEach(function (g) { descendants[g].push(i); });
  });

  function initialCollapsed() {
    return groups.map(function (g) { return g.kind === "type"; });
  }
  var collapsed = initialCollapsed();

  function groupVisible(g) {
    var chain = groupChains[g];
    for (var k = 1; k < chain.length; k++) {
      if (collapsed[chain[k]]) { return false; }
    }
    return true;
  }

  // rep returns the element ID that currently stands for node i.
  function rep(i) {
    var chain = nodeChains[i];
    for (var k = chain.length - 1; k >= 0; k--) {
      if (collapsed[chain[k]]) { return "g" + chain[k]; }
    }
    return "n" + i;
  }

  /* ---------- colors ---------- */

  var colors = {};
  function readColors() {
    var cs = getComputedStyle(document.documentElement);
    function v(name, fallback) {
      var x = cs.getPropertyValue(name).trim();
      return x || fallback;
    }
    colors = {
      a: v("--grade-a", "#1f9d55"), b: v("--grade-b", "#7cb518"), c: v("--grade-c", "#e0a100"),
      d: v("--grade-d", "#ea6a1f"), e: v("--grade-e", "#d63a3a"), neutral: v("--neutral-node", "#b7bec9"),
      text: v("--text", "#16191f"), text3: v("--text-3", "#7a8391"), surface: v("--surface", "#ffffff"),
      surface2: v("--surface-2", "#f1f3f6"), border: v("--border-strong", "#cfd5dd"), accent: v("--accent", "#2f5bd3")
    };
  }
  readColors();

  function band(risk) {
    if (!risk || risk <= 0) { return "neutral"; }
    var p = risk * 100;
    if (p < 20) { return "a"; }
    if (p < 40) { return "b"; }
    if (p < 60) { return "c"; }
    if (p < 80) { return "d"; }
    return "e";
  }
  var bandName = { a: "A (low)", b: "B", c: "C", d: "D", e: "E (high)", neutral: "no lock-in findings" };

  function short(s) {
    return s.length > MAX_LABEL ? s.slice(0, MAX_LABEL - 3) + "..." : s;
  }

  /* ---------- element building ---------- */

  var showWaves = false;

  function waveText(lo, hi) {
    if (!lo) { return ""; }
    return lo === hi ? "W" + lo : "W" + lo + "-" + hi;
  }

  function buildElements() {
    var els = [];
    var waveOrder = {};

    groups.forEach(function (g, i) {
      if (!groupVisible(i)) { return; }
      var desc = descendants[i];
      var lo = 0, hi = 0, risk = g.max_risk || 0;
      desc.forEach(function (n) {
        var w = nodes[n].wave || 0;
        if (w && (!lo || w < lo)) { lo = w; }
        if (w > hi) { hi = w; }
        if ((nodes[n].risk || 0) > risk) { risk = nodes[n].risk; }
      });
      var d = {
        id: "g" + i, kind: "group", gi: i, collapsed: collapsed[i],
        base: collapsed[i] ? g.label : g.label, risk: risk, band: band(risk),
        wave: waveText(lo, hi), size: 26 + Math.min(desc.length, 30)
      };
      if (g.parent !== undefined && g.parent !== null && g.parent >= 0) { d.parent = "g" + g.parent; }
      els.push({ group: "nodes", data: d, classes: collapsed[i] ? "grp collapsed" : "grp expanded" });
    });

    nodes.forEach(function (n, i) {
      if (rep(i) !== "n" + i) { return; }
      var d = {
        id: "n" + i, kind: "node", ni: i, base: (n.anchor ? "#" + n.anchor + " " : "") + n.label,
        risk: n.risk || 0, band: band(n.risk), wave: waveText(n.wave, n.wave),
        size: 18 + Math.min((n.dependents || []).length, 20) * 1.2
      };
      if (n.group !== undefined && n.group !== null && n.group >= 0) { d.parent = "g" + n.group; }
      var cls = "leaf";
      if (n.anchor) { cls += " anchor"; }
      els.push({ group: "nodes", data: d, classes: cls });
      var w = n.wave || 0;
      waveOrder[w] = (waveOrder[w] || 0) + 1;
    });

    var seen = {};
    edges.forEach(function (e) {
      var s = rep(e[0]), t = rep(e[1]);
      if (s === t) { return; }
      var key = s + ">" + t;
      if (seen[key]) { seen[key].data.count++; return; }
      var el = { group: "edges", data: { id: "e" + key, source: s, target: t, kind: e[2], count: 1 } };
      seen[key] = el;
      els.push(el);
    });

    els.forEach(function (el) {
      if (el.group === "nodes") { el.data.label = label(el.data); }
    });
    return els;
  }

  function label(d) {
    var base = d.collapsed ? d.base : (d.kind === "group" ? d.base : short(d.base));
    if (showWaves && d.wave) { return "[" + d.wave + "] " + base; }
    return base;
  }

  // Seed positions by wave so the force layout starts from a stable,
  // meaningful arrangement (no randomness: the report is deterministic).
  function seedPositions(cy) {
    var rows = {};
    cy.nodes().forEach(function (n) {
      if (n.isParent()) { return; }
      var w = parseInt((n.data("wave") || "W1").slice(1), 10) || 1;
      rows[w] = (rows[w] || 0) + 1;
      n.position({ x: w * 220, y: rows[w] * 90 });
    });
  }

  function style() {
    return [
      { selector: "node", style: {
        "label": "data(label)", "font-family": "system-ui, -apple-system, Segoe UI, Roboto, sans-serif",
        "font-size": 11, "color": colors.text, "text-valign": "bottom", "text-halign": "center",
        "text-margin-y": 5, "text-wrap": "none", "min-zoomed-font-size": 6,
        "width": "data(size)", "height": "data(size)", "border-width": 2, "border-color": colors.surface,
        "background-color": function (n) { return colors[n.data("band")] || colors.neutral; },
        "transition-property": "opacity", "transition-duration": "150ms"
      } },
      { selector: "node.anchor", style: { "border-width": 3, "border-color": colors.text, "font-weight": 700 } },
      { selector: "node.grp.collapsed", style: {
        "shape": "round-rectangle",
        "width": function (n) { return n.data("size") * 1.7; }, "height": "data(size)",
        "font-weight": 600, "border-width": 3, "border-style": "double", "border-color": colors.text3
      } },
      { selector: "node.grp.expanded, :parent", style: {
        "shape": "round-rectangle", "background-color": colors.surface2, "background-opacity": 0.6,
        "border-width": 1, "border-style": "dashed", "border-color": colors.border,
        "text-valign": "top", "text-halign": "center", "text-margin-y": -4,
        "font-size": 11, "font-weight": 600, "color": colors.text3, "padding": 16
      } },
      { selector: "edge", style: {
        "width": function (e) { return Math.min(1.2 + (e.data("count") - 1) * 0.6, 5); },
        "line-color": colors.border, "target-arrow-color": colors.border,
        "target-arrow-shape": "triangle", "arrow-scale": 0.8, "curve-style": "bezier",
        "transition-property": "opacity", "transition-duration": "150ms"
      } },
      { selector: "edge[kind = 'data-flow']", style: { "line-style": "dashed" } },
      { selector: ".faded", style: { "opacity": 0.12 } },
      { selector: "node.focus", style: { "border-width": 4, "border-color": colors.accent, "z-index": 20 } },
      { selector: "node.dep", style: { "border-width": 3, "border-color": colors.accent } },
      { selector: "edge.hl", style: { "line-color": colors.accent, "target-arrow-color": colors.accent, "width": 2.5, "z-index": 10 } }
    ];
  }

  var cy = cytoscape({
    container: container,
    elements: [],
    style: style(),
    minZoom: 0.15,
    maxZoom: 3,
    boxSelectionEnabled: false,
    autoungrabify: false
  });

  function layout() {
    seedPositions(cy);
    cy.layout({
      name: "cose", randomize: false, animate: false, fit: true, padding: 30,
      nodeDimensionsIncludeLabels: true, nodeRepulsion: function () { return 9000; },
      idealEdgeLength: function () { return 80; }, nestingFactor: 1.1, gravity: 0.6,
      numIter: 1500, componentSpacing: 80
    }).run();
  }

  function rebuild() {
    cy.batch(function () {
      cy.elements().remove();
      cy.add(buildElements());
    });
    layout();
    selected = null;
    renderSide(null);
  }

  /* ---------- highlight and side panel ---------- */

  var selected = null;

  function clearHighlight() {
    cy.elements().removeClass("faded focus dep hl");
  }

  function blast(el) {
    // dependents of the clicked element, mapped to what is currently visible
    var members = el.data("kind") === "group" ? descendants[el.data("gi")] : [el.data("ni")];
    var out = {};
    var own = {};
    members.forEach(function (m) { own[m] = true; });
    members.forEach(function (m) {
      (nodes[m].dependents || []).forEach(function (d) {
        if (!own[d]) { out[d] = true; }
      });
    });
    return Object.keys(out).map(Number).sort(function (a, b) { return a - b; });
  }

  function highlight(el) {
    clearHighlight();
    var deps = blast(el);
    var keep = cy.collection().union(el);
    deps.forEach(function (d) {
      var r = cy.getElementById(rep(d));
      if (r.nonempty() && r.id() !== el.id()) { keep = keep.union(r); r.addClass("dep"); }
    });
    var withParents = keep.union(keep.ancestors()).union(el.descendants());
    var hlEdges = keep.connectedEdges().filter(function (e) {
      return keep.contains(e.source()) && keep.contains(e.target());
    });
    hlEdges.addClass("hl");
    cy.elements().not(withParents).not(hlEdges).addClass("faded");
    el.addClass("focus");
    renderSide(el, deps);
  }

  function addRow(dl, k, v) {
    var dt = document.createElement("dt");
    dt.textContent = k;
    var dd = document.createElement("dd");
    dd.textContent = v;
    dl.appendChild(dt);
    dl.appendChild(dd);
  }

  function renderSide(el, deps) {
    if (!side) { return; }
    side.textContent = "";
    var h = document.createElement("h4");
    if (!el) {
      h.textContent = "Blast radius";
      side.appendChild(h);
      var p = document.createElement("p");
      p.className = "muted small";
      p.textContent = "Click a resource to highlight everything that depends on it. Click a group to expand or collapse it. Arrows point from a resource to what it depends on.";
      side.appendChild(p);
      return;
    }
    h.textContent = el.data("kind") === "group" ? "Group" : "Resource";
    side.appendChild(h);
    var t = document.createElement("div");
    t.className = "sel";
    var dl = document.createElement("dl");
    if (el.data("kind") === "group") {
      var g = groups[el.data("gi")];
      t.textContent = g.label;
      addRow(dl, "Kind", g.kind);
      addRow(dl, "Resources", String(descendants[el.data("gi")].length));
      addRow(dl, "State", collapsed[el.data("gi")] ? "collapsed" : "expanded");
    } else {
      var n = nodes[el.data("ni")];
      t.textContent = n.label;
      if (n.anchor) { addRow(dl, "Anchor", "#" + n.anchor); }
      addRow(dl, "Type", n.type || "-");
      if (n.service) { addRow(dl, "Service", n.service); }
    }
    var b = el.data("band");
    addRow(dl, "Risk", b === "neutral" ? "no lock-in findings" : Math.round(el.data("risk") * 100) + "/100, band " + bandName[b]);
    if (el.data("wave")) { addRow(dl, "Wave", el.data("wave").slice(1)); }
    addRow(dl, "Dependents", String(deps.length));
    side.appendChild(t);
    side.appendChild(dl);
    if (deps.length) {
      var h2 = document.createElement("h4");
      h2.textContent = "Depends on it";
      side.appendChild(h2);
      var ul = document.createElement("ul");
      ul.className = "plain";
      var max = 25;
      deps.slice(0, max).forEach(function (d) {
        var li = document.createElement("li");
        li.textContent = nodes[d].label;
        ul.appendChild(li);
      });
      if (deps.length > max) {
        var more = document.createElement("li");
        more.textContent = "... " + (deps.length - max) + " more";
        ul.appendChild(more);
      }
      side.appendChild(ul);
    }
  }

  cy.on("tap", "node", function (evt) {
    var el = evt.target;
    if (el.data("kind") === "group") {
      var gi = el.data("gi");
      collapsed[gi] = !collapsed[gi];
      rebuild();
      var again = cy.getElementById("g" + gi);
      if (again.nonempty()) { selected = again; highlight(again); }
      return;
    }
    if (selected && selected.id() === el.id()) {
      selected = null;
      clearHighlight();
      renderSide(null);
      return;
    }
    selected = el;
    highlight(el);
  });

  cy.on("tap", function (evt) {
    if (evt.target === cy) {
      selected = null;
      clearHighlight();
      renderSide(null);
    }
  });

  /* ---------- toolbar ---------- */

  function bind(id, fn) {
    var b = document.getElementById(id);
    if (b) { b.addEventListener("click", function () { fn(b); }); }
    return b;
  }

  function relabel() {
    cy.nodes().forEach(function (n) { n.data("label", label(n.data())); });
  }

  bind("btn-fit", function () { cy.fit(undefined, 30); });
  bind("btn-reset", function () {
    collapsed = initialCollapsed();
    showWaves = false;
    var wb = document.getElementById("btn-waves");
    if (wb) { wb.setAttribute("aria-pressed", "false"); }
    rebuild();
  });
  bind("btn-waves", function (b) {
    showWaves = !showWaves;
    b.setAttribute("aria-pressed", showWaves ? "true" : "false");
    relabel();
  });
  var expandBtn = bind("btn-expand", function (b) {
    var anyCollapsed = collapsed.some(function (c) { return c; });
    collapsed = collapsed.map(function () { return !anyCollapsed; });
    b.textContent = anyCollapsed ? "Collapse groups" : "Expand groups";
    rebuild();
  });
  if (expandBtn && groups.length === 0) { expandBtn.hidden = true; }

  if (window.matchMedia) {
    var mq = window.matchMedia("(prefers-color-scheme: dark)");
    var onScheme = function () { readColors(); cy.style(style()); };
    if (mq.addEventListener) { mq.addEventListener("change", onScheme); }
    else if (mq.addListener) { mq.addListener(onScheme); }
  }

  rebuild();
})();
