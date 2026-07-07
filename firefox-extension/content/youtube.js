// YouTube cleanup content script — the DOM-level counterpart of the proxy's
// youtube_filter addon (which rewrites InnerTube JSON responses). Same
// policy knobs: blocked/allowed channel lists (blacklist or whitelist
// mode), hide the home feed, remove comments, remove recommendations.
//
// Channels can be listed the same three ways the Go addon accepts
// (channelListed in youtube_filter.go): a UC... channel id, an @handle, or
// a display name (case-insensitive).

"use strict";

(() => {
  let cfg = null;
  let lastHref = "";

  const norm = (s) => String(s || "").trim().toLowerCase();

  function channelListed(channelId, handle, name) {
    const id = norm(channelId);
    const h = norm(handle).replace(/^@/, "");
    const n = norm(name);
    for (const raw of cfg.channels) {
      const entry = norm(raw);
      if (!entry) continue;
      if (entry.startsWith("uc") && entry === id) return true;
      if (entry.startsWith("@") && entry.slice(1) === h) return true;
      if (entry === n || entry === h) return true;
    }
    return false;
  }

  function channelBlocked(channelId, handle, name) {
    const listed = channelListed(channelId, handle, name);
    return cfg.mode === "whitelist" ? !listed : listed;
  }

  // --- current page's channel identity (watch + channel pages) -------------

  function currentChannel() {
    // Watch pages: the owner block under the player.
    const ownerLink = document.querySelector(
      "ytd-video-owner-renderer ytd-channel-name a, #owner ytd-channel-name a"
    );
    // Channel pages: canonical link + microdata carry the id.
    const canonical = document.querySelector('link[rel="canonical"]')?.href || "";
    const metaId = document.querySelector('meta[itemprop="identifier"]')?.content || "";

    let channelId = metaId;
    let handle = "";
    let name = ownerLink?.textContent || "";

    for (const href of [ownerLink?.href || "", canonical]) {
      const mh = /\/@([^/?#]+)/.exec(href);
      if (mh && !handle) handle = mh[1];
      const mc = /\/channel\/(UC[\w-]+)/.exec(href);
      if (mc && !channelId) channelId = mc[1];
    }
    if (!name) {
      name = document.querySelector("yt-dynamic-text-view-model h1, #channel-name #text")?.textContent || "";
    }
    return { channelId, handle, name };
  }

  function blockOverlay(label) {
    if (document.getElementById("webfilter-yt-block")) return;
    // Stop playback before covering the page.
    for (const v of document.querySelectorAll("video")) {
      try {
        v.pause();
        v.src = "";
      } catch {}
    }
    const div = document.createElement("div");
    div.id = "webfilter-yt-block";
    div.style.cssText =
      "position:fixed;inset:0;z-index:2147483647;background:#fff;display:flex;" +
      "flex-direction:column;align-items:center;justify-content:center;" +
      "font-family:system-ui,sans-serif;text-align:center;padding:2em;";
    const h = document.createElement("h1");
    h.textContent = "Content blocked";
    const p = document.createElement("p");
    p.textContent = `YouTube channel ${label} is blocked by your WebFilter policy.`;
    div.append(h, p);
    document.documentElement.appendChild(div);
  }

  function unblockOverlay() {
    document.getElementById("webfilter-yt-block")?.remove();
  }

  // --- element hiding --------------------------------------------------------

  const HIDE_STYLE_ID = "webfilter-yt-style";

  function applyHideRules() {
    const path = location.pathname;
    const selectors = [];
    if (cfg.removeComments) {
      selectors.push("ytd-comments#comments", "#comments.ytd-item-section-renderer");
    }
    if (cfg.removeRecommendations && path.startsWith("/watch")) {
      selectors.push("#related", "#secondary ytd-watch-next-secondary-results-renderer");
    }
    if (cfg.blockHome && (path === "/" || path === "/index")) {
      selectors.push(
        "ytd-rich-grid-renderer",
        "ytd-browse[page-subtype='home'] #contents"
      );
    }

    let style = document.getElementById(HIDE_STYLE_ID);
    if (!selectors.length) {
      style?.remove();
      return;
    }
    if (!style) {
      style = document.createElement("style");
      style.id = HIDE_STYLE_ID;
      document.documentElement.appendChild(style);
    }
    style.textContent = `${selectors.join(",")} { display: none !important; }`;
  }

  // --- main loop --------------------------------------------------------------

  function evaluate() {
    applyHideRules();

    const path = location.pathname;
    const onWatch = path.startsWith("/watch") || path.startsWith("/shorts/");
    const onChannel = path.startsWith("/@") || path.startsWith("/channel/") || path.startsWith("/c/");
    if (!onWatch && !onChannel) {
      unblockOverlay();
      return;
    }
    if (cfg.channels.length === 0 && cfg.mode !== "whitelist") return;

    const { channelId, handle, name } = currentChannel();
    if (!channelId && !handle && !name) return; // identity not rendered yet; observer retries
    if (channelBlocked(channelId, handle, name)) {
      blockOverlay(handle ? `@${handle}` : name || channelId);
    } else {
      unblockOverlay();
    }
  }

  // Single re-evaluation entry point for both the SPA navigation event and
  // the mutation observer: reset overlay state on a route change, then
  // evaluate once.
  function onNavigate() {
    if (location.href !== lastHref) {
      lastHref = location.href;
      unblockOverlay();
    }
    evaluate();
  }

  browser.runtime
    .sendMessage({ type: "getState" })
    .then((state) => {
      if (!state || !state.active || !state.youtube.enabled) return;
      cfg = state.youtube;
      lastHref = location.href;
      evaluate();
      // YouTube is a SPA: yt-navigate-finish fires on route changes; the
      // observer catches late-rendered owner info and injected sections.
      // Throttled — YouTube mutates the DOM constantly.
      window.addEventListener("yt-navigate-finish", onNavigate, true);
      let pending = false;
      new MutationObserver(() => {
        if (pending) return;
        pending = true;
        setTimeout(() => {
          pending = false;
          onNavigate();
        }, 500);
      }).observe(document.documentElement, { subtree: true, childList: true });
    })
    .catch(() => {});
})();
