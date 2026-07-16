package gui

import (
	"github.com/gogpu/ui/a11y"
	"github.com/gogpu/ui/core/button"
	"github.com/gogpu/ui/event"
	"github.com/gogpu/ui/geometry"
	"github.com/gogpu/ui/primitives"
	"github.com/gogpu/ui/theme/material3"
	"github.com/gogpu/ui/widget"
)

// scrollBox is a minimal vertical scroll container. core/scrollview can't be
// used: once its content overflows it self-invalidates every frame (~100fps,
// pegging the CPU on an idle window) in gogpu/ui v0.1.44. This one only
// requests a frame when the wheel actually moves, so an idle window stays at
// 0fps. It clips to its viewport, translates content by the scroll offset,
// and forwards translated mouse events so clickable rows/fields still work.
type scrollBox struct {
	widget.WidgetBase
	child    widget.Widget
	scrollY  float32
	viewport geometry.Size
	contentH float32
}

func newScrollBox(child widget.Widget) *scrollBox {
	s := &scrollBox{child: child}
	s.SetVisible(true)
	s.SetEnabled(true)
	if ps, ok := child.(interface{ SetParent(widget.Widget) }); ok {
		ps.SetParent(s)
	}
	return s
}

func (s *scrollBox) maxScroll() float32 {
	if s.contentH <= s.viewport.Height {
		return 0
	}
	return s.contentH - s.viewport.Height
}

func (s *scrollBox) Layout(ctx widget.Context, constraints geometry.Constraints) geometry.Size {
	s.viewport = constraints.Biggest()
	if s.viewport.Width <= 0 || s.viewport.Height <= 0 {
		s.viewport = constraints.Constrain(geometry.Sz(200, 200))
	}
	if s.child != nil {
		cc := geometry.Constraints{
			MinWidth: s.viewport.Width, MaxWidth: s.viewport.Width,
			MinHeight: 0, MaxHeight: geometry.Infinity,
		}
		size := widget.LayoutChild(s.child, ctx, cc)
		s.contentH = size.Height
		if sb, ok := s.child.(interface{ SetBounds(geometry.Rect) }); ok {
			sb.SetBounds(geometry.NewRect(0, 0, size.Width, size.Height))
		}
	}
	if s.scrollY > s.maxScroll() {
		s.scrollY = s.maxScroll()
	}
	if s.scrollY < 0 {
		s.scrollY = 0
	}
	s.SetBounds(geometry.FromPointSize(s.Position(), s.viewport))
	return s.viewport
}

func (s *scrollBox) Draw(ctx widget.Context, canvas widget.Canvas) {
	if !s.IsVisible() || s.child == nil {
		return
	}
	b := s.Bounds()
	canvas.PushClip(b)
	canvas.PushTransform(geometry.Pt(b.Min.X, b.Min.Y-s.scrollY))
	widget.StampScreenOrigin(s.child, canvas)
	s.child.Draw(ctx, canvas)
	canvas.PopTransform()
	canvas.PopClip()

	// Thin scroll indicator on the right edge when content overflows.
	if max := s.maxScroll(); max > 0 {
		trackH := b.Height()
		thumbH := trackH * b.Height() / s.contentH
		if thumbH < 24 {
			thumbH = 24
		}
		thumbY := b.Min.Y + (trackH-thumbH)*(s.scrollY/max)
		canvas.DrawRoundRect(geometry.NewRect(b.Max.X-7, thumbY, 4, thumbH), widget.RGBA8(170, 170, 180, 255), 2)
	}
}

func (s *scrollBox) Event(ctx widget.Context, ev event.Event) bool {
	if !s.IsVisible() || !s.IsEnabled() || s.child == nil {
		return false
	}
	b := s.Bounds()
	if we, ok := ev.(*event.WheelEvent); ok {
		if s.maxScroll() <= 0 {
			return false
		}
		// gogpu/ui delivers a positive Delta.Y for a downward wheel notch
		// (see gogpu platform_windows.go: DeltaY = -rawWheelDelta, and raw is
		// negative when scrolling down). Scrolling down must advance scrollY so
		// Draw's translate (b.Min.Y - scrollY) moves content up and reveals the
		// rows below. A `-=` here pins scrollY at 0 in the common downward
		// direction (the clamp below immediately floors it), which reads as
		// "scrolling does nothing".
		s.scrollY += we.Delta.Y * 40
		if s.scrollY < 0 {
			s.scrollY = 0
		}
		if s.scrollY > s.maxScroll() {
			s.scrollY = s.maxScroll()
		}
		s.SetNeedsRedraw(true)
		ctx.InvalidateRect(b)
		return true
	}
	if me, ok := ev.(*event.MouseEvent); ok {
		local := *me
		local.Position = me.Position.Sub(b.Min).Add(geometry.Pt(0, s.scrollY))
		return s.child.Event(ctx, &local)
	}
	return s.child.Event(ctx, ev)
}

func (s *scrollBox) Children() []widget.Widget {
	if s.child == nil {
		return nil
	}
	return []widget.Widget{s.child}
}

func (s *scrollBox) AccessibilityRole() a11y.Role { return a11y.RoleGroup }

// swapWidget is a single-child container whose child can be replaced at
// runtime - gogpu/ui's Box has a fixed child list, so screens that show
// different content per selection (the policy editor) mount one of these and
// call SetChild. Modeled on primitives.ExpandedWidget's delegation contract.
type swapWidget struct {
	widget.WidgetBase
	child widget.Widget
}

func newSwap(initial widget.Widget) *swapWidget {
	s := &swapWidget{child: initial}
	s.SetVisible(true)
	s.SetEnabled(true)
	s.adopt(initial)
	return s
}

