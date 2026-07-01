package models

import (
	"encoding/json"
	"path/filepath"
)

// GlobalSettings mirrors config/settings.json (shared/models.py's
// GlobalSettings). WireGuard listen mode is intentionally out of scope for
// this port (see project plan); proxy_listen parsing still tolerates a
// "wireguard@" prefix without erroring so an old settings.json with a
// leftover wireguard entry doesn't fail to load - that entry is simply
// skipped when starting listeners.
type GlobalSettings struct {
	ProxyListen []string `json:"proxy_listen"`
	MgmtHost    string   `json:"mgmt_host"`
	MgmtPort    int      `json:"mgmt_port"`

	CertDir       string `json:"cert_dir"`
	PoliciesDir   string `json:"policies_dir"`
	CategoriesDir string `json:"categories_dir"`
	UILanguage    string `json:"ui_language"`
	LogsDir       string `json:"logs_dir"`

	LogBlocks        bool `json:"log_blocks"`
	LogRequests      bool `json:"log_requests"`
	LogRetentionDays int  `json:"log_retention_days"`

	DefaultPolicy *string `json:"default_policy"`

	AuthEnabled  bool   `json:"auth_enabled"`
	PasswordHash string `json:"password_hash"`
	SecretKey    string `json:"secret_key"`

	PacProxyHost   string   `json:"pac_proxy_host"`
	PacDirectHosts []string `json:"pac_direct_hosts"`
	PacDirectIPs   []string `json:"pac_direct_ips"`

	MgmtHostname   string `json:"mgmt_hostname"`
	MgmtHostnameIP string `json:"mgmt_hostname_ip"`

	UpstreamProxy string `json:"upstream_proxy"`
	UpstreamAuth  string `json:"upstream_auth"`

	ProxyAuthEnabled      bool   `json:"proxy_auth_enabled"`
	ProxyAuthUsername     string `json:"proxy_auth_username"`
	ProxyAuthPasswordHash string `json:"proxy_auth_password_hash"`

	// ImageClassifierModelPath is a Go-port-only optional field (documented
	// deviation): path to the NudeNet-compatible ONNX model file, not
	// present in the Python schema. Defaults to a sibling "models/" dir
	// next to the executable if empty; round-trips harmlessly through the
	// Python original since unrecognized settings.json fields aren't
	// validated against there.
	ImageClassifierModelPath string `json:"image_classifier_model_path,omitempty"`

	// OuiPath is a Go-port-only optional field (documented deviation): path
	// to the IEEE OUI vendor lookup table (internal/neighbors.DefaultOuiPath
	// - "./data/oui.txt" - when empty), refreshed by `webfilter oui update`.
	// The Python original hardcodes shared/data/oui.txt instead of making it
	// configurable; round-trips harmlessly through it since unrecognized
	// settings.json fields aren't validated against there.
	OuiPath string `json:"oui_path,omitempty"`
}

// NewGlobalSettings returns GlobalSettings with every field at its
// documented Python default.
func NewGlobalSettings() GlobalSettings {
	return GlobalSettings{
		ProxyListen:      []string{"0.0.0.0:8080"},
		MgmtHost:         "0.0.0.0",
		MgmtPort:         8000,
		CertDir:          "./certs",
		PoliciesDir:      "./policies",
		CategoriesDir:    "./categories",
		UILanguage:       "en",
		LogsDir:          "./logs",
		LogBlocks:        true,
		LogRequests:      true,
		LogRetentionDays: 30,
		PacDirectHosts:   []string{},
		PacDirectIPs:     []string{},
		MgmtHostname:     "web.filter",
	}
}

type globalSettingsAlias GlobalSettings

func (s *GlobalSettings) UnmarshalJSON(data []byte) error {
	*s = NewGlobalSettings()
	if err := json.Unmarshal(data, (*globalSettingsAlias)(s)); err != nil {
		return err
	}
	s.migrateLegacy(data)
	if len(s.ProxyListen) == 0 {
		s.ProxyListen = []string{"0.0.0.0:8080"}
	} else {
		cleaned := make([]string, 0, len(s.ProxyListen))
		for _, v := range s.ProxyListen {
			if t := trimSpace(v); t != "" {
				cleaned = append(cleaned, t)
			}
		}
		if len(cleaned) == 0 {
			cleaned = []string{"0.0.0.0:8080"}
		}
		s.ProxyListen = cleaned
	}
	return nil
}

// legacySettingsFields captures the pre-proxy_listen flat schema
// (proxy_port + listen_host) and the renamed blocks_log_path field, so an
// old settings.json (from before these fields existed) still loads.
type legacySettingsFields struct {
	ProxyPort     *int    `json:"proxy_port"`
	ListenHost    *string `json:"listen_host"`
	BlocksLogPath *string `json:"blocks_log_path"`
}

func (s *GlobalSettings) migrateLegacy(data []byte) {
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil {
		return
	}
	var legacy legacySettingsFields
	_ = json.Unmarshal(data, &legacy)

	if _, hasProxyListen := raw["proxy_listen"]; !hasProxyListen && legacy.ProxyPort != nil {
		host := "0.0.0.0"
		if legacy.ListenHost != nil {
			host = *legacy.ListenHost
		}
		s.ProxyListen = []string{host + ":" + itoa(*legacy.ProxyPort)}
	}
	if _, hasMgmtHost := raw["mgmt_host"]; !hasMgmtHost && legacy.ListenHost != nil {
		s.MgmtHost = *legacy.ListenHost
	}
	if _, hasLogsDir := raw["logs_dir"]; !hasLogsDir && legacy.BlocksLogPath != nil {
		s.LogsDir = filepath.Dir(*legacy.BlocksLogPath)
	}
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

// DBPath returns the SQLite log database path derived from LogsDir.
func (s GlobalSettings) DBPath() string {
	return filepath.Join(s.LogsDir, "webfilter.db")
}

// PrimaryProxyPort extracts the first proxy_listen port whose mode is
// "regular" or "socks5" (the two modes that bind a TCP port a client
// connects a browser/OS proxy setting to). Returns 8080 if none found.
func (s GlobalSettings) PrimaryProxyPort() int {
	for _, entry := range s.ProxyListen {
		mode, _, port := ParseListen(entry)
		if (mode == "regular" || mode == "socks5") && port != 0 {
			return port
		}
	}
	return 8080
}
