// Package models defines the shared JSON schema for policies/*.json and
// config/settings.json - the single source of truth for both the proxy
// engine and the management API, mirroring Python's shared/models.py.
//
// Every struct here implements a custom UnmarshalJSON that first populates
// itself with the same field defaults as the Pydantic v2 models (so an
// absent field takes the documented default, exactly like Pydantic), then
// overlays whatever the input JSON actually specifies. This is the one
// piece of boilerplate needed to get Pydantic-default-on-missing-field
// semantics out of encoding/json, which otherwise only gives Go zero
// values for absent fields.
package models

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/yjlion/gowebfilter/internal/macutil"
)

// ---- DohConfig ----

type DohConfig struct {
	Enabled     bool     `json:"enabled"`
	Server      string   `json:"server"`
	Exclude     []string `json:"exclude"`
	IncludeOnly []string `json:"include_only"`
}

func NewDohConfig() DohConfig {
	return DohConfig{
		Server:      "https://1.1.1.3/dns-query",
		Exclude:     []string{},
		IncludeOnly: []string{},
	}
}

type dohConfigAlias DohConfig

func (c *DohConfig) UnmarshalJSON(data []byte) error {
	*c = NewDohConfig()
	if err := json.Unmarshal(data, (*dohConfigAlias)(c)); err != nil {
		return err
	}
	c.Server = trimSpace(c.Server)
	return nil
}

// ---- TextClassifierConfig ----

// TextClassifierMode selects what the text classifier does with a page.
type TextClassifierMode string

const (
	// TextModeBlock replaces adult pages with the block page (the original
	// and default behaviour).
	TextModeBlock TextClassifierMode = "block"
	// TextModeCensor never blocks; it masks offensive words in the page
	// text with asterisks.
	TextModeCensor TextClassifierMode = "censor"
	// TextModeBoth blocks adult pages and censors the rest.
	TextModeBoth TextClassifierMode = "both"
)

// Blocks reports whether the mode can block a page.
func (m TextClassifierMode) Blocks() bool { return m != TextModeCensor }

// Censors reports whether the mode masks words.
func (m TextClassifierMode) Censors() bool { return m == TextModeCensor || m == TextModeBoth }

type TextClassifierConfig struct {
	Enabled     bool               `json:"enabled"`
	Mode        TextClassifierMode `json:"mode"`
	Threshold   float64            `json:"threshold"`
	Exclude     []string           `json:"exclude"`
	IncludeOnly []string           `json:"include_only"`
	// CensorWords are extra words/phrases masked in censor modes, on top of
	// the embedded multilingual list.
	CensorWords []string `json:"censor_words"`
	// CensorLanguages pins which languages' Latin-script word lists the
	// censor uses (e.g. ["en","de"]). Empty means automatic: the page's
	// <html lang> / Content-Language, falling back to English. Words in
	// non-Latin scripts are always checked.
	CensorLanguages []string `json:"censor_languages"`
}

func NewTextClassifierConfig() TextClassifierConfig {
	return TextClassifierConfig{
		Mode:            TextModeBlock,
		Threshold:       0.80,
		Exclude:         []string{},
		IncludeOnly:     []string{},
		CensorWords:     []string{},
		CensorLanguages: []string{},
	}
}

type textClassifierConfigAlias TextClassifierConfig

func (c *TextClassifierConfig) UnmarshalJSON(data []byte) error {
	*c = NewTextClassifierConfig()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if v, ok := raw["enabled"]; ok {
		if err := json.Unmarshal(v, &c.Enabled); err != nil {
			return err
		}
	}
	if v, ok := raw["threshold"]; ok {
		f, err := decodeJSONFloat(v)
		if err != nil {
			return fmt.Errorf("text_classifier.threshold: %w", err)
		}
		c.Threshold = f
	}
	if v, ok := raw["exclude"]; ok {
		if err := json.Unmarshal(v, &c.Exclude); err != nil {
			return err
		}
	}
	if v, ok := raw["include_only"]; ok {
		if err := json.Unmarshal(v, &c.IncludeOnly); err != nil {
			return err
		}
	}
	if v, ok := raw["mode"]; ok {
		var m string
		if err := json.Unmarshal(v, &m); err != nil {
			return fmt.Errorf("text_classifier.mode: %w", err)
		}
		switch mode := TextClassifierMode(strings.ToLower(strings.TrimSpace(m))); mode {
		case TextModeBlock, TextModeCensor, TextModeBoth:
			c.Mode = mode
		default:
			// Unknown (or empty) modes keep the safe historical behaviour.
			c.Mode = TextModeBlock
		}
	}
	for key, dst := range map[string]*[]string{"censor_words": &c.CensorWords, "censor_languages": &c.CensorLanguages} {
		if v, ok := raw[key]; ok && string(v) != "null" {
			if err := json.Unmarshal(v, dst); err != nil {
				return fmt.Errorf("text_classifier.%s: %w", key, err)
			}
		}
	}
	return nil
}

