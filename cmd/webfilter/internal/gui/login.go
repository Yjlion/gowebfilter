package gui

import (
	"github.com/gogpu/ui/core/dialog"
	"github.com/gogpu/ui/core/textfield"
	"github.com/gogpu/ui/primitives"
	"github.com/gogpu/ui/state"
	"github.com/gogpu/ui/theme/material3"
)

// loginController shows the password dialog when the management server
// requires authentication (attached mode; self-host mode is pre-seeded with
// a session cookie and never prompts).
type loginController struct {
	u        *ui
	password state.Signal[string]
	errMsg   state.Signal[string]
	dlg      *dialog.Widget
}

func newLoginController(u *ui) *loginController {
	c := &loginController{
		u:        u,
		password: state.NewSignal(""),
		errMsg:   state.NewSignal(""),
	}
	c.dlg = dialog.New(
		dialog.Title("Log in to WebFilter"),
		dialog.Content(primitives.VBox(
			fieldLabel("Management password"),
			textfield.New(
				textfield.ValueSignal(c.password),
				textfield.InputTypeOpt(textfield.TypePassword),
				textfield.OnSubmit(func(string) { c.attempt() }),
				textfield.PainterOpt(material3.TextFieldPainter{Theme: u.m3}),
			),
			errorText(c.errMsg.Get),
		).Gap(6).Padding(4)),
		dialog.Actions(
			dialog.Action{Label: "Log in", OnClick: c.attempt},
		),
		dialog.DismissibleOpt(false),
		dialog.EscapeToCloseOpt(false),
		dialog.PainterOpt(material3.DialogPainter{Theme: u.m3}),
	)
	return c
}

// show opens the dialog (idempotent - repeated auth failures while it is
// already open are no-ops).
func (c *loginController) show() {
	if c.dlg.IsOpen() {
		return
	}
	c.errMsg.Set("")
	c.dlg.Show(c.u.wctx())
	c.u.redraw()
}

func (c *loginController) attempt() {
	pw := c.password.Get()
	go func() {
		if err := c.u.opts.Client.Login(pw); err != nil {
			c.errMsg.Set("Login failed: " + err.Error())
			c.dlg.Show(c.u.wctx()) // the action auto-closed it; reopen
			c.u.redraw()
			return
		}
		c.password.Set("")
		c.errMsg.Set("")
		// Fresh session: repopulate whatever the user is looking at.
		c.u.dash.refresh()
		c.u.pols.refresh()
		c.u.sets.reload()
		c.u.redraw()
	}()
}
