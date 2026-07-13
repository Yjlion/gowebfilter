package gui

import (
	"strconv"

	"github.com/gogpu/ui/core/dropdown"
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

	listSwap *swapWidget
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

// updateVisible recomputes the filtered rows and rebuilds the list.
func (s *logsScreen) updateVisible() {
	filter := s.filter.Get()
	rows := s.poller.Rows()
	items := make([]widget.Widget, 0, len(rows))
	for _, r := range rows {
		if r.MatchesFilter(filter) {
			items = append(items, logRowWidget(r.Time, r.Client, r.Action, r.Target, r.Detail))
		}
	}
	if s.listSwap != nil {
		s.listSwap.SetChild(scrollList(items))
	}
	s.u.redraw()
}

func (s *logsScreen) build() widget.Widget {
	s.listSwap = newSwap(scrollList(nil))

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
		primitives.Expanded(s.listSwap),
	).Padding(16).Gap(10)
}
