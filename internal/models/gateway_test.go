package models

import (
	"encoding/json"
	"reflect"
	"testing"
)

// A settings.json written before gateway mode existed must load with the
// documented defaults rather than a zero value - an empty intercept list would
// silently disable interception on a host whose config says gateway is on.
func TestGatewayConfigDefaultsWhenAbsent(t *testing.T) {
	var s GlobalSettings
	if err := json.Unmarshal([]byte(`{"mgmt_port": 8000}`), &s); err != nil {
		t.Fatalf("Unmarshal() = %v", err)
	}
	if s.Gateway.Enabled {
		t.Error("gateway is enabled by default; it must be opt-in")
	}
	if !reflect.DeepEqual(s.Gateway.InterceptPorts, []int{80, 443}) {
		t.Errorf("InterceptPorts = %v, want [80 443]", s.Gateway.InterceptPorts)
	}
	if !s.Gateway.DropQUIC {
		t.Error("DropQUIC defaults off; QUIC would then bypass the filter entirely")
	}
	if !s.Gateway.IPForward {
		t.Error("IPForward defaults off; unintercepted traffic would black-hole")
	}
}

// Every sub-config resets to defaults before overlaying, so a partial block
// must not leave the port list empty.
func TestGatewayConfigPartialBlockKeepsDefaults(t *testing.T) {
	var s GlobalSettings
	if err := json.Unmarshal([]byte(`{"gateway":{"enabled":true,"interface":" eth0 "}}`), &s); err != nil {
		t.Fatalf("Unmarshal() = %v", err)
	}
	if !s.Gateway.Enabled {
		t.Fatal("enabled did not survive")
	}
	if s.Gateway.Interface != "eth0" {
		t.Errorf("Interface = %q, want it trimmed to eth0", s.Gateway.Interface)
	}
	if !reflect.DeepEqual(s.Gateway.InterceptPorts, []int{80, 443}) {
		t.Errorf("InterceptPorts = %v, want the defaults restored", s.Gateway.InterceptPorts)
	}
	if len(s.Gateway.BypassCIDRs) == 0 {
		t.Error("BypassCIDRs was emptied by a partial block")
	}
}

func TestGatewayConfigRoundTrips(t *testing.T) {
	in := NewGatewayConfig()
	in.Enabled = true
	in.Interface = "enp0s3"
	in.ClientCIDRs = []string{"192.168.12.0/24"}
	in.InterceptPorts = []int{80, 443, 8080}

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal() = %v", err)
	}
	var out GatewayConfig
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal() = %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round trip changed the config:\n got %+v\nwant %+v", out, in)
	}
}
