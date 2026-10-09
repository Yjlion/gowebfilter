//go:build ignore

// build_text_bayes_model.go rebuilds the embedded textbayes model
// (internal/classify/textbayes/model_data.json) from two layers:
//
//  1. the hand-curated, high-weight adult features in
//     scripts/text_bayes_curated.json, copied verbatim; and
//  2. every LDNOOBW language list embedded in internal/classify/profanity,
//     added at a deliberately LIGHT weight.
//
// LDNOOBW is a general profanity list (insults, slurs, swearing), not an
// adult-content list, so its entries are weak evidence: a handful of
// swear words must not push an ordinary page past the default 0.80
// threshold on their own. With the weights below each light hit moves the
// log-odds by roughly 0.75, so it takes about seven distinct hits (each
// capped at four repeats by the scorer) before a page with no curated
// adult phrase reaches 0.80. Curated features always win on conflicts.
//
// Short Latin-script entries are left out of the model entirely: they
// collide with common words across languages ("am", "pot", "fan", "con"),
// and unlike the censor the scorer has no page-language scoping.
//
// Usage (no network access; the inputs are in the repo):
//
//	go run scripts/build_text_bayes_model.go
//	go run scripts/build_text_bayes_model.go --extra path/to/local-phrases.txt
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/yjlion/gowebfilter/internal/classify/profanity"
)

type modelData struct {
	Name        string        `json:"name"`
	Version     int           `json:"version"`
	SourceNotes []string      `json:"source_notes"`
	AdultPrior  float64       `json:"adult_prior"`
	SafePrior   float64       `json:"safe_prior"`
	AdultTotal  float64       `json:"adult_total"`
	SafeTotal   float64       `json:"safe_total"`
	Features    []featureData `json:"features"`
}

type featureData struct {
	Text  string  `json:"text"`
	Adult float64 `json:"adult"`
	Safe  float64 `json:"safe"`
}

type curatedFile struct {
	SourceNotes []string      `json:"source_notes"`
	Features    []featureData `json:"features"`
}

// Light weight for LDNOOBW-only entries (see the file comment).
const (
	lightAdult = 6
	lightSafe  = 55
	// minLatinRunes drops short Latin-script entries from the model.
	minLatinRunes = 5
)

func main() {
	out := flag.String("out", "internal/classify/textbayes/model_data.json", "output model JSON")
	base := flag.String("base", "scripts/text_bayes_curated.json", "curated high-weight features")
	extra := flag.String("extra", "", "optional newline-delimited phrase file, added at curated weight (420/2)")
	flag.Parse()

	var cur curatedFile
	raw, err := os.ReadFile(*base)
	if err != nil {
		fatal(err)
	}
	if err := json.Unmarshal(raw, &cur); err != nil {
		fatal(fmt.Errorf("%s: %w", *base, err))
	}

	features := map[string]featureData{}
	keyOf := func(s string) string {
		if profanity.HasUnspaced(s) {
			return strings.ToLower(strings.TrimSpace(s))
		}
		return profanity.PhraseKey(s)
	}
	for _, f := range cur.Features {
		k := keyOf(f.Text)
		if k == "" {
			continue
		}
		f.Text = k
		features[k] = f
	}
	if *extra != "" {
		if err := readLines(*extra, func(s string) {
			if k := keyOf(s); k != "" && !tooBroad(k) {
				features[k] = featureData{Text: k, Adult: 420, Safe: 2}
			}
		}); err != nil {
			fatal(err)
		}
	}
	light := 0
	for _, e := range profanity.Entries() {
		k := keyOf(e.Text)
		if k == "" || tooBroad(k) {
			continue
		}
		if e.Latin && utf8.RuneCountInString(k) < minLatinRunes {
			continue
		}
		if _, ok := features[k]; ok {
			continue // curated (or an earlier language) wins
		}
		features[k] = featureData{Text: k, Adult: lightAdult, Safe: lightSafe}
		light++
	}

	keys := make([]string, 0, len(features))
	for k := range features {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	data := modelData{
		Name:    "embedded-adult-text-bayes",
		Version: 2,
		SourceNotes: append(cur.SourceNotes,
			fmt.Sprintf("Plus %d light-weight entries from every LDNOOBW language list (internal/classify/profanity/words, CC-BY-4.0, copyright Shutterstock, Inc.).", light)),
		AdultPrior: 0.015,
		SafePrior:  0.985,
		AdultTotal: 4200,
		SafeTotal:  120000,
	}
	for _, k := range keys {
		data.Features = append(data.Features, features[k])
	}
	if err := os.WriteFile(*out, render(data), 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("wrote %s: %d features (%d curated, %d light)\n", *out, len(keys), len(keys)-light, light)
}

// render writes one feature per line so diffs of the regenerated model
// stay readable.
func render(d modelData) []byte {
	var b bytes.Buffer
	enc := func(v any) string {
		var buf bytes.Buffer
		e := json.NewEncoder(&buf)
		e.SetEscapeHTML(false)
		_ = e.Encode(v)
		return strings.TrimSpace(buf.String())
	}
	b.WriteString("{\n")
	fmt.Fprintf(&b, "  \"name\": %s,\n  \"version\": %d,\n  \"source_notes\": [\n", enc(d.Name), d.Version)
	for i, n := range d.SourceNotes {
		sep := ","
		if i == len(d.SourceNotes)-1 {
			sep = ""
		}
		fmt.Fprintf(&b, "    %s%s\n", enc(n), sep)
	}
	fmt.Fprintf(&b, "  ],\n  \"adult_prior\": %v,\n  \"safe_prior\": %v,\n  \"adult_total\": %v,\n  \"safe_total\": %v,\n  \"features\": [\n",
		d.AdultPrior, d.SafePrior, d.AdultTotal, d.SafeTotal)
	for i, f := range d.Features {
		sep := ","
		if i == len(d.Features)-1 {
			sep = ""
		}
		fmt.Fprintf(&b, "    {\"text\": %s, \"adult\": %v, \"safe\": %v}%s\n", enc(f.Text), f.Adult, f.Safe, sep)
	}
	b.WriteString("  ]\n}\n")
	return b.Bytes()
}

func readLines(path string, add func(string)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		add(line)
	}
	return sc.Err()
}

// tooBroad lists words that are adult-adjacent but far too common in
// ordinary prose to count as evidence at any weight.
func tooBroad(s string) bool {
	switch s {
	case "sex", "nude", "naked", "adult", "girl", "girls", "hard", "sexo", "sexy":
		return true
	default:
		return false
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "build_text_bayes_model:", err)
	os.Exit(1)
}
