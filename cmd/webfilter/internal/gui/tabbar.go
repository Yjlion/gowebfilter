package gui

import (
	"github.com/gogpu/ui/a11y"
	"github.com/gogpu/ui/event"
	"github.com/gogpu/ui/geometry"
	"github.com/gogpu/ui/icon"
	"github.com/gogpu/ui/state"
	"github.com/gogpu/ui/widget"
)

// tabBar is a custom icon+label tab strip. core/tabview can't render icons —
// its Tab is a bare label string — so this widget draws each tab itself
// (icon.Draw + canvas.DrawText) and swaps the content through the swapWidget
// held by buildRoot. Like the other custom widgets here it is stateless per
// frame: item hit-rects are recomputed on every Draw, which is correct under
// the full-repaint render loop and the offscreen snapshot renderer alike.
type tabBar struct {
	widget.WidgetBase
	items    []tabBarItem
	selected state.Signal[int]
	onSelect func(int)
	rects    []geometry.Rect // per-item hit rects in Bounds() space, filled by Draw
}

type tabBarItem struct {
	icon  icon.IconData
	label string
}

const (
	tabBarHeight   float32 = 44
	tabBarFontSize float32 = 14
	tabBarIconSize float32 = 17
	tabBarPadX     float32 = 14 // inner left/right padding of one item
	tabBarIconGap  float32 = 7  // between icon and label
)

func newTabBar(items []tabBarItem, selected state.Signal[int], onSelect func(int)) *tabBar {
	t := &tabBar{items: items, selected: selected, onSelect: onSelect}
	t.SetVisible(true)
	t.SetEnabled(true)
	return t
}

func (t *tabBar) Layout(_ widget.Context, c geometry.Constraints) geometry.Size {
	size := c.Constrain(geometry.Sz(c.MaxWidth, tabBarHeight))
	t.SetBounds(geometry.FromPointSize(t.Position(), size))
	return size
}

func (t *tabBar) Draw(_ widget.Context, canvas widget.Canvas) {
	if !t.IsVisible() {
		return
	}
	b := t.Bounds()
	canvas.DrawRect(geometry.NewRect(b.Min.X, b.Max.Y-1, b.Width(), 1), colDivider)

	sel := t.selected.Get()
	t.rects = t.rects[:0]
	x := b.Min.X + 8
	for i, it := range t.items {
		labelW := canvas.MeasureText(it.label, tabBarFontSize, i == sel)
		itemW := tabBarPadX + tabBarIconSize + tabBarIconGap + labelW + tabBarPadX
		t.rects = append(t.rects, geometry.NewRect(x, b.Min.Y, itemW, b.Height()))

		c := colMuted
		if i == sel {
			c = colAccent
		}
		iconRect := geometry.NewRect(
			x+tabBarPadX, b.Min.Y+(b.Height()-tabBarIconSize)/2-1,
			tabBarIconSize, tabBarIconSize,
		)
		icon.Draw(canvas, it.icon, iconRect, c)
		textRect := geometry.NewRect(
			x+tabBarPadX+tabBarIconSize+tabBarIconGap, b.Min.Y,
			labelW+2, b.Height(),
		)
		canvas.DrawText(it.label, textRect, tabBarFontSize, c, i == sel, widget.TextAlignLeft)
		if i == sel {
			canvas.DrawRoundRect(
				geometry.NewRect(x+tabBarPadX, b.Max.Y-3, itemW-2*tabBarPadX, 3),
				colAccent, 1.5,
			)
		}
		x += itemW
	}
}

func (t *tabBar) Event(_ widget.Context, ev event.Event) bool {
	if !t.IsVisible() || !t.IsEnabled() {
		return false
	}
	me, ok := ev.(*event.MouseEvent)
	if !ok || me.MouseType != event.MousePress || me.Button != event.ButtonLeft {
		return false
	}
	for i, r := range t.rects {
		if r.Contains(me.Position) {
			if t.selected.Get() != i {
				t.selected.Set(i)
				if t.onSelect != nil {
					t.onSelect(i)
				}
			}
			return true
		}
	}
	return false
}

func (t *tabBar) Children() []widget.Widget    { return nil }
func (t *tabBar) AccessibilityRole() a11y.Role { return a11y.RoleTabList }
