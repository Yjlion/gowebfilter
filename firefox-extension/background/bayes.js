// Adult-text scoring, ported 1:1 from the Go proxy:
//  - keyword prefilter: internal/proxy/addons/text_classifier.go
//  - Bayesian scorer:   internal/classify/textbayes/model.go
// The parity test (test/bayes_parity.mjs) checks this port against scores
// produced by the Go implementation — keep the two in lockstep.

"use strict";

// --- keyword prefilter (text_classifier.go) -------------------------------

// Conservative, high-precision keyword pre-filter, same token list as the
// Go side's adultKeywordsRe.
const ADULT_KEYWORDS_RE = new RegExp(
  "\\b(porn|pornography|xxx|hentai|nude|naked|erotic|masturbat|orgasm|" +
    "penis|vagina|anal sex|oral sex|blowjob|handjob|gangbang|threesome|" +
    "escort service|cam girl|onlyfans|nsfw|adult content)\\b",
  "gi"
);

const MIN_KEYWORD_HITS = 3;

function keywordScore(text) {
  const m = text.match(ADULT_KEYWORDS_RE);
  const hits = m ? m.length : 0;
  return Math.min(hits / MIN_KEYWORD_HITS, 1.0);
}

// --- Bayesian scorer (textbayes/model.go) ---------------------------------

const TOKEN_RE = /[a-z0-9]+/g;

function normalizeToken(tok) {
  switch (tok) {
    case "cams":
      return "cam";
    case "pics":
      return "pic";
    case "photos":
      return "photo";
    case "videos":
      return "video";
    case "webcams":
      return "webcam";
  }
  if (tok.length > 4 && tok.endsWith("ies")) {
    return tok.slice(0, -3) + "y";
  }
  return tok;
}

function tokenize(s) {
  const raw = s.toLowerCase().match(TOKEN_RE) || [];
  return raw.map(normalizeToken);
}

function normalizePhrase(s) {
  return tokenize(s).join(" ");
}

class BayesModel {
  constructor(data) {
    if (!(data.adult_prior > 0) || !(data.safe_prior > 0)) {
      throw new Error("textbayes: priors must be positive");
    }
    if (!(data.adult_total > 0) || !(data.safe_total > 0)) {
      throw new Error("textbayes: totals must be positive");
    }
    if (!data.features || data.features.length === 0) {
      throw new Error("textbayes: feature table must not be empty");
    }
    this.adultPrior = data.adult_prior;
    this.safePrior = data.safe_prior;
    this.adultTotal = data.adult_total;
    this.safeTotal = data.safe_total;
    this.features = new Map();
    this.maxPhrase = 0;
    for (const f of data.features) {
      const key = normalizePhrase(f.text);
      if (key === "") continue;
      if (f.adult < 0 || f.safe < 0) {
        throw new Error(`textbayes: feature ${f.text} has negative count`);
      }
      this.features.set(key, { adult: f.adult, safe: f.safe });
      const n = key.split(" ").length;
      if (n > this.maxPhrase) this.maxPhrase = n;
    }
    if (this.features.size === 0) {
      throw new Error("textbayes: feature table normalized to empty");
    }
    this.vocabSize = this.features.size;
  }

  // score returns {score, ok}: a calibrated adult-content probability in
  // [0,1], ok=false when the text has no scoreable tokens at all.
  score(text) {
    const hits = this.extractFeatures(text);
    if (hits.length === 0) {
      if (tokenize(text).length === 0) {
        return { score: 0, ok: false };
      }
      return { score: this.adultPrior / (this.adultPrior + this.safePrior), ok: true };
    }

    let logAdult = Math.log(this.adultPrior);
    let logSafe = Math.log(this.safePrior);
    for (const hit of hits) {
      const f = this.features.get(hit);
      logAdult += Math.log((f.adult + 1) / (this.adultTotal + this.vocabSize));
      logSafe += Math.log((f.safe + 1) / (this.safeTotal + this.vocabSize));
    }
    if (logAdult >= logSafe) {
      return { score: 1 / (1 + Math.exp(logSafe - logAdult)), ok: true };
    }
    const r = Math.exp(logAdult - logSafe);
    return { score: r / (1 + r), ok: true };
  }

  extractFeatures(text) {
    const tokens = tokenize(text);
    if (tokens.length === 0) return [];
    const hits = [];
    const seen = new Map();
    for (let i = 0; i < tokens.length; i++) {
      const maxN = Math.min(this.maxPhrase, tokens.length - i);
      for (let n = maxN; n >= 1; n--) {
        const phrase = tokens.slice(i, i + n).join(" ");
        if (!this.features.has(phrase)) continue;
        // Let repeated adult evidence count, but cap repetition so a long
        // spam page cannot drive the score solely by duplication.
        const count = seen.get(phrase) || 0;
        if (count < 4) {
          hits.push(phrase);
          seen.set(phrase, count + 1);
        }
        break;
      }
    }
    return hits;
  }
}

// classifyText mirrors TextClassifier.HandleResponse's decision sequence:
// keyword prefilter first (3 hits block even tiny pages), then the
// 100-character floor, then the Bayesian score against the threshold.
function classifyText(model, text, threshold) {
  if (keywordScore(text) >= 1.0) {
    return { blocked: true, reason: "keywords" };
  }
  if (text.length < 100) {
    return { blocked: false };
  }
  const { score, ok } = model.score(text);
  if (ok && score >= threshold) {
    return { blocked: true, reason: "bayes", score };
  }
  return { blocked: false, score: ok ? score : undefined };
}
