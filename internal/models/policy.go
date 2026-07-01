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

type TextClassifierConfig struct {
	Enabled     bool     `json:"enabled"`
	Threshold   float64  `json:"threshold"`
	Exclude     []string `json:"exclude"`
	IncludeOnly []string `json:"include_only"`
}

func NewTextClassifierConfig() TextClassifierConfig {
	return TextClassifierConfig{
		Threshold:   0.80,
		Exclude:     []string{},
		IncludeOnly: []string{},
	}
}

type textClassifierConfigAlias TextClassifierConfig

func (c *TextClassifierConfig) UnmarshalJSON(data []byte) error {
	*c = NewTextClassifierConfig()
	return json.Unmarshal(data, (*textClassifierConfigAlias)(c))
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
	return json.Unmarshal(data, (*imageClassifierConfigAlias)(c))
}

// ---- SafeSearch ----

// SafeSearchEngines lists the engines configurable per-policy; also used to
// migrate the legacy flat schema (global block_*_tab flags) into the
// engines map, one entry per known engine.
var SafeSearchEngines = []string{"google", "bing", "duckduckgo", "yahoo", "youtube"}

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

type UrlFilterConfig struct {
	Enabled    bool          `json:"enabled"`
	Allow      []string      `json:"allow"`
	Block      []string      `json:"block"`
	Mode       UrlFilterMode `json:"mode"`
	Categories []string      `json:"categories"`
	BlockQuic  bool          `json:"block_quic"`
}

func NewUrlFilterConfig() UrlFilterConfig {
	return UrlFilterConfig{
		Allow:      []string{},
		Block:      []string{},
		Mode:       UrlFilterModeBlacklist,
		Categories: []string{},
	}
}

type urlFilterConfigAlias UrlFilterConfig

func (c *UrlFilterConfig) UnmarshalJSON(data []byte) error {
	*c = NewUrlFilterConfig()
	return json.Unmarshal(data, (*urlFilterConfigAlias)(c))
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