func (s *swapWidget) adopt(child widget.Widget) {
	if child == nil {
		return
	}
	type parentSetter interface{ SetParent(widget.Widget) }
	if ps, ok := child.(parentSetter); ok {
		ps.SetParent(s)
	}
}

// SetChild replaces the content. Call from the UI thread (event handlers) or
// followed by a redraw request.
func (s *swapWidget) SetChild(child widget.Widget) {
	s.child = child
	s.adopt(child)
	s.MarkNeedsLayout()
	s.SetNeedsRedraw(true)
}

func (s *swapWidget) Layout(ctx widget.Context, constraints geometry.Constraints) geometry.Size {
	if s.child == nil {
		size := constraints.Constrain(geometry.Size{})
		s.SetBounds(geometry.FromPointSize(s.Position(), size))
		return size
	}
	size := widget.LayoutChild(s.child, ctx, constraints)
	if sb, ok := s.child.(interface{ SetBounds(geometry.Rect) }); ok {
		sb.SetBounds(geometry.FromPointSize(geometry.Point{}, size))
	}
	s.SetBounds(geometry.FromPointSize(s.Position(), size))
	return size
}

func (s *swapWidget) Draw(ctx widget.Context, canvas widget.Canvas) {
	if !s.IsVisible() || s.child == nil {
		return
	}
	bounds := s.Bounds()
	canvas.PushTransform(bounds.Min)
	widget.StampScreenOrigin(s.child, canvas)
	s.child.Draw(ctx, canvas)
	canvas.PopTransform()
}

func (s *swapWidget) Event(ctx widget.Context, ev event.Event) bool {
	if !s.IsVisible() || !s.IsEnabled() || s.child == nil {
		return false
	}
	if me, ok := ev.(*event.MouseEvent); ok {
		local := *me
		local.Position = me.Position.Sub(s.Bounds().Min)
		return s.child.Event(ctx, &local)
	}
	if we, ok := ev.(*event.WheelEvent); ok {
		local := *we
		local.Position = we.Position.Sub(s.Bounds().Min)
		return s.child.Event(ctx, &local)
	}
	return s.child.Event(ctx, ev)
}

func (s *swapWidget) Children() []widget.Widget {
	if s.child == nil {
		return nil
	}
	return []widget.Widget{s.child}
}

func (s *swapWidget) AccessibilityRole() a11y.Role { return a11y.RoleGroup }

// ---- shared styling helpers ----

func (u *ui) btn(label string, onClick func()) *button.Widget {
	return button.New(
		button.TextOpt(label),
		button.OnClick(onClick),
		button.PainterOpt(material3.ButtonPainter{Theme: u.m3}),
	)
}

func (u *ui) btnOutlined(label string, onClick func()) *button.Widget {
	return button.New(
		button.TextOpt(label),
		button.OnClick(onClick),
		button.VariantOpt(button.Outlined),
		button.PainterOpt(material3.ButtonPainter{Theme: u.m3}),
	)
}

func sectionTitle(s string) *primitives.TextWidget {
	return primitives.Text(s).FontSize(16).Bold()
}

func fieldLabel(s string) *primitives.TextWidget {
	return primitives.Text(s).FontSize(12).Color(widget.RGBA8(90, 90, 100, 255))
}

func errorText(fn func() string) *primitives.TextWidget {
	return primitives.TextFn(fn).FontSize(13).Color(widget.RGBA8(179, 38, 30, 255))
}

// col pins a log-row cell to a fixed width so rows line up like a table.
func col(w float32, t widget.Widget) widget.Widget {
	return primitives.Box(t).Width(w)
}

// logRowWidget renders one normalized log row with aligned columns; shared
// by the logs screen and the dashboard's recent-blocks list.
func logRowWidget(time, client, action, target, detail string) widget.Widget {
	muted := widget.RGBA8(90, 90, 100, 255)
	return primitives.HBox(
		col(120, primitives.Text(time).FontSize(12).Color(muted)),
		col(110, primitives.Text(client).FontSize(12)),
		col(70, primitives.Text(action).FontSize(12).Bold()),
		col(260, primitives.Text(target).FontSize(12).MaxLines(1).Ellipsis()),
		primitives.Text(detail).FontSize(12).Color(muted).MaxLines(1).Ellipsis(),
	).Gap(12).PaddingXY(8, 4).CrossAlign(primitives.CrossAxisCenter)
}

// scrollList wraps a set of pre-built row widgets in a scrolling column.
//
// This deliberately avoids core/listview: its virtualization makes it a
// repaint boundary with a cached GPU texture, which under gogpu/ui v0.1.44
// mis-renders in the direct DrawTo render loop this GUI uses (rows vanish, or
// a stale texture bleeds across tab switches). A plain VBox of ordinary
// widgets renders correctly and, for a management UI capped at a few hundred
// rows, is cheap. Rows past maxListRows are dropped with a trailing note;
// the full history is one click away in the web UI.
const maxListRows = 300

func scrollList(rows []widget.Widget) widget.Widget {
	truncated := false
	if len(rows) > maxListRows {
		rows = rows[:maxListRows]
		truncated = true
	}
	if truncated {
		rows = append(rows, fieldLabel("… more rows — open the Web UI for the full list."))
	}
	if len(rows) == 0 {
		rows = append(rows, fieldLabel("Nothing to show yet."))
	}
	return newScrollBox(primitives.VBox(rows...).Gap(1))
}

func noticeText(fn func() string) *primitives.TextWidget {
	return primitives.TextFn(fn).FontSize(13).Color(widget.RGBA8(56, 107, 1, 255))
}
