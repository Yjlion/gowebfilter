//go:build ignore

// build_text_bayes_model.go rebuilds the compact embedded textbayes model
// from local word/phrase list snapshots. It intentionally accepts plain
// text inputs instead of fetching from the network so regeneration is
// repeatable and the caller controls licensing/provenance.
//
// Example:
//
//	go run scripts/build_text_bayes_model.go \
//	  --out internal/classify/textbayes/model_data.json \
//	  --ldnoobw path/to/LDNOOBW/en \
//	  --extra path/to/local-adult-phrases.txt
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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

var tokenRe = regexp.MustCompile(`[a-z0-9]+`)

func main() {
	out := flag.String("out", "internal/classify/textbayes/model_data.json", "output model JSON")
	ldnoobw := flag.String("ldnoobw", "", "path to an LDNOOBW language file or directory of files")
	extra := flag.String("extra", "", "optional newline-delimited phrase file")
	flag.Parse()

	features := map[string]featureData{}
	add := func(s string, adult, safe float64) {
		key := normalize(s)
		if key == "" || tooBroad(key) {
			return
		}
		features[key] = featureData{Text: key, Adult: adult, Safe: safe}
	}

	for _, seed := range conservativeSeeds() {
		add(seed, 420, 2)
	}
	if *ldnoobw != "" {
		if err := readInputs(*ldnoobw, func(s string) { add(s, 280, 4) }); err != nil {
			fatal(err)
		}
	}
	if *extra != "" {
		if err := readInputs(*extra, func(s string) { add(s, 420, 2) }); err != nil {
			fatal(err)
		}
	}

	keys := make([]string, 0, len(features))
	for key := range features {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	data := modelData{
		Name:    "embedded-adult-text-bayes",
		Version: 1,
		SourceNotes: []string{
			"Seed vocabulary curated from LDNOOBW/List-of-Dirty-Naughty-Obscene-and-Otherwise-Bad-Words English list concepts, CC-BY-4.0, copyright Shutterstock, Inc.",
			"Only use extra source files when their licenses are compatible with embedding in this project.",
		},
		AdultPrior: 0.015,
		SafePrior:  0.985,
		AdultTotal: 4200,
		SafeTotal:  120000,
	}
	for _, key := range keys {
		data.Features = append(data.Features, features[key])
	}
	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		fatal(err)
	}
	bytes = append(bytes, '\n')
	if err := os.WriteFile(*out, bytes, 0o644); err != nil {
		fatal(err)
	}
}

func readInputs(path string, add func(string)) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return readFile(path, add)
	}
	return filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		return readFile(p, add)
	})
}

func readFile(path string, add func(string)) error {
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

func normalize(s string) string {
	return strings.Join(tokenRe.FindAllString(strings.ToLower(s), -1), " ")
}

func tooBroad(s string) bool {
	switch s {
	case "sex", "nude", "naked", "adult", "girl", "girls", "hard":
		return true
	default:
		return false
	}
}

func conservativeSeeds() []string {
	return []string{
		"adult content", "adult entertainment", "adult video", "anal sex",
		"cam girl", "escort service", "explicit sex", "hentai", "live sex",
		"nsfw", "onlyfans", "oral sex", "porn", "porn video", "pornography",
		"sexual content", "threesome", "xxx",
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "build_text_bayes_model:", err)
	os.Exit(1)
}