// ---- ImageClassifierConfig ----

type ImageClassifierAction string

const (
	ImageActionBlur         ImageClassifierAction = "blur"
	ImageActionBlock        ImageClassifierAction = "block"
	ImageActionCheckerboard ImageClassifierAction = "checkerboard"
)

type ImageClassifierConfig struct {
	Enabled      bool                  `json:"enabled"`
	Action       ImageClassifierAction `json:"action"`
	Threshold    float64               `json:"threshold"`
	MinDimension int                   `json:"min_dimension"`
	Exclude      []string              `json:"exclude"`
	IncludeOnly  []string              `json:"include_only"`
}

func NewImageClassifierConfig() ImageClassifierConfig {
	return ImageClassifierConfig{
		Action:       ImageActionBlur,
		Threshold:    0.4,
		MinDimension: 100,
		Exclude:      []string{},
		IncludeOnly:  []string{},
	}
}

type imageClassifierConfigAlias ImageClassifierConfig

func (c *ImageClassifierConfig) UnmarshalJSON(data []byte) error {
	*c = NewImageClassifierConfig()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if v, ok := raw["enabled"]; ok {
		if err := json.Unmarshal(v, &c.Enabled); err != nil {
			return err
		}
	}
	if v, ok := raw["action"]; ok {
		if err := json.Unmarshal(v, &c.Action); err != nil {
			return err
		}
	}
	if v, ok := raw["threshold"]; ok {
		f, err := decodeJSONFloat(v)
		if err != nil {
			return fmt.Errorf("image_classifier.threshold: %w", err)
		}
		c.Threshold = f
	}
	if v, ok := raw["min_dimension"]; ok {
		i, err := decodeJSONInt(v)
		if err != nil {
			return fmt.Errorf("image_classifier.min_dimension: %w", err)
		}
		c.MinDimension = i
	}
	if v, ok := raw["exclude"]; ok {
		if err := json.Unmarshal(v, &c.Exclude); err != nil {
			return err
		}
	}
	if v, ok := raw["include_only"]; ok {
		if err := json.Unmarshal(v, &c.IncludeOnly); err != nil {
			return err
		}
	}
	return nil
}

// ---- SafeSearch ----

// SafeSearchEngines lists the engines configurable per-policy; also used to
// migrate the legacy flat schema (global block_*_tab flags) into the
// engines map, one entry per known engine.
var SafeSearchEngines = []string{"google", "bing", "duckduckgo", "yahoo", "youtube", "brave", "yandex"}

type SafeSearchEngineConfig struct {
	Enabled        bool `json:"enabled"`
	BlockImagesTab bool `json:"block_images_tab"`
	BlockVideosTab bool `json:"block_videos_tab"`
	BlockAiTab     bool `json:"block_ai_tab"`
}

func NewSafeSearchEngineConfig() SafeSearchEngineConfig {
	return SafeSearchEngineConfig{Enabled: true}
}

type safeSearchEngineConfigAlias SafeSearchEngineConfig

func (c *SafeSearchEngineConfig) UnmarshalJSON(data []byte) error {
	*c = NewSafeSearchEngineConfig()
	return json.Unmarshal(data, (*safeSearchEngineConfigAlias)(c))
}

type SafeSearchConfig struct {
	Enabled     bool                              `json:"enabled"`
	Engines     map[string]SafeSearchEngineConfig `json:"engines"`
	Exclude     []string                          `json:"exclude"`
	IncludeOnly []string                          `json:"include_only"`
}

func NewSafeSearchConfig() SafeSearchConfig {
	return SafeSearchConfig{
		Engines:     map[string]SafeSearchEngineConfig{},
		Exclude:     []string{},
		IncludeOnly: []string{},
	}
}

