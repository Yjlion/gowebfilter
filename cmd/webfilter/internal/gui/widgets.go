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

func noticeText(fn func() string) *primitives.TextWidget {
	return primitives.TextFn(fn).FontSize(13).Color(widget.RGBA8(56, 107, 1, 255))
}
