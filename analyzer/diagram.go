package analyzer

import (
	"fmt"
	"math"
	"strings"

	"github.com/kashari/yggdrasil/model"
)

// DiagramHTML produces a self-contained HTML page containing an SVG diagram
// of the workflow definition. States are laid out in layers (BFS depth),
// with child-spawn actions rendered as fork annotations.
func DiagramHTML(def *model.WorkflowDefinition) string {
	svg := buildSVG(def)
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8"/>
  <meta name="viewport" content="width=device-width, initial-scale=1.0"/>
  <title>Workflow Diagram — %s</title>
  <style>
    * { box-sizing: border-box; margin: 0; padding: 0; }
    body {
      background: #0f1117;
      color: #e2e8f0;
      font-family: 'Segoe UI', system-ui, sans-serif;
      min-height: 100vh;
      display: flex;
      flex-direction: column;
      align-items: center;
      padding: 32px 16px;
    }
    h1 {
      font-size: 1.5rem;
      font-weight: 600;
      margin-bottom: 8px;
      color: #f8fafc;
      letter-spacing: 0.03em;
    }
    .subtitle {
      font-size: 0.85rem;
      color: #94a3b8;
      margin-bottom: 32px;
    }
    .diagram-wrap {
      background: #1e2433;
      border: 1px solid #2d3748;
      border-radius: 16px;
      padding: 32px;
      overflow-x: auto;
      max-width: 100%%;
      box-shadow: 0 8px 32px rgba(0,0,0,0.4);
    }
    .legend {
      display: flex;
      gap: 24px;
      margin-top: 24px;
      flex-wrap: wrap;
      justify-content: center;
      font-size: 0.8rem;
      color: #94a3b8;
    }
    .legend-item { display: flex; align-items: center; gap: 8px; }
    .legend-dot {
      width: 14px; height: 14px; border-radius: 3px;
    }
  </style>
</head>
<body>
  <h1>%s</h1>
  <div class="subtitle">Workflow Definition Diagram &nbsp;·&nbsp; %d states &nbsp;·&nbsp; %d transitions</div>
  <div class="diagram-wrap">%s</div>
  <div class="legend">
    <div class="legend-item"><div class="legend-dot" style="background:#3b82f6"></div>Initial state</div>
    <div class="legend-item"><div class="legend-dot" style="background:#6366f1"></div>Intermediate</div>
    <div class="legend-item"><div class="legend-dot" style="background:#10b981"></div>Terminal</div>
    <div class="legend-item"><div class="legend-dot" style="background:#f59e0b;border-radius:50%%"></div>Child spawn</div>
  </div>
</body>
</html>`, def.ID, def.ID, len(def.States), len(def.Transitions), svg)
}

const (
	nodeW    = 180
	nodeH    = 52
	nodeRx   = 10
	layerGap = 130 // vertical gap between layers
	nodeGap  = 210 // horizontal gap between nodes in same layer
	padX     = 60
	padY     = 60
)

type point struct{ x, y float64 }

type nodeLayout struct {
	state  model.StateDefinition
	cx, cy float64 // centre
	layer  int
}

func buildSVG(def *model.WorkflowDefinition) string {
	// BFS layers
	stateMap := make(map[string]model.StateDefinition)
	for _, s := range def.States {
		stateMap[s.StateID] = s
	}

	transFrom := make(map[string][]model.TransitionDefinition)
	for _, t := range def.Transitions {
		if !(t.IsCommon && t.Source == "*") {
			transFrom[t.Source] = append(transFrom[t.Source], t)
		}
	}

	depth := make(map[string]int)
	queue := []string{def.InitialState}
	depth[def.InitialState] = 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, t := range transFrom[cur] {
			if _, seen := depth[t.Target]; !seen {
				depth[t.Target] = depth[cur] + 1
				queue = append(queue, t.Target)
			}
		}
	}

	maxDepth := 0
	for _, d := range depth {
		if d > maxDepth {
			maxDepth = d
		}
	}
	for _, s := range def.States {
		if _, ok := depth[s.StateID]; !ok {
			depth[s.StateID] = maxDepth + 1
		}
	}

	layers := make(map[int][]string)
	for id, d := range depth {
		layers[d] = append(layers[d], id)
	}
	numLayers := maxDepth + 2 // +2 for unvisited overflow layer

	maxPerLayer := 1
	for _, ids := range layers {
		if len(ids) > maxPerLayer {
			maxPerLayer = len(ids)
		}
	}
	svgW := float64(padX*2 + maxPerLayer*nodeW + (maxPerLayer-1)*nodeGap + 60)
	svgH := float64(padY*2 + numLayers*nodeH + (numLayers-1)*layerGap + 60)

	nodes := make(map[string]*nodeLayout)
	for d, ids := range layers {
		count := len(ids)
		totalW := float64(count*nodeW + (count-1)*nodeGap)
		startX := (svgW - totalW) / 2.0
		for i, id := range ids {
			cx := startX + float64(i)*(float64(nodeW)+float64(nodeGap)) + float64(nodeW)/2.0
			cy := float64(padY) + float64(d)*(float64(nodeH)+float64(layerGap)) + float64(nodeH)/2.0
			nodes[id] = &nodeLayout{
				state: stateMap[id],
				cx:    cx, cy: cy,
				layer: d,
			}
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" width="%.0f" height="%.0f" viewBox="0 0 %.0f %.0f">`,
		svgW, svgH, svgW, svgH,
	))

	sb.WriteString(`
  <defs>
    <marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5"
            markerWidth="6" markerHeight="6" orient="auto-start-reverse">
      <path d="M0,0 L10,5 L0,10 z" fill="#64748b"/>
    </marker>
    <marker id="arrow-child" viewBox="0 0 10 10" refX="9" refY="5"
            markerWidth="6" markerHeight="6" orient="auto-start-reverse">
      <path d="M0,0 L10,5 L0,10 z" fill="#f59e0b"/>
    </marker>
    <filter id="glow" x="-30%" y="-30%" width="160%" height="160%">
      <feGaussianBlur in="SourceGraphic" stdDeviation="3" result="blur"/>
      <feMerge><feMergeNode in="blur"/><feMergeNode in="SourceGraphic"/></feMerge>
    </filter>
  </defs>`)

	for _, t := range def.Transitions {
		if t.IsCommon && t.Source == "*" {
			continue // drawn separately
		}
		src, ok1 := nodes[t.Source]
		tgt, ok2 := nodes[t.Target]
		if !ok1 || !ok2 {
			continue
		}

		isChildSpawn := false
		for _, a := range t.Actions {
			if a.Type == model.ActionTypeStartChild {
				isChildSpawn = true
			}
		}

		color := "#475569"
		marker := "url(#arrow)"
		if isChildSpawn {
			color = "#f59e0b"
			marker = "url(#arrow-child)"
		}

		if t.Source == t.Target {
			drawSelfLoop(&sb, src.cx, src.cy, t.Event, color, marker)
			continue
		}

		if tgt.layer <= src.layer && t.Source != def.InitialState {
			drawCurvedEdge(&sb, src, tgt, t.Event, color, marker, true)
			continue
		}

		drawStraightEdge(&sb, src, tgt, t.Event, color, marker)
	}

	var commonTrans []model.TransitionDefinition
	for _, t := range def.Transitions {
		if t.IsCommon && t.Source == "*" {
			commonTrans = append(commonTrans, t)
		}
	}
	if len(commonTrans) > 0 {
		bx := svgW - 10
		by := float64(padY)
		for i, t := range commonTrans {
			drawCommonBadge(&sb, bx, by+float64(i)*36, t.Event, t.Target)
		}
	}

	for _, n := range nodes {
		drawNode(&sb, n, def.InitialState)
	}

	sb.WriteString(`</svg>`)
	return sb.String()
}

