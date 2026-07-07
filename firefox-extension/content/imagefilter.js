// NSFW image filtering content script (runs at document_start).
//
// Counterpart of the proxy's image_classifier addon, adapted to the DOM:
// instead of rewriting responses, every <img> is blurred the moment it
// appears and only un-blurred once the background page's classifier clears
// it ("blur until proven safe"). Scoring happens in the background — it
// re-fetches the image with host permissions, which sidesteps the CORS
// canvas-tainting that stops content scripts from reading cross-origin
// pixels, and it holds the single TF.js model instance.
//
// This also covers Google Images' inline base64 `data:image/...` grid
// naturally: those data URIs are <img> srcs here, and the background can
// fetch data: URLs like any other. (The proxy needs filterInlineImages to
// rewrite them inside HTML; at the DOM layer they're just images.)

"use strict";

(() => {
  const PENDING = "webfilter-pending";
  const BLURRED = "webfilter-blurred";
  const REMOVED = "webfilter-removed";

  let cfg = null; // {threshold, action, minSize} once enabled, else null
  const scanned = new WeakMap(); // img -> last classified URL

  function currentUrl(img) {
    return img.currentSrc || img.src || "";
  }

  function release(img) {
    img.classList.remove(PENDING);
  }

  // classify always ends by resolving the pending blur one way or another:
  // release for safe/small/broken/already-scanned images, a verdict class
  // for filtered ones. (An already-scanned image can re-enter here when DOM
  // reparenting re-runs scanTree — its earlier verdict class survives on
  // the element, so releasing the pending blur is the correct resolution.)
  async function classify(img) {
    const url = currentUrl(img);
    if (!url || scanned.get(img) === url) {
      release(img);
      return;
    }
    img.classList.add(PENDING);

    // Size prefilter: icons and pixels aren't worth inference.
    if (img.naturalWidth === 0 && img.naturalHeight === 0) {
      release(img); // broken image
      return;
    }
    if (img.naturalWidth < cfg.minSize && img.naturalHeight < cfg.minSize) {
      scanned.set(img, url);
      release(img);
      return;
    }

    scanned.set(img, url);
    let verdict;
    try {
      verdict = await browser.runtime.sendMessage({ type: "classifyImage", url });
    } catch {
      verdict = null;
    }
    if (currentUrl(img) !== url) return; // src changed while scoring; observer re-queues

    release(img);
    if (!verdict || !verdict.ok) return; // fail open, like the proxy's Score(ok=false)
    if (verdict.filtered) {
      img.classList.add(verdict.action === "block" ? REMOVED : BLURRED);
    } else {
      img.classList.remove(BLURRED, REMOVED);
    }
  }

  function watch(img) {
    if (img.complete) {
      classify(img);
      return;
    }
    // Natural size is only known once the image loads; keep the pending
    // blur up and re-examine on the load event.
    img.classList.add(PENDING);
    img.addEventListener("load", () => classify(img), { once: true });
    img.addEventListener("error", () => release(img), { once: true });
  }

  function scanTree(root) {
    if (root.nodeType !== Node.ELEMENT_NODE) return;
    if (root.tagName === "IMG") watch(root);
    for (const img of root.querySelectorAll("img")) watch(img);
  }

  function start() {
    scanTree(document.documentElement);
    // Catch lazy-loaded and dynamically inserted media (infinite scroll).
    new MutationObserver((muts) => {
      for (const m of muts) {
        if (m.type === "attributes") {
          if (m.target.tagName === "IMG") {
            scanned.delete(m.target);
            watch(m.target);
          }
          continue;
        }
        for (const n of m.addedNodes) scanTree(n);
      }
    }).observe(document.documentElement, {
      subtree: true,
      childList: true,
      attributes: true,
      attributeFilter: ["src", "srcset"],
    });
  }

  browser.runtime
    .sendMessage({ type: "getState" })
    .then((state) => {
      if (!state || !state.active || !state.imageClassifier.enabled) return;
      cfg = state.imageClassifier;
      if (document.readyState === "loading") {
        document.addEventListener("DOMContentLoaded", start, { once: true });
        // Still scan whatever exists already so early images blur eagerly.
        scanTree(document.documentElement);
      } else {
        start();
      }
    })
    .catch(() => {});
})();
