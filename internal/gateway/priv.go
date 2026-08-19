package gateway

import "github.com/yjlion/gowebfilter/internal/netpriv"

// HasNetAdmin reports whether this process can install the nftables ruleset.
// Exported so `webfilter gateway cleanup` can refuse loudly instead of running
// nft, watching it fail, and reporting success.
func HasNetAdmin() (bool, string) { return netpriv.Current() }
