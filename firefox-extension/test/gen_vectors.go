//go:build ignore

// Generates bayes_vectors.json: adult-text scores from the Go
// implementation (internal/classify/textbayes) for the fixed inputs below.
// test/bayes_parity.mjs replays them against the JS port in
// background/bayes.js — the two must agree to near float precision.
//
// Regenerate (from the repo root) after changing the model or scorer:
//
//	go run firefox-extension/test/gen_vectors.go
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/yjlion/gowebfilter/internal/classify/textbayes"
)

var inputs = []string{
	"",
	"!!! ??? ---",
	"The quarterly report shows steady growth in the manufacturing sector.",
	"Grandma's apple pie recipe: flour, butter, sugar, apples, cinnamon.",
	"Watch free porn videos online now",
	"hot sexy nude cams live webcam girls xxx",
	"porn porn porn porn porn porn porn porn porn porn",
	"amateur pics and photos gallery of celebrities on the red carpet",
	"Ladies and strawberries, a study of berries and cherries.",
	"live sex cams with the hottest cam girls, join free adult chat",
	"school homework help with math and science tutoring for kids",
	"XXX ADULT CONTENT nsfw hentai anime collection",
	"the movie features some nudity and adult themes, rated R",
}

func main() {
	model, err := textbayes.New()
	if err != nil {
		panic(err)
	}
	type vec struct {
		Text  string  `json:"text"`
		Score float64 `json:"score"`
		Ok    bool    `json:"ok"`
	}
	out := make([]vec, 0, len(inputs))
	for _, text := range inputs {
		score, ok := model.Score(text)
		out = append(out, vec{Text: text, Score: score, Ok: ok})
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile("firefox-extension/test/bayes_vectors.json", append(data, '\n'), 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("wrote %d vectors\n", len(out))
}