// legacySafeSearchFlags captures the pre-engines-map flat schema, so old
// policy files (or a manually hand-edited one) still load correctly.
type legacySafeSearchFlags struct {
	BlockImagesTab *bool `json:"block_images_tab"`
	BlockVideosTab *bool `json:"block_videos_tab"`
	BlockAiTab     *bool `json:"block_ai_tab"`
}

type safeSearchConfigAlias SafeSearchConfig

func (c *SafeSearchConfig) UnmarshalJSON(data []byte) error {
	*c = NewSafeSearchConfig()
	if err := json.Unmarshal(data, (*safeSearchConfigAlias)(c)); err != nil {
		return err
	}
	// Legacy migration: if the input has top-level block_*_tab flags and no
	// engines map was provided, upgrade to a per-engine map covering every
	// known engine, mirroring the Pydantic model_validator(mode="before").
	var legacy legacySafeSearchFlags
	_ = json.Unmarshal(data, &legacy)
	hasLegacy := legacy.BlockImagesTab != nil || legacy.BlockVideosTab != nil || legacy.BlockAiTab != nil
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(data, &raw)
	_, hasEngines := raw["engines"]
	if hasLegacy && !hasEngines {
		flags := SafeSearchEngineConfig{Enabled: true}
		if legacy.BlockImagesTab != nil {
			flags.BlockImagesTab = *legacy.BlockImagesTab
		}
		if legacy.BlockVideosTab != nil {
			flags.BlockVideosTab = *legacy.BlockVideosTab
		}
		if legacy.BlockAiTab != nil {
			flags.BlockAiTab = *legacy.BlockAiTab
		}
		for _, name := range SafeSearchEngines {
			c.Engines[name] = flags
		}
	}
	return nil
}

// ---- YouTubeConfig ----

type YouTubeMode string

const (
	YouTubeModeBlacklist YouTubeMode = "blacklist"
	YouTubeModeWhitelist YouTubeMode = "whitelist"
)

type YouTubeConfig struct {
	Enabled               bool        `json:"enabled"`
	Mode                  YouTubeMode `json:"mode"`
	Channels              []string    `json:"channels"`
	Exclude               []string    `json:"exclude"`
	IncludeOnly           []string    `json:"include_only"`
	BlockHome             bool        `json:"block_home"`
	RemoveComments        bool        `json:"remove_comments"`
	RemoveRecommendations bool        `json:"remove_recommendations"`
}

func NewYouTubeConfig() YouTubeConfig {
	return YouTubeConfig{
		Mode:        YouTubeModeBlacklist,
		Channels:    []string{},
		Exclude:     []string{},
		IncludeOnly: []string{},
		BlockHome:   true,
	}
}

type youTubeConfigAlias YouTubeConfig

func (c *YouTubeConfig) UnmarshalJSON(data []byte) error {
	*c = NewYouTubeConfig()
	return json.Unmarshal(data, (*youTubeConfigAlias)(c))
}

// ---- MitmConfig ----

type MitmMode string

const (
	MitmModeExclude MitmMode = "exclude"
	MitmModeInclude MitmMode = "include"
)

type MitmUAMode string

const (
	MitmUAModeOff     MitmUAMode = "off"
	MitmUAModeExclude MitmUAMode = "exclude"
	MitmUAModeInclude MitmUAMode = "include"
)

type MitmConfig struct {
	Mode       MitmMode   `json:"mode"`
	Sites      []string   `json:"sites"`
	UAMode     MitmUAMode `json:"ua_mode"`
	UserAgents []string   `json:"user_agents"`
}

func NewMitmConfig() MitmConfig {
	return MitmConfig{
		Mode:       MitmModeExclude,
		Sites:      []string{},
		UAMode:     MitmUAModeOff,
		UserAgents: []string{},
	}
}

type mitmConfigAlias MitmConfig

func (c *MitmConfig) UnmarshalJSON(data []byte) error {
	*c = NewMitmConfig()
	return json.Unmarshal(data, (*mitmConfigAlias)(c))
}

// ---- UrlFilterConfig ----

type UrlFilterMode string

const (
	UrlFilterModeBlacklist UrlFilterMode = "blacklist"
	UrlFilterModeWhitelist UrlFilterMode = "whitelist"
)

// CategoryAction is what a url_filter category does to a matching site.
type CategoryAction string

const (
	CategoryActionBlock CategoryAction = "block"
	CategoryActionAllow CategoryAction = "allow"
)

