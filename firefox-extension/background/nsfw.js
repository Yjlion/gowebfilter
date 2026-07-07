// NSFW image scoring: the same GantMan/nsfw_model MobileNetV2 (224x224,
// [0,1] RGB input) the Go proxy embeds, here in its original TF.js form
// (vendor/models/mobilenet_v2/, extracted from the nsfwjs npm package — see
// NOTICE), executed by TF.js. The class list, the combined-score formula
// and the skin-ratio gate are ported from internal/classify/image/
// (detector.go, skinprefilter.go) so both products block the same images.

"use strict";

// Classes, in model output order (same as detector.go's Scores).
// nsfw = Porn + Hentai + SEXY_WEIGHT*Sexy; Drawings and Neutral are safe;
// sexy is weighted down since it's revealing rather than explicit.
const SEXY_WEIGHT = 0.5;

// Minimum skin-region ratio below which the classifier is skipped entirely
// (score 0). Logos, screenshots, scenery and most product shots have ~0%
// skin and never pay for inference. Same constant as detector.go.
const PREFILTER_SKIN_RATIO = 0.07;

// Raster size the skin heuristic runs at (skinprefilter.go's analysisDim).
const SKIN_ANALYSIS_DIM = 192;

const MODEL_INPUT_DIM = 224;

let modelPromise = null;

function loadNsfwModel() {
  if (!modelPromise) {
    modelPromise = tf
      .loadLayersModel(browser.runtime.getURL("vendor/models/mobilenet_v2/model.json"))
      .then((model) => {
        // Warm up so the first real classification isn't slow.
        tf.tidy(() => model.predict(tf.zeros([1, MODEL_INPUT_DIM, MODEL_INPUT_DIM, 3])));
        return model;
      })
      .catch((err) => {
        modelPromise = null; // allow retry
        throw err;
      });
  }
  return modelPromise;
}

// isSkin classifies one pixel — ported verbatim from skinprefilter.go's
// isSkin (two RGB rules gated by a YCbCr chroma window).
function isSkinPixel(r, g, b) {
  const maxc = Math.max(r, g, b);
  const minc = Math.min(r, g, b);

  // Daylight rule.
  const a = r > 95 && g > 40 && b > 20 && maxc - minc > 15 && Math.abs(r - g) > 15 && r > g && r > b;
  // Relaxed rule for darker skin / dimmer lighting.
  const d = r > 60 && g > 30 && b > 15 && r > g && r > b && Math.abs(r - g) >= 10;
  if (!a && !d) return false;

  // YCbCr chroma gate (JPEG coefficients).
  const y = 0.299 * r + 0.587 * g + 0.114 * b;
  const cb = 128 - 0.168736 * r - 0.331264 * g + 0.5 * b;
  const cr = 128 + 0.5 * r - 0.418688 * g - 0.081312 * b;
  return y > 30 && cb >= 75 && cb <= 130 && cr >= 131 && cr <= 178;
}

function skinRatio(imageData) {
  const px = imageData.data;
  const total = imageData.width * imageData.height;
  if (total === 0) return 0;
  let skin = 0;
  for (let i = 0; i < px.length; i += 4) {
    if (isSkinPixel(px[i], px[i + 1], px[i + 2])) skin++;
  }
  return skin / total;
}

function drawScaled(bitmap, maxDim) {
  let w = bitmap.width;
  let h = bitmap.height;
  if (w >= h && w > maxDim) {
    h = Math.max(1, Math.round((h * maxDim) / w));
    w = maxDim;
  } else if (h > w && h > maxDim) {
    w = Math.max(1, Math.round((w * maxDim) / h));
    h = maxDim;
  }
  const canvas = new OffscreenCanvas(w, h);
  const ctx = canvas.getContext("2d", { willReadFrequently: true });
  ctx.drawImage(bitmap, 0, 0, w, h);
  return ctx.getImageData(0, 0, w, h);
}

// scoreImageBitmap returns the combined NSFW score for a decoded image,
// mirroring detector.Score: skin gate first, CNN only when it passes.
async function scoreImageBitmap(bitmap) {
  const skinData = drawScaled(bitmap, SKIN_ANALYSIS_DIM);
  if (skinRatio(skinData) < PREFILTER_SKIN_RATIO) {
    return 0;
  }

  const model = await loadNsfwModel();

  // Bilinear resize to 224x224, [0,1] RGB — same preprocessing as
  // detector.go's predict (the graph does its own [-1,1] normalization).
  const canvas = new OffscreenCanvas(MODEL_INPUT_DIM, MODEL_INPUT_DIM);
  const ctx = canvas.getContext("2d", { willReadFrequently: true });
  ctx.drawImage(bitmap, 0, 0, MODEL_INPUT_DIM, MODEL_INPUT_DIM);
  const imageData = ctx.getImageData(0, 0, MODEL_INPUT_DIM, MODEL_INPUT_DIM);

  const probs = tf.tidy(() => {
    const x = tf.browser.fromPixels(imageData).toFloat().div(255).expandDims(0);
    return model.predict(x);
  });
  try {
    const [drawings, hentai, neutral, porn, sexy] = await probs.data();
    void drawings;
    void neutral;
    return porn + hentai + SEXY_WEIGHT * sexy;
  } finally {
    probs.dispose();
  }
}

// scoreImageUrl fetches and scores one image. Fetching from the background
// (with host permissions) sidesteps the CORS canvas-tainting that stops
// content scripts from reading cross-origin pixels; the HTTP cache usually
// makes the second fetch cheap. Returns {score, ok}.
async function scoreImageUrl(url) {
  try {
    const resp = await fetch(url, { credentials: "omit", cache: "force-cache" });
    if (!resp.ok) return { score: 0, ok: false };
    const blob = await resp.blob();
    const bitmap = await createImageBitmap(blob);
    try {
      return { score: await scoreImageBitmap(bitmap), ok: true };
    } finally {
      bitmap.close();
    }
  } catch (err) {
    return { score: 0, ok: false };
  }
}
