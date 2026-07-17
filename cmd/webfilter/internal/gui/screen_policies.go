package gui

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gogpu/ui/core/button"
	"github.com/gogpu/ui/core/checkbox"
	"github.com/gogpu/ui/core/dialog"
	"github.com/gogpu/ui/core/dropdown"
	"github.com/gogpu/ui/core/slider"
	"github.com/gogpu/ui/core/textfield"
	"github.com/gogpu/ui/primitives"
	"github.com/gogpu/ui/state"
	"github.com/gogpu/ui/theme/material3"
	"github.com/gogpu/ui/widget"

	"github.com/yjlion/gowebfilter/cmd/webfilter/internal/gui/mgmtclient"
	"github.com/yjlion/gowebfilter/cmd/webfilter/internal/gui/uimodel"
	"github.com/yjlion/gowebfilter/internal/models"
)

type policiesScreen struct {
	u *ui

	mu       sync.Mutex
	policies []models.Policy

	// editorGen counts editor mounts; the offscreen snapshot test uses it to
	// wait for the async open() to land.
	editorGen atomic.Int32

	listSwap   *swapWidget
	editorSwap *swapWidget
	listErr    state.Signal[string]
	newName    state.Signal[string]
	newErr     state.Signal[string]
	newDialog  *dialog.Widget
}

func newPoliciesScreen(u *ui) *policiesScreen {
	return &policiesScreen{
		u:       u,
		listErr: state.NewSignal(""),
		newName: state.NewSignal(""),
		newErr:  state.NewSignal(""),
	}
}

// refresh reloads the policy list; safe from any goroutine.
func (s *policiesScreen) refresh() {
	list, err := s.u.opts.Client.Policies()
	if err != nil {
		if !s.u.handleAuthErr(err) {
			s.listErr.Set(err.Error())
		}
		s.u.redraw()
		return
	}
	sort.Slice(list, func(i, j int) bool {
		// default first, then alphabetical - matches the web UI's ordering.
		if list[i].Name == uimodel.DefaultPolicyName {
			return true
		}
		if list[j].Name == uimodel.DefaultPolicyName {
			return false
		}
		return list[i].Name < list[j].Name
	})
	s.listErr.Set("")
	s.mu.Lock()
	s.policies = list
	s.mu.Unlock()
	s.rebuildList()
	s.u.redraw()
}

// rebuildList renders the policy list as clickable rows (one button each);
// clicking opens that policy in the editor.
func (s *policiesScreen) rebuildList() {
	if s.listSwap == nil {
		return
	}
	s.mu.Lock()
	list := append([]models.Policy(nil), s.policies...)
	s.mu.Unlock()

	rows := make([]widget.Widget, 0, len(list))
	for _, p := range list {
		p := p
		label := p.Name
		if chips := uimodel.PolicyChips(p); chips != "" {
			label += "  [" + chips + "]"
		}
		label += "  —  " + uimodel.PolicySourceSummary(p)
		rows = append(rows, button.New(
			button.TextOpt(label),
			button.VariantOpt(button.TextOnly),
			button.OnClick(func() { s.open(p.Name) }),
			button.PainterOpt(material3.ButtonPainter{Theme: s.u.m3}),
		))
	}
	s.listSwap.SetChild(scrollList(rows))
}

// open fetches the policy fresh (list entries may be stale) and mounts the
// editor.
func (s *policiesScreen) open(name string) {
	go func() {
		p, err := s.u.opts.Client.Policy(name)
		if err != nil {
			if !s.u.handleAuthErr(err) {
				s.listErr.Set(err.Error())
			}
			s.u.redraw()
			return
		}
		s.editorSwap.SetChild(s.buildEditor(p))
		s.editorGen.Add(1)
		s.u.redraw()
	}()
}

