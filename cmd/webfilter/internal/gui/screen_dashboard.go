package gui

import (
	"sync"

	"github.com/gogpu/ui/core/listview"
	"github.com/gogpu/ui/primitives"
	"github.com/gogpu/ui/theme/material3"
	"github.com/gogpu/ui/widget"

	"github.com/yjlion/gowebfilter/cmd/webfilter/internal/gui/uimodel"
)

type dashboardScreen struct {
	u     *ui
	model *uimodel.StatusModel

	mu        sync.Mutex
	blockRows []uimodel.LogRow

	blocksLV *listview.Widget
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
	rows := d.model.RecentBlockRows()
	d.mu.Lock()
	d.blockRows = rows
	d.mu.Unlock()
	if d.blocksLV != nil {
		d.blocksLV.InvalidateData()
	}
	d.u.redraw()
}

func (d *dashboardScreen) rowCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.blockRows)
}

func (d *dashboardScreen) row(i int) uimodel.LogRow {
	d.mu.Lock()
	defer d.mu.Unlock()
	if i < 0 || i >= len(d.blockRows) {
		return uimodel.LogRow{}
	}
	return d.blockRows[i]
}

func (d *dashboardScreen) build() widget.Widget {
	d.blocksLV = listview.New(
		listview.ItemCountFn(d.rowCount),
		listview.FixedItemHeight(24),
		listview.BuildItem(func(ctx listview.ItemContext) widget.Widget {
			r := d.row(ctx.Index)
			return logRowWidget(r.Time, r.Client, r.Action, r.Target, r.Detail)
		}),
		listview.PainterOpt(material3.ListViewPainter{Theme: d.u.m3}),
	)

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
		primitives.TextFn(d.model.RunningLabel).FontSize(14).Bold(),
		primitives.TextFn(d.model.ListenersLabel).FontSize(13),
		primitives.TextFn(d.model.MgmtLabel).FontSize(13),
		primitives.TextFn(d.model.Tun2SocksLabel).FontSize(13),
		errorText(d.model.ErrorLabel),
		primitives.HBox(buttons...).Gap(8),
		sectionTitle("Recent blocks"),
		primitives.Expanded(d.blocksLV),
	).Padding(16).Gap(10)
}
