"use strict";

// The DNR redirect carries the blocking component in ?reason= (mirrors the
// proxy's block log "component" field).
const reason = new URLSearchParams(location.search).get("reason");
if (reason) {
  document.getElementById("reason").textContent = `reason: ${reason}`;
}
