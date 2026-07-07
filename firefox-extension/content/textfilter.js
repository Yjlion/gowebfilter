// Adult-text filtering content script (runs at document_idle).
//
// Counterpart of the proxy's text_classifier addon: the page's visible text
// is sent to the background page, which runs the same keyword prefilter and
// Bayesian scorer the Go proxy embeds (see background/bayes.js). On a block
// verdict the document is replaced in place with a block page — matching the
// proxy's behavior of returning an HTTP 200 block-page body rather than an
// error status.
//
// Divergence from the proxy, on purpose: the proxy strips tags from the raw
// HTML (script/style contents included); here `innerText` supplies only
// user-visible text, which strictly reduces false positives. Single-pass:
// like the proxy (one scan per HTML response), SPA route changes after the
// initial load are not re-scanned.

"use strict";

(() => {
  function renderBlockPage(reason) {
    window.stop();
    document.documentElement.innerHTML = "";
    const style = { fontFamily: "system-ui, sans-serif", textAlign: "center", marginTop: "15vh" };
    const wrap = document.createElement("div");
    Object.assign(wrap.style, style);
    const h1 = document.createElement("h1");
    h1.textContent = "Content blocked";
    const p = document.createElement("p");
    p.textContent = "This content has been blocked by your WebFilter policy.";
    const small = document.createElement("small");
    small.style.color = "#888";
    small.textContent = `reason: text_classifier (${reason})`;
    wrap.append(h1, p, small);
    document.documentElement.appendChild(wrap);
  }

  async function run() {
    const text = document.body ? document.body.innerText : "";
    if (!text) return;
    let verdict;
    try {
      verdict = await browser.runtime.sendMessage({ type: "scoreText", text });
    } catch {
      return;
    }
    if (verdict && verdict.blocked) {
      renderBlockPage(verdict.reason);
    }
  }

  browser.runtime
    .sendMessage({ type: "getState" })
    .then((state) => {
      if (!state || !state.active || !state.textClassifier.enabled) return;
      run();
    })
    .catch(() => {});
})();