type UrlFilterConfig struct {
	Enabled bool     `json:"enabled"`
	Allow   []string `json:"allow"`
	Block   []string `json:"block"`
	// Mode is the default for sites no rule or category matches:
	// blacklist = allow them (the default), whitelist = block them.
	Mode UrlFilterMode `json:"mode"`
	// CategoryActions sets each shared category to block or allow; a
	// category that is absent is off. An allow-category hit wins over a
	// block-category hit, so e.g. "banking: allow" can carve banks out of a
	// broader blocked category.
	CategoryActions map[string]CategoryAction `json:"category_actions"`
	// Categories is the legacy (pre-category_actions) list. On input it is
	// only read when category_actions is absent: every listed category
	// becomes "block" in blacklist mode or "allow" in whitelist mode, which
	// is exactly what the list meant then. On output it carries the names of
	// every category that has an action, for readers that predate
	// category_actions.
	Categories []string `json:"categories"`
	BlockQuic  bool     `json:"block_quic"`
}

// NewUrlFilterConfig defaults BlockQuic on: stripping Alt-Svc is a
// header-only change with no false-positive cost, and leaving it off by
// default meant a filtering proxy silently let browsers discover HTTP/3 and
// leave the pipeline. It is independent of Enabled - QuicBlocker is about
// keeping traffic inspectable, not about URL rules.
func NewUrlFilterConfig() UrlFilterConfig {
	return UrlFilterConfig{
		Allow:           []string{},
		Block:           []string{},
		Mode:            UrlFilterModeBlacklist,
		CategoryActions: map[string]CategoryAction{},
		Categories:      []string{},
		BlockQuic:       true,
	}
}

type urlFilterConfigAlias UrlFilterConfig

func (c *UrlFilterConfig) UnmarshalJSON(data []byte) error {
	*c = NewUrlFilterConfig()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if err := json.Unmarshal(data, (*urlFilterConfigAlias)(c)); err != nil {
		return err
	}
	actions := map[string]CategoryAction{}
	if v, ok := raw["category_actions"]; ok && string(v) != "null" {
		for name, a := range c.CategoryActions {
			name = strings.TrimSpace(name)
			switch act := CategoryAction(strings.ToLower(strings.TrimSpace(string(a)))); act {
			case CategoryActionBlock, CategoryActionAllow:
				if name != "" {
					actions[name] = act
				}
			}
			// Anything else ("off", "", typos) means off.
		}
	} else {
		// Legacy schema: one list whose meaning came from the global mode.
		act := CategoryActionBlock
		if c.Mode == UrlFilterModeWhitelist {
			act = CategoryActionAllow
		}
		for _, name := range c.Categories {
			if name = strings.TrimSpace(name); name != "" {
				actions[name] = act
			}
		}
		// Whitelist with no categories never blocked anything (the old
		// verdict only applied when categories were listed). Under the new
		// meaning - "block every unlisted site" - it would block
		// everything, so keep the old behaviour by migrating it to
		// blacklist.
		if c.Mode == UrlFilterModeWhitelist && len(actions) == 0 {
			c.Mode = UrlFilterModeBlacklist
		}
	}
	if c.Mode != UrlFilterModeWhitelist {
		c.Mode = UrlFilterModeBlacklist
	}
	c.CategoryActions = actions
	c.Categories = c.categoryNames()
	return nil
}

// MarshalJSON keeps the legacy categories list in step with
// category_actions, whoever mutated the struct.
func (c UrlFilterConfig) MarshalJSON() ([]byte, error) {
	c.CategoryActions = c.EffectiveCategoryActions()
	c.Categories = c.categoryNames()
	return json.Marshal(urlFilterConfigAlias(c))
}

// EffectiveCategoryActions is CategoryActions, or - for a config built in
// Go with only the legacy Categories list set - that list read the legacy
// way (block in blacklist mode, allow in whitelist mode). JSON input is
// already migrated by UnmarshalJSON.
func (c UrlFilterConfig) EffectiveCategoryActions() map[string]CategoryAction {
	if len(c.CategoryActions) > 0 || len(c.Categories) == 0 {
		if c.CategoryActions == nil {
			return map[string]CategoryAction{}
		}
		return c.CategoryActions
	}
	act := CategoryActionBlock
	if c.Mode == UrlFilterModeWhitelist {
		act = CategoryActionAllow
	}
	out := make(map[string]CategoryAction, len(c.Categories))
	for _, name := range c.Categories {
		if name = strings.TrimSpace(name); name != "" {
			out[name] = act
		}
	}
	return out
}