func (s *policiesScreen) build() widget.Widget {
	s.listSwap = newSwap(scrollList(nil))

	s.newDialog = dialog.New(
		dialog.Title("New policy"),
		dialog.Content(primitives.VBox(
			fieldLabel("Name"),
			textfield.New(
				textfield.Placeholder("e.g. kids"),
				textfield.ValueSignal(s.newName),
				textfield.PainterOpt(material3.TextFieldPainter{Theme: s.u.m3}),
			),
			errorText(s.newErr.Get),
		).Gap(6).Padding(4)),
		dialog.Actions(
			dialog.Action{Label: "Cancel"},
			dialog.Action{Label: "Create", OnClick: s.createFromDialog},
		),
		dialog.PainterOpt(material3.DialogPainter{Theme: s.u.m3}),
	)

	left := primitives.VBox(
		primitives.HBox(
			s.u.btn("New", func() {
				s.newName.Set("")
				s.newErr.Set("")
				s.newDialog.Show(s.u.wctx())
			}),
			s.u.btnOutlined("Reload", func() { go s.refresh() }),
		).Gap(8),
		errorText(s.listErr.Get),
		primitives.Expanded(s.listSwap),
	).Gap(8).MaxWidthValue(300).MinWidthValue(240)

	s.editorSwap = newSwap(primitives.Box(
		primitives.Text("Select a policy to edit it.").FontSize(14).Color(widget.RGBA8(90, 90, 100, 255)),
	).Padding(24))

	return primitives.HBox(
		left,
		vline(),
		primitives.Expanded(s.editorSwap),
	).Padding(16).Gap(16).CrossAlign(primitives.CrossAxisStretch)
}

func (s *policiesScreen) createFromDialog() {
	name := strings.TrimSpace(s.newName.Get())
	if err := uimodel.ValidatePolicyName(name); err != nil {
		s.newErr.Set(err.Error())
		s.newDialog.Show(s.u.wctx()) // action closed it; reopen with the error
		return
	}
	go func() {
		p := models.NewPolicy()
		p.Name = name
		if _, err := s.u.opts.Client.CreatePolicy(p); err != nil {
			if !s.u.handleAuthErr(err) {
				s.newErr.Set(err.Error())
				s.newDialog.Show(s.u.wctx())
			}
			s.u.redraw()
			return
		}
		s.refresh()
		s.open(name)
	}()
}

// policyEditorState carries the editor's widget-bound signals; Save reads
// them back into the full policy document.
type policyEditorState struct {
	name        state.Signal[string]
	inactive    state.Signal[bool]
	sourceIPs   state.Signal[string]
	sourceMACs  state.Signal[string]
	mitmMode    state.Signal[int]
	mitmSites   state.Signal[string]
	urlfEnabled state.Signal[bool]
	urlfMode    state.Signal[int]
	urlfBlock   state.Signal[string]
	urlfAllow   state.Signal[string]
	urlfQuic    state.Signal[bool]
	ssEnabled   state.Signal[bool]
	dohEnabled  state.Signal[bool]
	dohServer   state.Signal[string]
	ytEnabled   state.Signal[bool]
	ytMode      state.Signal[int]
	ytHome      state.Signal[bool]
	ytComments  state.Signal[bool]
	ytRecs      state.Signal[bool]
	ytChannels  state.Signal[string]
	tcEnabled   state.Signal[bool]
	tcThreshold state.Signal[float32]
	icEnabled   state.Signal[bool]
	icAction    state.Signal[int]
	icThreshold state.Signal[float32]
	bpMessage   state.Signal[string]
	saveMsg     state.Signal[string]
	saveErr     state.Signal[string]

	engineKeys []string // sorted SafeSearch engine names
	engines    map[string]state.Signal[bool]
}

// Dropdown labels stay short - the dropdown sizes itself to the text and a
// long label collides with its chevron; the fieldLabel above each dropdown
// carries the explanation instead.
var (
	mitmModes     = []string{"exclude", "include"}
	mitmModeVals  = []models.MitmMode{models.MitmModeExclude, models.MitmModeInclude}
	urlfModes     = []string{"blacklist", "whitelist"}
	urlfModeVals  = []models.UrlFilterMode{models.UrlFilterModeBlacklist, models.UrlFilterModeWhitelist}
	ytModes       = []string{"blacklist", "whitelist"}
	ytModeVals    = []models.YouTubeMode{models.YouTubeModeBlacklist, models.YouTubeModeWhitelist}
	imgActions    = []string{"blur", "block", "checkerboard"}
	imgActionVals = []models.ImageClassifierAction{models.ImageActionBlur, models.ImageActionBlock, models.ImageActionCheckerboard}
)

func indexOf[T comparable](vals []T, v T) int {
	for i, x := range vals {
		if x == v {
			return i
		}
	}
	return 0
}