func nodeColor(n *nodeLayout, initialState string) (fill, stroke string) {
	if n.state.StateID == initialState {
		return "#1d4ed8", "#3b82f6"
	}
	if n.state.IsEndState {
		return "#065f46", "#10b981"
	}
	return "#312e81", "#6366f1"
}

func drawNode(sb *strings.Builder, n *nodeLayout, initialState string) {
	fill, stroke := nodeColor(n, initialState)
	x := n.cx - float64(nodeW)/2
	y := n.cy - float64(nodeH)/2

	hasChild := false
	for _, a := range n.state.EntryActions {
		if a.Type == model.ActionTypeStartChild {
			hasChild = true
		}
	}

	fmt.Fprintf(sb,
		`<rect x="%.1f" y="%.1f" width="%d" height="%d" rx="%d" fill="rgba(0,0,0,0.3)"/>`,
		x+3, y+3, nodeW, nodeH, nodeRx)

	fmt.Fprintf(sb,
		`<rect x="%.1f" y="%.1f" width="%d" height="%d" rx="%d" fill="%s" stroke="%s" stroke-width="1.5"/>`,
		x, y, nodeW, nodeH, nodeRx, fill, stroke)

	if hasChild {
		fmt.Fprintf(sb,
			`<circle cx="%.1f" cy="%.1f" r="7" fill="#f59e0b" filter="url(#glow)"/>`,
			x+float64(nodeW)-10, y+10)
	}

	label := n.state.StateID
	lines := wrapText(label, 22)
	lineH := 15.0
	startY := n.cy - float64(len(lines)-1)*lineH/2.0
	for i, line := range lines {
		fmt.Fprintf(sb,
			`<text x="%.1f" y="%.1f" text-anchor="middle" font-family="'Segoe UI',sans-serif" font-size="11" font-weight="600" fill="#f8fafc">%s</text>`,
			n.cx, startY+float64(i)*lineH, escapeXML(line))
	}

	if n.state.IsEndState {
		fmt.Fprintf(sb,
			`<text x="%.1f" y="%.1f" text-anchor="middle" font-family="sans-serif" font-size="9" fill="#6ee7b7">✓ terminal</text>`,
			n.cx, y+float64(nodeH)-5)
	}
}