func (c UrlFilterConfig) categoryNames() []string {
	actions := c.EffectiveCategoryActions()
	names := make([]string, 0, len(actions))
	for name := range actions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// CategoryLists splits the category actions into sorted allow and block
// lists.
func (c UrlFilterConfig) CategoryLists() (allow, block []string) {
	actions := c.EffectiveCategoryActions()
	for _, name := range c.categoryNames() {
		switch actions[name] {
		case CategoryActionAllow:
			allow = append(allow, name)
		case CategoryActionBlock:
			block = append(block, name)
		}
	}
	return allow, block
}

// ---- BlockPageConfig ----

type BlockPageConfig struct {
	Template string `json:"template"`
	Message  string `json:"message"`
}

func NewBlockPageConfig() BlockPageConfig {
	return BlockPageConfig{Template: "default"}
}

type blockPageConfigAlias BlockPageConfig

func (c *BlockPageConfig) UnmarshalJSON(data []byte) error {
	*c = NewBlockPageConfig()
	return json.Unmarshal(data, (*blockPageConfigAlias)(c))
}

// ---- ScheduleConfig defaults wiring ----

func NewScheduleConfig() ScheduleConfig {
	return ScheduleConfig{ActiveWindows: []TimeWindow{}}
}

type scheduleConfigAlias ScheduleConfig

func (c *ScheduleConfig) UnmarshalJSON(data []byte) error {
	*c = NewScheduleConfig()
	if err := json.Unmarshal(data, (*scheduleConfigAlias)(c)); err != nil {
		return err
	}
	for i := range c.ActiveWindows {
		c.ActiveWindows[i].Normalize()
	}
	return nil
}

// ---- Policy ----

type Policy struct {
	Name            string                `json:"name"`
	Inactive        bool                  `json:"inactive"`
	SourceIPs       []string              `json:"source_ips"`
	SourceMACs      []string              `json:"source_macs"`
	Schedule        ScheduleConfig        `json:"schedule"`
	Doh             DohConfig             `json:"doh"`
	TextClassifier  TextClassifierConfig  `json:"text_classifier"`
	ImageClassifier ImageClassifierConfig `json:"image_classifier"`
	SafeSearch      SafeSearchConfig      `json:"safesearch"`
	YouTube         YouTubeConfig         `json:"youtube"`
	Mitm            MitmConfig            `json:"mitm"`
	UrlFilter       UrlFilterConfig       `json:"url_filter"`
	BlockPage       BlockPageConfig       `json:"block_page"`
}

// NewPolicy returns a Policy with every sub-config at its documented
// default - equivalent to Pydantic's Policy() with only `name` supplied.
func NewPolicy() Policy {
	return Policy{
		SourceIPs:       []string{},
		SourceMACs:      []string{},
		Schedule:        NewScheduleConfig(),
		Doh:             NewDohConfig(),
		TextClassifier:  NewTextClassifierConfig(),
		ImageClassifier: NewImageClassifierConfig(),
		SafeSearch:      NewSafeSearchConfig(),
		YouTube:         NewYouTubeConfig(),
		Mitm:            NewMitmConfig(),
		UrlFilter:       NewUrlFilterConfig(),
		BlockPage:       NewBlockPageConfig(),
	}
}

type policyAlias Policy

func (p *Policy) UnmarshalJSON(data []byte) error {
	*p = NewPolicy()
	if err := json.Unmarshal(data, (*policyAlias)(p)); err != nil {
		return err
	}
	// Normalize source_macs: canonicalize, drop unparseable entries -
	// mirrors the Pydantic field_validator("source_macs").
	normalized := make([]string, 0, len(p.SourceMACs))
	for _, m := range p.SourceMACs {
		if n := macutil.Normalize(m); n != "" {
			normalized = append(normalized, n)
		}
	}
	p.SourceMACs = normalized
	return nil
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && isSpace(s[start]) {
		start++
	}
	for end > start && isSpace(s[end-1]) {
		end--
	}
	return s[start:end]
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

func decodeJSONFloat(raw json.RawMessage) (float64, error) {
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return f, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, err
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, err
	}
	return f, nil
}

func decodeJSONInt(raw json.RawMessage) (int, error) {
	var i int
	if err := json.Unmarshal(raw, &i); err == nil {
		return i, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, err
	}
	i, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, err
	}
	return i, nil
}
