package gui

import (
	"strconv"
	"sync"

	"github.com/gogpu/ui/core/dropdown"
	"github.com/gogpu/ui/core/listview"
	"github.com/gogpu/ui/core/textfield"
	"github.com/gogpu/ui/primitives"
	"github.com/gogpu/ui/state"
	"github.com/gogpu/ui/theme/material3"
	"github.com/gogpu/ui/widget"

	"github.com/yjlion/gowebfilter/cmd/webfilter/internal/gui/uimodel"
)

var (
	logKinds  = []string{"blocks", "requests", "policy_changes"}
	logLimits = []int{100, 500, 1000}
)

type logsScreen struct {
	u      *ui
	poller *uimodel.LogPoller

	filter  state.Signal[string]
	paused  state.Signal[bool]
	fetchEr state.Signal[string]

	mu      sync.Mutex
	visible []uimodel.LogRow

	lv *listview.Widget
}

func newLogsScreen(u *ui) *logsScreen {
	return &logsScreen{
		u:       u,
		poller:  uimodel.NewLogPoller("blocks", 500),
		filter:  state.NewSignal(""),
		paused:  state.NewSignal(false),
		fetchEr: state.NewSignal(""),
	}
}

// poll fetches the current tail if the logs tab is visible and unpaused.
// Runs on the shared background ticker (2s) and on user actions.
func (s *logsScreen) poll() {
	if s.paused.Get() {
		return
	}
	entries, err := s.u.opts.Client.Logs(s.poller.Kind(), s.poller.Limit())
	if err != nil {
		if !s.u.handleAuthErr(err) {
			s.fetchEr.Set(err.Error())
		}
		s.u.redraw()
		return
	}
	s.fetchEr.Set("")
	if s.poller.Apply(entries) {
		s.updateVisible()
	}
}

// updateVisible recomputes the filtered rows and refreshes the list.
func (s *logsScreen) updateVisible() {
	filter := s.filter.Get()
	rows := s.poller.Rows()
	out := make([]uimodel.LogRow, 0, len(rows))
	for _, r := range rows {
		if r.MatchesFilter(filter) {
			out = append(out, r)
		}
	}
	s.mu.Lock()
	s.visible = out
	s.mu.Unlock()
	if s.lv != nil {
		s.lv.InvalidateData()
	}
	s.u.redraw()
}

func (s *logsScreen) rowCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.visible)
}

func (s *logsScreen) row(i int) uimodel.LogRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i < 0 || i >= len(s.visible) {
		return uimodel.LogRow{}
	}
	return s.visible[i]
}

func (s *logsScreen) build() widget.Widget {
	s.lv = listview.New(
		listview.ItemCountFn(s.rowCount),
		listview.FixedItemHeight(24),
		listview.BuildItem(func(ctx listview.ItemContext) widget.Widget {
			r := s.row(ctx.Index)
			return logRowWidget(r.Time, r.Client, r.Action, r.Target, r.Detail)
		}),
		listview.PainterOpt(material3.ListViewPainter{Theme: s.u.m3}),
	)

	limitLabels := make([]string, len(logLimits))
	for i, n := range logLimits {
		limitLabels[i] = strconv.Itoa(n)
	}

	controls := primitives.HBox(
		fieldLabel("Kind"),
		dropdown.New(
			dropdown.Items(logKinds...),
			dropdown.Selected(0),
			dropdown.OnChange(func(_ int, val string) {
				s.poller.SetKind(val)
				s.updateVisible() // clear immediately; next poll repopulates
				go s.poll()
			}),
			dropdown.PainterOpt(material3.DropdownPainter{Theme: s.u.m3}),
		),
		fieldLabel("Limit"),
		dropdown.New(
			dropdown.Items(limitLabels...),
			dropdown.Selected(1), // 500
			dropdown.OnChange(func(idx int, _ string) {
				s.poller.SetLimit(logLimits[idx])
				go s.poll()
			}),
			dropdown.PainterOpt(material3.DropdownPainter{Theme: s.u.m3}),
		),
		fieldLabel("Filter"),
		// Cap the filter width so the Pause button stays on-screen; an
		// unconstrained textfield in an HBox greedily eats all remaining
		// space and pushes later children off the right edge.
		primitives.Box(textfield.New(
			textfield.Placeholder("substring match"),
			textfield.ValueSignal(s.filter),
			textfield.OnChange(func(string) { s.updateVisible() }),
			textfield.PainterOpt(material3.TextFieldPainter{Theme: s.u.m3}),
		)).Width(320),
		s.u.btnOutlined("Pause", func() {
			next := !s.paused.Get()
			s.paused.Set(next)
			s.poller.SetPaused(next)
			if !next {
				go s.poll()
			}
		}),
		primitives.TextFn(func() string {
			if s.paused.Get() {
				return "paused"
			}
			return ""
		}).FontSize(12).Color(widget.RGBA8(179, 38, 30, 255)),
	).Gap(8).CrossAlign(primitives.CrossAxisCenter)

	return primitives.VBox(
		controls,
		errorText(s.fetchEr.Get),
		primitives.Expanded(s.lv),
	).Padding(16).Gap(10)
}
