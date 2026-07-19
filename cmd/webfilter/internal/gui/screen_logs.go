package gui

import (
	"fmt"
	"strconv"
	"strings"

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
	copyMsg state.Signal[string]

	listSwap *swapWidget
}

func newLogsScreen(u *ui) *logsScreen {
	return &logsScreen{
		u:       u,
		poller:  uimodel.NewLogPoller("blocks", 500),
		filter:  state.NewSignal(""),
		paused:  state.NewSignal(false),
		fetchEr: state.NewSignal(""),
		copyMsg: state.NewSignal(""),
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

// updateVisible recomputes the filtered rows and rebuilds the list. There is
// no selectable-text widget in gogpu/ui, so each row is wrapped in a
// clickable that copies the row to the OS clipboard instead.
func (s *logsScreen) updateVisible() {
	filter := s.filter.Get()
	rows := s.poller.Rows()
	items := make([]widget.Widget, 0, len(rows))
	for _, r := range rows {
		if r.MatchesFilter(filter) {
			row := r
			items = append(items, newClickable(
				logRowWidget(row.Time, row.Client, row.Action, row.Target, row.Detail),
				func() {
					if s.u.copyText(row.ClipboardLine()) {
						s.copyMsg.Set("Copied row to clipboard.")
						s.u.redraw()
					}
				},
			))
		}
	}
	if s.listSwap != nil {
		s.listSwap.SetChild(scrollList(items))
	}
	s.u.redraw()
}

// visibleLines returns the currently filtered rows as clipboard lines.
func (s *logsScreen) visibleLines() []string {
	filter := s.filter.Get()
	var lines []string
	for _, r := range s.poller.Rows() {
		if r.MatchesFilter(filter) {
			lines = append(lines, r.ClipboardLine())
		}
	}
	return lines
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
		s.u.btnOutlined("Copy all", func() {
			lines := s.visibleLines()
			if len(lines) == 0 {
				return
			}
			if s.u.copyText(strings.Join(lines, "\n")) {
				s.copyMsg.Set(fmt.Sprintf("Copied %d rows to clipboard.", len(lines)))
				s.u.redraw()
			}
		}),
		primitives.TextFn(func() string {
			if s.paused.Get() {
				return "paused"
			}
			return ""
		}).FontSize(12).Color(colBlocked),
		noticeText(s.copyMsg.Get),
	).Gap(8).CrossAlign(primitives.CrossAxisCenter)

	return primitives.VBox(
		controls,
		errorText(s.fetchEr.Get),
		logHeaderRow(),
		hairline(),
		primitives.Expanded(s.listSwap),
		fieldLabel("Click a row to copy it; Copy all copies every visible row."),
	).Padding(16).Gap(10).CrossAlign(primitives.CrossAxisStretch)
}
