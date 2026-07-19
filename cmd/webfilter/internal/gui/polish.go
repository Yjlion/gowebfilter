package gui

import (
	"github.com/gogpu/ui/a11y"
	"github.com/gogpu/ui/event"
	"github.com/gogpu/ui/geometry"
	"github.com/gogpu/ui/primitives"
	"github.com/gogpu/ui/widget"
)

// Shared palette. Kept in one place so every screen reads from the same set of
// values rather than scattering raw RGBA literals (the pre-polish code had the
// same muted gray hard-coded in five files).
var (
	colAccent   = widget.Hex(0x2563EB) // brand blue — also seeds the material3 theme
	colCardBG   = widget.RGBA8(255, 255, 255, 255)
	colBorder   = widget.RGBA8(226, 228, 233, 255)
	colDivider  = widget.RGBA8(233, 234, 238, 255)
	colMuted    = widget.RGBA8(90, 90, 100, 255)
	colStrong   = widget.RGBA8(28, 30, 38, 255)
	colOk       = widget.RGBA8(21, 128, 61, 255)   // green — running / allowed
	colBlocked  = widget.RGBA8(179, 38, 30, 255)   // red — blocked / stopped
	colModified = widget.RGBA8(176, 108, 8, 255)   // amber — modified / changed
	colDotIdle  = widget.RGBA8(148, 150, 158, 255) // gray — unknown / connecting
)

// card wraps content in a white rounded panel with a hairline border. The
// caller's parent must stretch it (CrossAxisStretch) for it to span the
// content width; on its own a Box shrinks to its content.
func card(children ...widget.Widget) *primitives.BoxWidget {
	return primitives.Box(primitives.VBox(children...).Gap(6)).
		Background(colCardBG).
		Rounded(12).
		BorderStyle(1, colBorder).
		Padding(16)
}

// hairline is a 1px full-width divider (needs a stretching parent).
func hairline() widget.Widget {
	return primitives.Box().Background(colDivider).Height(1)
}

// vline is a 1px full-height vertical divider (needs a stretching parent).
func vline() widget.Widget {
	return primitives.Box().Background(colDivider).Width(1)
}

// panel wraps content in a white bordered card sized to at most maxW wide
// (0 = unconstrained). Unlike card, the caller supplies the inner gap so a
// dense form and a loose status block can both use it.
func panel(maxW float32, gap float32, children ...widget.Widget) *primitives.BoxWidget {
	b := primitives.Box(primitives.VBox(children...).Gap(gap)).
		Background(colCardBG).
		Rounded(12).
		BorderStyle(1, colBorder).
		Padding(16)
	if maxW > 0 {
		b.MaxWidthValue(maxW)
	}
	return b
}

// actionColor maps a normalized log/audit action to its accent color.
func actionColor(action string) widget.Color {
	switch action {
	case "blocked", "deleted":
		return colBlocked
	case "modified", "updated", "renamed":
		return colModified
	case "ok", "created":
		return colOk
	default:
		return colStrong
	}
}

// logHeaderRow labels the columns of the shared log-row layout; column widths
// must match logRowWidget so the header lines up with the rows beneath it.
func logHeaderRow() widget.Widget {
	h := func(w float32, s string) widget.Widget {
		return col(w, primitives.Text(s).FontSize(11).Bold().Color(colMuted))
	}
	return primitives.HBox(
		h(120, "Time"),
		h(110, "Client"),
		h(70, "Action"),
		h(260, "Target"),
		primitives.Text("Detail").FontSize(11).Bold().Color(colMuted),
	).Gap(12).PaddingXY(8, 4).CrossAlign(primitives.CrossAxisCenter)
}

// dotWidget draws a small filled status circle whose color comes from a
// callback, so it stays reactive to engine state (running/stopped) without the
// screen rebuilding its widget tree on every poll.
type dotWidget struct {
	widget.WidgetBase
	size  float32
	color func() widget.Color
}

func statusDot(size float32, color func() widget.Color) *dotWidget {
	d := &dotWidget{size: size, color: color}
	d.SetVisible(true)
	d.SetEnabled(true)
	return d
}

func (d *dotWidget) Layout(_ widget.Context, c geometry.Constraints) geometry.Size {
	sz := c.Constrain(geometry.Sz(d.size, d.size))
	d.SetBounds(geometry.FromPointSize(d.Position(), sz))
	return sz
}

func (d *dotWidget) Draw(_ widget.Context, canvas widget.Canvas) {
	if !d.IsVisible() {
		return
	}
	b := d.Bounds()
	r := d.size / 2
	center := geometry.Pt(b.Min.X+r, b.Min.Y+b.Height()/2)
	canvas.DrawCircle(center, r, d.color())
}

func (d *dotWidget) Event(_ widget.Context, _ event.Event) bool { return false }
func (d *dotWidget) Children() []widget.Widget                  { return nil }
func (d *dotWidget) AccessibilityRole() a11y.Role               { return a11y.RoleImage }