// buildEditor renders one policy's full document as a form. Save PUTs the
// whole document back (never a partial body - the sub-config UnmarshalJSON
// default-reset makes partial updates destructive).
func (s *policiesScreen) buildEditor(p models.Policy) widget.Widget {
	es := &policyEditorState{
		name:        state.NewSignal(p.Name),
		inactive:    state.NewSignal(p.Inactive),
		sourceIPs:   state.NewSignal(strings.Join(p.SourceIPs, ", ")),
		sourceMACs:  state.NewSignal(strings.Join(p.SourceMACs, ", ")),
		mitmMode:    state.NewSignal(indexOf(mitmModeVals, p.Mitm.Mode)),
		mitmSites:   state.NewSignal(strings.Join(p.Mitm.Sites, ", ")),
		urlfEnabled: state.NewSignal(p.UrlFilter.Enabled),
		urlfMode:    state.NewSignal(indexOf(urlfModeVals, p.UrlFilter.Mode)),
		urlfBlock:   state.NewSignal(strings.Join(p.UrlFilter.Block, ", ")),
		urlfAllow:   state.NewSignal(strings.Join(p.UrlFilter.Allow, ", ")),
		urlfQuic:    state.NewSignal(p.UrlFilter.BlockQuic),
		ssEnabled:   state.NewSignal(p.SafeSearch.Enabled),
		dohEnabled:  state.NewSignal(p.Doh.Enabled),
		dohServer:   state.NewSignal(p.Doh.Server),
		ytEnabled:   state.NewSignal(p.YouTube.Enabled),
		ytMode:      state.NewSignal(indexOf(ytModeVals, p.YouTube.Mode)),
		ytHome:      state.NewSignal(p.YouTube.BlockHome),
		ytComments:  state.NewSignal(p.YouTube.RemoveComments),
		ytRecs:      state.NewSignal(p.YouTube.RemoveRecommendations),
		ytChannels:  state.NewSignal(strings.Join(p.YouTube.Channels, ", ")),
		tcEnabled:   state.NewSignal(p.TextClassifier.Enabled),
		tcThreshold: state.NewSignal(float32(p.TextClassifier.Threshold)),
		icEnabled:   state.NewSignal(p.ImageClassifier.Enabled),
		icAction:    state.NewSignal(indexOf(imgActionVals, p.ImageClassifier.Action)),
		icThreshold: state.NewSignal(float32(p.ImageClassifier.Threshold)),
		bpMessage:   state.NewSignal(p.BlockPage.Message),
		saveMsg:     state.NewSignal(""),
		saveErr:     state.NewSignal(""),
		engines:     map[string]state.Signal[bool]{},
	}
	for name := range p.SafeSearch.Engines {
		es.engineKeys = append(es.engineKeys, name)
	}
	sort.Strings(es.engineKeys)
	for _, name := range es.engineKeys {
		es.engines[name] = state.NewSignal(p.SafeSearch.Engines[name].Enabled)
	}

	origName := p.Name
	isDefault := origName == uimodel.DefaultPolicyName

	tf := func(sig state.Signal[string], placeholder string) widget.Widget {
		return textfield.New(
			textfield.ValueSignal(sig),
			textfield.Placeholder(placeholder),
			textfield.PainterOpt(material3.TextFieldPainter{Theme: s.u.m3}),
		)
	}
	cb := func(label string, sig state.Signal[bool]) widget.Widget {
		return checkbox.New(
			checkbox.LabelOpt(label),
			checkbox.CheckedSignal(sig),
			checkbox.PainterOpt(material3.CheckboxPainter{Theme: s.u.m3}),
		)
	}
	dd := func(items []string, sig state.Signal[int]) widget.Widget {
		return dropdown.New(
			dropdown.Items(items...),
			dropdown.SelectedSignal(sig),
			dropdown.PainterOpt(material3.DropdownPainter{Theme: s.u.m3}),
		)
	}
	thresholdRow := func(sig state.Signal[float32]) widget.Widget {
		return primitives.HBox(
			primitives.Box(slider.New(
				slider.Min(0), slider.Max(1), slider.Step(0.01),
				slider.ValueSignal(sig),
				slider.PainterOpt(material3.SliderPainter{Theme: s.u.m3}),
			)).Width(320),
			primitives.TextFn(func() string { return fmt.Sprintf("%.2f", sig.Get()) }).FontSize(12),
		).Gap(12).CrossAlign(primitives.CrossAxisCenter)
	}

	var nameField widget.Widget
	if isDefault {
		nameField = primitives.Text("default (catch-all policy - cannot be renamed or deleted)").FontSize(13)
	} else {
		nameField = tf(es.name, "policy name")
	}

	engineChecks := []widget.Widget{}
	for _, name := range es.engineKeys {
		engineChecks = append(engineChecks, cb(name, es.engines[name]))
	}
	if len(engineChecks) == 0 {
		engineChecks = append(engineChecks,
			fieldLabel("Engine list is configured in the Web UI."))
	}

	header := []widget.Widget{
		sectionTitle("Policy: " + origName),
		fieldLabel("Name"), nameField,
		cb("Inactive (skip this policy entirely)", es.inactive),
		fieldLabel("Source IPs / CIDRs (comma-separated; empty = catch-all)"), tf(es.sourceIPs, "192.168.1.50, 10.0.0.0/24"),
		fieldLabel("Source MACs (comma-separated)"), tf(es.sourceMACs, "aa:bb:cc:dd:ee:ff"),
		fieldLabel("Schedule"), primitives.Text(uimodel.ScheduleSummary(p)).FontSize(13),

		sectionTitle("MITM"),
		fieldLabel("Mode (exclude = intercept everything except the sites below; include = only them)"),
		dd(mitmModes, es.mitmMode),
		fieldLabel("Sites (comma-separated)"), tf(es.mitmSites, "bank.example"),

		sectionTitle("URL filter"),
		cb("Enabled", es.urlfEnabled),
		fieldLabel("Mode (blacklist = block listed; whitelist = allow only listed)"),
		dd(urlfModes, es.urlfMode),
		fieldLabel("Block (domains/keywords, comma-separated)"), tf(es.urlfBlock, "ads.example, gambling"),
		fieldLabel("Allow (comma-separated)"), tf(es.urlfAllow, ""),
		cb("Block QUIC (force filterable TCP)", es.urlfQuic),
		fieldLabel("Category blocklists are managed in the Web UI."),

		sectionTitle("SafeSearch"),
		cb("Enabled", es.ssEnabled),
	}
	header = append(header, engineChecks...)
	rest := []widget.Widget{
		sectionTitle("DNS-over-HTTPS filter"),
		cb("Enabled", es.dohEnabled),
		fieldLabel("DoH server"), tf(es.dohServer, "https://1.1.1.3/dns-query"),

		sectionTitle("YouTube"),
		cb("Enabled", es.ytEnabled),
		fieldLabel("Channel mode (blacklist = block listed channels; whitelist = allow only listed)"),
		dd(ytModes, es.ytMode),
		fieldLabel("Channels (comma-separated)"), tf(es.ytChannels, ""),
		cb("Block home page", es.ytHome),
		cb("Remove comments", es.ytComments),
		cb("Remove recommendations", es.ytRecs),

		sectionTitle("Text classifier (NSFW text)"),
		cb("Enabled", es.tcEnabled),
		fieldLabel("Threshold"), thresholdRow(es.tcThreshold),

		sectionTitle("Image classifier (NSFW images)"),
		cb("Enabled", es.icEnabled),
		fieldLabel("Action"), dd(imgActions, es.icAction),
		fieldLabel("Threshold"), thresholdRow(es.icThreshold),

		sectionTitle("Block page"),
		fieldLabel("Message"), tf(es.bpMessage, "This page is blocked."),
	}

	actions := []widget.Widget{
		s.u.btn("Save", func() { s.save(origName, p, es) }),
	}
	if !isDefault {
		confirm := dialog.New(
			dialog.Title("Delete policy \""+origName+"\"?"),
			dialog.Content(primitives.Text("Clients matched by it fall through to the next policy.").FontSize(13)),
			dialog.Actions(
				dialog.Action{Label: "Cancel"},
				dialog.Action{Label: "Delete", OnClick: func() { s.deletePolicy(origName) }},
			),
			dialog.PainterOpt(material3.DialogPainter{Theme: s.u.m3}),
		)
		actions = append(actions, s.u.btnOutlined("Delete", func() { confirm.Show(s.u.wctx()) }))
	}
	actions = append(actions,
		errorText(es.saveErr.Get),
		noticeText(es.saveMsg.Get),
	)

	form := append(header, rest...)
	form = append(form, primitives.HBox(actions...).Gap(8).CrossAlign(primitives.CrossAxisCenter))

	// The form lives in a white card so it reads as a distinct editor pane
	// against the window background, capped in width so fields stay a
	// comfortable length on a wide window.
	return newScrollBox(
		primitives.VBox(panel(760, 8, form...)).Padding(8),
	)
}

