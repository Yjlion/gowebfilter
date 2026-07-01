package proxy

import (
	"net/http"

	"github.com/yjlion/gowebfilter/internal/models"
	"github.com/yjlion/gowebfilter/internal/proxy/state"
)

// FlowContext carries one request/response pair through the addon
// pipeline, mirroring mitmproxy's flow.metadata dict. Request/Response
// mutations by any addon are visible to every addon that runs after it,
// exactly like the Python original's shared HTTPFlow object.
//
// ResponseBody is always the full buffered response body (Response.Body is
// never populated - the pipeline reads and closes the real body once, up
// front) since every response-hook addon that needs to inspect content
// (youtube_filter, text_classifier, image_classifier) needs the whole thing
// anyway, matching mitmproxy's own fully-buffered flow.response.text/
// raw_content semantics.
type FlowContext struct {
	Runtime  *state.Runtime
	ClientIP string
	// ClientConnID identifies the underlying TCP connection (one CONNECT
	// tunnel, or one plain-HTTP keep-alive connection) - mirrors
	// mitmproxy's flow.client_conn.id, used by proxy_auth to remember a
	// connection authenticated at the CONNECT stage.
	ClientConnID uint64
	// ProxySockName is the local address the client connected to (this
	// proxy's own address on that connection) - mirrors
	// flow.client_conn.sockname[0], used by management_access to build the
	// pseudo-domain redirect Location and to recognize management-port
	// traffic addressed to the proxy's own IP.
	ProxySockName string

	Request      *http.Request
	Response     *http.Response
	ResponseBody []byte

	// URLAllowed mirrors flow.metadata["url_allowed"]: an allow-list match
	// short-circuits every downstream filtering addon.
	URLAllowed bool
	// MitmPassthrough mirrors flow.metadata["mitm_passthrough"]: set for
	// MITM-include-mode non-listed sites and matching User-Agent rules, or
	// by ManagementAccess for the proxy's own management traffic.
	MitmPassthrough bool
	// WFAction/WFComponent mirror flow.metadata["wf_action"/"wf_component"]
	// - the final decision RequestLogger records ("ok"/"modified"/"blocked").
	WFAction    string
	WFComponent string
	// WFLogged mirrors flow.metadata["wf_logged"]: set once RequestLogger
	// has recorded this flow, so the error hook doesn't double-log a flow
	// that already got a response hook.
	WFLogged bool

	Policy *models.Policy
}

// Addon is the common interface every pipeline stage implements; concrete
// stages additionally implement RequestAddon, ResponseAddon, and/or
// ErrorAddon depending on which mitmproxy hooks their Python original
// registered.
type Addon interface {
	Name() string
}

// RequestAddon runs during the request phase, in pipeline order, for
// every flow - regardless of whether an earlier addon already set
// fc.Response. This mirrors mitmproxy's actual behavior: setting
// flow.response early skips the real upstream fetch but does NOT skip
// later addons' request() hooks, which is why several addons must guard
// on fc.URLAllowed/fc.MitmPassthrough themselves.
type RequestAddon interface {
	Addon
	HandleRequest(fc *FlowContext)
}

// ResponseAddon runs during the response phase, in pipeline order, once a
// response exists (from upstream or synthesized by a request-hook addon).
type ResponseAddon interface {
	Addon
	HandleResponse(fc *FlowContext)
}

// ErrorAddon runs when the upstream fetch itself failed (connection
// refused, DNS failure, etc.) - the response phase never runs in that
// case, mirroring mitmproxy's separate error() hook.
type ErrorAddon interface {
	Addon
	HandleError(fc *FlowContext)
}

// ConnectGate lets an addon gate a CONNECT tunnel before MITM/blind-splice
// begins. Only ProxyAuthGate implements this - mitmproxy fires a distinct
// http_connect hook for CONNECT requests, uniquely before the tunnel (and
// thus before any per-flow FlowContext) exists, so it isn't part of the
// ordinary Request/Response/Error pipeline. The engine calls this directly
// rather than through Pipeline.
type ConnectGate interface {
	Addon
	// AuthorizeConnect reports whether the CONNECT request (identified by
	// connID) may proceed. On success the addon should remember connID as
	// authorized so subsequent requests over the same tunnel aren't
	// re-challenged (mirrors ProxyAuthGate's _authed_conns).
	AuthorizeConnect(req *http.Request, connID uint64) bool
	// ClientDisconnected releases any per-connection state for connID,
	// called once the connection (CONNECT tunnel or plain-HTTP
	// connection) closes - mirrors client_disconnected.
	ClientDisconnected(connID uint64)
}