func drawStraightEdge(sb *strings.Builder, src, tgt *nodeLayout, label, color, marker string) {
	sx, sy := edgeExit(src, tgt)
	tx, ty := edgeEntry(tgt, src)

	mx := (sx + tx) / 2
	my := (sy + ty) / 2

	fmt.Fprintf(sb,
		`<path d="M%.1f,%.1f Q%.1f,%.1f %.1f,%.1f" fill="none" stroke="%s" stroke-width="1.5" marker-end="%s"/>`,
		sx, sy, mx, my, tx, ty, color, marker)

	drawEdgeLabel(sb, mx, my-10, label, color)
}

func drawCurvedEdge(sb *strings.Builder, src, tgt *nodeLayout, label, color, marker string, isBack bool) {
	sx := src.cx + float64(nodeW)/2 + 15
	sy := src.cy
	tx := tgt.cx + float64(nodeW)/2 + 15
	ty := tgt.cy
	cx := math.Max(sx, tx) + 60

	fmt.Fprintf(sb,
		`<path d="M%.1f,%.1f C%.1f,%.1f %.1f,%.1f %.1f,%.1f" fill="none" stroke="%s" stroke-width="1.5" stroke-dasharray="5,3" marker-end="%s"/>`,
		sx, sy, cx, sy, cx, ty, tx, ty, color, marker)
	drawEdgeLabel(sb, cx+5, (sy+ty)/2, label, color)
}

func drawSelfLoop(sb *strings.Builder, cx, cy float64, label, color, marker string) {
	x := cx + float64(nodeW)/2
	sb.WriteString(fmt.Sprintf(
		`<path d="M%.1f,%.1f C%.1f,%.1f %.1f,%.1f %.1f,%.1f" fill="none" stroke="%s" stroke-width="1.5" stroke-dasharray="4,3" marker-end="%s"/>`,
		x, cy-10, x+50, cy-40, x+50, cy+40, x, cy+10, color, marker,
	))
	drawEdgeLabel(sb, x+55, cy, label, color)
}

func drawCommonBadge(sb *strings.Builder, rightX, y float64, event, target string) {
	w := 190.0
	x := rightX - w
	fmt.Fprintf(sb,
		`<rect x="%.1f" y="%.1f" width="%.0f" height="28" rx="14" fill="#292524" stroke="#78716c" stroke-width="1"/>`,
		x, y, w)
	fmt.Fprintf(sb,
		`<text x="%.1f" y="%.1f" text-anchor="middle" font-family="sans-serif" font-size="10" fill="#d6d3d1">⚡ %s → %s</text>`,
		x+w/2, y+18, escapeXML(event), escapeXML(target))
}

func drawEdgeLabel(sb *strings.Builder, x, y float64, label, color string) {
	if label == "" {
		return
	}
	fmt.Fprintf(sb,
		`<text x="%.1f" y="%.1f" text-anchor="middle" font-family="'Segoe UI',sans-serif" font-size="10" fill="%s">%s</text>`,
		x, y, color, escapeXML(label))
}

func edgeExit(src, tgt *nodeLayout) (float64, float64) {
	return clampToBorder(src.cx, src.cy, tgt.cx, tgt.cy, true)
}

func edgeEntry(tgt, src *nodeLayout) (float64, float64) {
	return clampToBorder(tgt.cx, tgt.cy, src.cx, src.cy, false)
}

func clampToBorder(ox, oy, tx, ty float64, exit bool) (float64, float64) {
	hw := float64(nodeW) / 2.0
	hh := float64(nodeH) / 2.0

	dx := tx - ox
	dy := ty - oy
	if dx == 0 && dy == 0 {
		return ox, oy + hh
	}

	scaleX, scaleY := math.Inf(1), math.Inf(1)
	if dx != 0 {
		scaleX = hw / math.Abs(dx)
	}
	if dy != 0 {
		scaleY = hh / math.Abs(dy)
	}
	scale := math.Min(scaleX, scaleY)
	_ = exit
	return ox + dx*scale, oy + dy*scale
}

func wrapText(s string, maxLen int) []string {
	if len(s) <= maxLen {
		return []string{s}
	}

	parts := strings.Split(s, "_")
	var lines []string
	cur := ""
	for _, p := range parts {
		add := p
		if cur != "" {
			add = "_" + p
		}
		if len(cur)+len(add) > maxLen && cur != "" {
			lines = append(lines, cur)
			cur = p
		} else {
			cur += add
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