// save reads the editor signals back over the originally fetched document
// and PUTs the full result.
func (s *policiesScreen) save(origName string, orig models.Policy, es *policyEditorState) {
	out := orig // fields the editor doesn't expose (schedule, categories, ...) pass through

	name := strings.TrimSpace(es.name.Get())
	if origName == uimodel.DefaultPolicyName {
		name = uimodel.DefaultPolicyName
	}
	if err := uimodel.ValidatePolicyName(name); err != nil {
		es.saveErr.Set(err.Error())
		s.u.redraw()
		return
	}
	out.Name = name
	out.Inactive = es.inactive.Get()
	out.SourceIPs = uimodel.SplitLines(es.sourceIPs.Get())
	out.SourceMACs = uimodel.SplitLines(es.sourceMACs.Get())
	out.Mitm.Mode = mitmModeVals[es.mitmMode.Get()]
	out.Mitm.Sites = uimodel.SplitLines(es.mitmSites.Get())
	out.UrlFilter.Enabled = es.urlfEnabled.Get()
	out.UrlFilter.Mode = urlfModeVals[es.urlfMode.Get()]
	out.UrlFilter.Block = uimodel.SplitLines(es.urlfBlock.Get())
	out.UrlFilter.Allow = uimodel.SplitLines(es.urlfAllow.Get())
	out.UrlFilter.BlockQuic = es.urlfQuic.Get()
	out.SafeSearch.Enabled = es.ssEnabled.Get()
	for _, key := range es.engineKeys {
		eng := out.SafeSearch.Engines[key]
		eng.Enabled = es.engines[key].Get()
		out.SafeSearch.Engines[key] = eng
	}
	out.Doh.Enabled = es.dohEnabled.Get()
	out.Doh.Server = strings.TrimSpace(es.dohServer.Get())
	out.YouTube.Enabled = es.ytEnabled.Get()
	out.YouTube.Mode = ytModeVals[es.ytMode.Get()]
	out.YouTube.Channels = uimodel.SplitLines(es.ytChannels.Get())
	out.YouTube.BlockHome = es.ytHome.Get()
	out.YouTube.RemoveComments = es.ytComments.Get()
	out.YouTube.RemoveRecommendations = es.ytRecs.Get()
	out.TextClassifier.Enabled = es.tcEnabled.Get()
	out.TextClassifier.Threshold = uimodel.ClampThreshold(float64(es.tcThreshold.Get()))
	out.ImageClassifier.Enabled = es.icEnabled.Get()
	out.ImageClassifier.Action = imgActionVals[es.icAction.Get()]
	out.ImageClassifier.Threshold = uimodel.ClampThreshold(float64(es.icThreshold.Get()))
	out.BlockPage.Message = es.bpMessage.Get()

	go func() {
		saved, err := s.u.opts.Client.UpdatePolicy(origName, out)
		if err != nil {
			if !s.u.handleAuthErr(err) {
				if isManagedLocked(err) {
					es.saveErr.Set("Settings are managed by your organization; policies are read-only.")
				} else {
					es.saveErr.Set(err.Error())
				}
			}
			es.saveMsg.Set("")
			s.u.redraw()
			return
		}
		es.saveErr.Set("")
		es.saveMsg.Set("Saved. Policy changes apply immediately.")
		s.refresh()
		if saved.Name != origName {
			s.open(saved.Name) // rename: remount the editor under the new name
		}
		s.u.redraw()
	}()
}

func (s *policiesScreen) deletePolicy(name string) {
	go func() {
		if err := s.u.opts.Client.DeletePolicy(name); err != nil {
			if !s.u.handleAuthErr(err) {
				s.listErr.Set(err.Error())
			}
			s.u.redraw()
			return
		}
		s.editorSwap.SetChild(primitives.Box(
			primitives.Text("Policy deleted.").FontSize(14),
		).Padding(24))
		s.refresh()
	}()
}

func isManagedLocked(err error) bool {
	return errors.Is(err, mgmtclient.ErrManagedLocked)
}
