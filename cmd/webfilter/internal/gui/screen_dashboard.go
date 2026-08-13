package gui

import (
	"github.com/gogpu/ui/primitives"
	"github.com/gogpu/ui/widget"

	"github.com/yjlion/gowebfilter/cmd/webfilter/internal/gui/uimodel"
)

type dashboardScreen struct {
	u     *ui
	model *uimodel.StatusModel

	listSwap *swapWidget
}

func newDashboardScreen(u *ui) *dashboardScreen {
	return &dashboardScreen{u: u, model: &uimodel.StatusModel{}}
}

// refresh fetches /api/status; safe to call from any goroutine.
func (d *dashboardScreen) refresh() {
	st, err := d.u.opts.Client.Status()
	if err != nil {
		if !d.u.handleAuthErr(err) {
			d.model.SetError(err)
		}
		d.u.redraw()
		return
	}
	d.model.Set(st)
	d.rebuildList()
	d.u.redraw()
}

func (d *dashboardScreen) rebuildList() {
	if d.listSwap == nil {
		return
	}
	rows := d.model.RecentBlockRows()
	items := make([]widget.Widget, 0, len(rows))
	for _, r := range rows {
		row := r
		items = append(items, newClickable(
			logRowWidget(row.Time, row.Client, row.Action, row.Target, row.Detail),
			func() { _ = d.u.copyText(row.ClipboardLine()) },
		))
	}
	d.listSwap.SetChild(scrollList(items))
}

func (d *dashboardScreen) build() widget.Widget {
	d.listSwap = newSwap(scrollList(nil))

	buttons := []widget.Widget{
		d.u.btn("Open Web UI", func() { _ = d.u.opts.OpenBrowser(d.u.opts.MgmtURL) }),
		d.u.btnOutlined("Export CA certificate", func() {
			_ = d.u.opts.OpenBrowser(d.u.opts.MgmtURL + "/api/certs/export")
		}),
		d.u.btnOutlined("Refresh", func() { go d.refresh() }),
	}
	if d.u.opts.SelfHosted && d.u.opts.RestartEngine != nil {
		buttons = append(buttons, d.u.btnOutlined("Restart engine", func() {
			go func() {
				if err := d.u.opts.RestartEngine(); err != nil {
					d.u.engineBanner.Set("Restart failed: " + err.Error())
				} else {
					d.u.restartNeeded.Set(false)
					d.u.engineBanner.Set("")
				}
				d.refresh()
			}()
		}))
	}

	statusCard := card(
		primitives.HBox(
			statusDot(11, func() widget.Color {
				st, _, loaded := d.model.Get()
				switch {
				case loaded && st.ProxyRunning:
					return colOk
				case loaded:
					return colBlocked
				default:
					return colDotIdle
				}
			}),
			primitives.TextFn(d.model.RunningLabel).FontSize(15).Bold(),
		).Gap(8).CrossAlign(primitives.CrossAxisCenter),
		primitives.TextFn(d.model.ListenersLabel).FontSize(13).Color(colMuted),
		primitives.TextFn(d.model.MgmtLabel).FontSize(13).Color(colMuted),
		primitives.TextFn(d.model.Tun2SocksLabel).FontSize(13).Color(colMuted),
		errorText(d.model.ErrorLabel),
	)

	return primitives.VBox(
		noticeText(func() string {
			if d.u.restartNeeded.Get() {
				if d.u.opts.SelfHosted {
					return "Settings saved. Restart the engine to apply them."
				}
				return "Settings saved. Restart the WebFilter service/process to apply them."
			}
			return ""
		}),
		sectionTitle("Status"),
		statusCard,
		primitives.HBox(buttons...).Gap(8),
		sectionTitle("Recent blocks"),
		logHeaderRow(),
		hairline(),
		primitives.Expanded(d.listSwap),
	).Padding(16).Gap(10).CrossAlign(primitives.CrossAxisStretch)
}
