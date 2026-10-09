# Text classifier: blocking and censoring

The `text_classifier` addon looks at the text of HTML pages. It can **block**
adult pages, **censor** offensive words in place, or **both**. Everything it
needs is embedded in the binary: no model download, no native runtime.

```json
"text_classifier": {
  "enabled": true,
  "mode": "both",
  "threshold": 0.8,
  "censor_words": ["frak"],
  "censor_languages": [],
  "exclude": [],
  "include_only": []
}
```

| Field | Meaning |
|---|---|
| `enabled` | Off by default. NSFW false positives have a real cost, so this is opt-in. |
| `mode` | `block` (default, the original behaviour), `censor` (never blocks), or `both`. Unknown values read as `block`. |
| `threshold` | Score at or above which a page is blocked (block/both modes). |
| `censor_words` | Extra words or phrases to mask, in any language, on top of the built-in lists. |
| `censor_languages` | Pin the Latin-script languages the censor uses (`["en","de"]`). Empty = automatic (below). |
| `exclude` / `include_only` | Site patterns, as in the URL filter. |

Only `text/html` responses are inspected.

## Block mode

Two stages, unchanged from before except that they now understand
non-English text:

1. **Keyword prefilter**: three hits from a short, high-precision English
   list (`porn`, `hentai`, `xxx`, ...) block the page, even a tiny one.
2. **Bayesian scorer** (`internal/classify/textbayes`): a Naive Bayes model
   over an embedded feature table, compared against `threshold`. Pages with
   under 100 characters of text are not scored, because a few words are too
   little evidence.

Script and style contents are stripped before scoring; they are code, not
prose.

### What the model knows

`model_data.json` (version 2) has two layers, built by
`scripts/build_text_bayes_model.go`:

- **47 curated, high-weight features** (`scripts/text_bayes_curated.json`):
  explicit adult phrases ("free porn", "live sex", "onlyfans", ...).
- **~2,370 light-weight features** from every
  [LDNOOBW](https://github.com/LDNOOBW/List-of-Dirty-Naughty-Obscene-and-Otherwise-Bad-Words)
  language list: Arabic, Chinese, Czech, Danish, Dutch, English, Esperanto,
  Filipino, Finnish, French (incl. Québécois), German, Hindi (romanized),
  Hungarian, Italian, Japanese, Kabyle, Klingon, Korean, Norwegian, Persian,
  Polish, Portuguese, Russian, Spanish, Swedish, Thai and Turkish.

LDNOOBW is a general **profanity** list (insults, slurs, swearing), not an
adult-content list. Its entries are therefore weak evidence. Each hit moves
the log-odds by about 0.75, so a page needs roughly seven distinct hits
before swearing alone reaches 0.8. Tests pin this: ordinary angry
paragraphs in German, Spanish, English and Russian stay below the default
threshold, while dense explicit vocabulary in Japanese, Chinese, Korean and
Russian scores clearly above neutral text in the same language. Short
Latin-script entries (under five letters) are left out of the model,
because they collide with everyday words in other languages ("am", "pot",
"fan").

Regenerate after editing the curated layer or refreshing the lists:

```bash
go run scripts/build_text_bayes_model.go
go test ./internal/classify/textbayes
```

## Censor mode

Censoring replaces each letter of a matched word with `*` in the page's
**text nodes only**:

```
This shit printer broke again.   ->   This **** printer broke again.
看三级片吧                         ->   看***吧
```

Never touched: tags and attributes (URLs, class names), comments, and the
contents of `<script>`, `<style>`, `<textarea>`, `<noscript>`, `<template>`,
`<code>`, `<pre>` and similar. A page with no match is passed through
byte-for-byte. A censored page is logged as action `modified`, component
`text_classifier`.

In `both` mode the block decision runs first, on the original text. A page
that isn't blocked is then censored.

### Which words, which languages

Matching works per script:

- **Space-delimited scripts** (Latin, Cyrillic, Arabic, Hangul,
  Devanagari, ...) match whole words only: "class" never matches "ass".
  Multi-word entries match across any run of spaces and punctuation.
- **Han, Kana, Thai, Lao, Khmer and Myanmar** have no spaces between
  words, so their entries match as substrings. To keep that from masking
  everyday words, entries shorter than 2 letters (Han/Kana) or 3 letters
  (Thai etc.) are dropped. 乳 alone would mask "dairy" and ขี้ would mask
  "lazy".

**Latin-script words are language-scoped.** The same short string is
profane in one language and ordinary in another, so Latin-script lists are
loaded only for the page's language:

1. `censor_languages` from the policy, when set;
2. otherwise the element's own `lang` attribute (a
   `<blockquote lang="de">` on an English page is censored as German), then
   `<html lang>`, then the `Content-Language` header;
3. otherwise English.

Words in every other script are always checked, whatever the page language.

A few entries that are ordinary words in their own language ("asesinato",
Dutch "pot", Swedish "sås", ...) and purely numeric entries ("13.") are
filtered out at load time. See the `skipped` map in
`internal/classify/profanity/profanity.go`. The raw lists under
`internal/classify/profanity/words/` are an unmodified upstream snapshot,
so they can be refreshed with a plain copy (commit recorded in that
package's `NOTICE`).

### Limits

- Censoring is word-list based: it doesn't understand context, so a medical
  page discussing anatomy will be masked too. Use `exclude` for such sites.
- Text inserted by JavaScript after the page loads isn't seen; only the
  HTML the server sent is rewritten.
- Words split by markup (`sh<b>it</b>`) aren't matched.

## Android and MDM

The native classifiers screen exposes `mode` and `censor_words`. Managed
configurations can set `text_classifier_mode` (`block`/`censor`/`both`) and
`text_classifier_censor_words` (one per line). `censor_languages` is
editable from the web UI.
