//go:build ignore

// seed_sample_data.go populates a throwaway data directory with realistic
// policies and request/block/audit logs, so the management UI has something to
// show in screenshots and manual walkthroughs.
//
// It never touches the repo's own config/, policies/ or logs/ - point it at a
// scratch directory and start the server against the settings file it writes:
//
//	go run scripts/seed_sample_data.go -dir /tmp/wf-demo
//	./webfilter mgmt --settings /tmp/wf-demo/config/settings.json
//
// The generator is seeded with a fixed RNG seed, so re-running it produces the
// same data and screenshots stay comparable across runs.
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yjlion/gowebfilter/internal/categories"
	"github.com/yjlion/gowebfilter/internal/logstore"
	"github.com/yjlion/gowebfilter/internal/models"
)

func main() {
	dir := flag.String("dir", "", "data directory to populate (required; created if missing)")
	hours := flag.Int("hours", 48, "how far back to spread generated log entries")
	requests := flag.Int("requests", 900, "number of request-log rows to generate")
	port := flag.Int("mgmt-port", 8000, "mgmt_port to write into the generated settings.json")
	proxyPort := flag.Int("proxy-port", 8080, "HTTP proxy port to write into the generated settings.json")
	flag.Parse()

	if *dir == "" {
		log.Fatal("-dir is required (e.g. -dir /tmp/wf-demo)")
	}
	root, err := filepath.Abs(*dir)
	if err != nil {
		log.Fatalf("resolve -dir: %v", err)
	}

	// Test-helper rule from CLAUDE.md applies here too: seed absolute paths, or
	// the documented relative defaults resolve against the process's working
	// directory rather than the settings file's location.
	for _, sub := range []string{"config", "policies", "certs", "logs", "categories", "adblock"} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			log.Fatalf("mkdir %s: %v", sub, err)
		}
	}

	if err := writeSettings(root, *port, *proxyPort); err != nil {
		log.Fatalf("write settings: %v", err)
	}
	if err := writePolicies(root); err != nil {
		log.Fatalf("write policies: %v", err)
	}
	if err := writeCategories(root); err != nil {
		log.Fatalf("write categories: %v", err)
	}
	if err := writeAdblockLists(root); err != nil {
		log.Fatalf("write adblock lists: %v", err)
	}
	if err := seedLogs(root, *hours, *requests); err != nil {
		log.Fatalf("seed logs: %v", err)
	}

	fmt.Printf("seeded %s\n", root)
	fmt.Printf("  start with: ./webfilter mgmt --settings %s\n", filepath.Join(root, "config", "settings.json"))
}

func writeJSONFile(path string, v any) error {
	buf, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(buf, '\n'), 0o644)
}

func writeSettings(root string, port, proxyPort int) error {
	s := models.NewGlobalSettings()
	s.MgmtHost = "127.0.0.1"
	s.MgmtPort = port
	s.CertDir = filepath.Join(root, "certs")
	s.PoliciesDir = filepath.Join(root, "policies")
	s.LogsDir = filepath.Join(root, "logs")
	s.CategoriesDir = filepath.Join(root, "categories")
	s.Adblock.Dir = filepath.Join(root, "adblock")
	s.Adblock.CustomLists = []models.AdblockListSource{{Name: "home-lab", URL: "https://lists.example/home-lab.txt"}}
	s.LogBlocks = true
	s.LogRequests = true
	s.ProxyListen = []string{fmt.Sprintf("127.0.0.1:%d", proxyPort)}
	return writeJSONFile(filepath.Join(root, "config", "settings.json"), s)
}

// writePolicies lays down a spread of policies that exercises the UI's badges:
// an always-on catch-all, a strict child profile, a looser teen profile with a
// MAC match, a scheduled bedtime profile, and one that is switched off.
func writePolicies(root string) error {
	policies := []models.Policy{
		defaultPolicy(),
		kidsPolicy(),
		teensPolicy(),
		bedtimePolicy(),
		guestPolicy(),
		labPolicy(),
	}
	for _, p := range policies {
		path := filepath.Join(root, "policies", p.Name+".json")
		if err := writeJSONFile(path, p); err != nil {
			return fmt.Errorf("%s: %w", p.Name, err)
		}
	}
	return nil
}

func defaultPolicy() models.Policy {
	p := models.NewPolicy()
	p.Name = "default"
	p.SafeSearch.Enabled = true
	p.UrlFilter.Enabled = true
	p.UrlFilter.Block = []string{"*.doubleclick.net", "ads.example.com"}
	p.Doh.Enabled = true
	p.Doh.Server = "https://1.1.1.3/dns-query"
	return p
}

func kidsPolicy() models.Policy {
	p := models.NewPolicy()
	p.Name = "kids"
	p.SourceIPs = []string{"192.168.1.50", "192.168.1.51"}
	p.UrlFilter.Enabled = true
	p.UrlFilter.CategoryActions = map[string]models.CategoryAction{
		"porn": models.CategoryActionBlock, "gambling": models.CategoryActionBlock,
		"violence": models.CategoryActionBlock, "social": models.CategoryActionBlock,
		"banking": models.CategoryActionAllow,
	}
	p.UrlFilter.Block = []string{"*.tiktok.com", "*.snapchat.com"}
	p.Adblock.Enabled = true
	p.Adblock.Lists = []string{"easylist", "easyprivacy", "fanboy-annoyance"}
	p.SafeSearch.Enabled = true
	for name, eng := range p.SafeSearch.Engines {
		eng.Enabled = true
		eng.BlockImagesTab = true
		eng.BlockVideosTab = true
		eng.BlockAiTab = true
		p.SafeSearch.Engines[name] = eng
	}
	p.TextClassifier.Enabled = true
	p.TextClassifier.Mode = models.TextModeBoth
	p.TextClassifier.Threshold = 0.75
	p.TextClassifier.CensorWords = []string{"frak"}
	p.ImageClassifier.Enabled = true
	p.ImageClassifier.Threshold = 0.35
	p.YouTube.Enabled = true
	p.YouTube.BlockHome = true
	p.YouTube.RemoveComments = true
	p.Doh.Enabled = true
	p.Doh.Server = "https://1.1.1.3/dns-query"
	return p
}

func teensPolicy() models.Policy {
	p := models.NewPolicy()
	p.Name = "teens"
	p.SourceIPs = []string{"192.168.1.60", "192.168.1.0/24"}
	p.SourceMACs = []string{"aa:bb:cc:dd:ee:01"}
	p.UrlFilter.Enabled = true
	p.UrlFilter.CategoryActions = map[string]models.CategoryAction{
		"porn": models.CategoryActionBlock, "gambling": models.CategoryActionBlock,
	}
	p.SafeSearch.Enabled = true
	p.TextClassifier.Enabled = true
	p.TextClassifier.Mode = models.TextModeCensor
	p.ImageClassifier.Enabled = true
	p.YouTube.Enabled = true
	p.Adblock.Enabled = true
	return p
}

// bedtimePolicy is scheduled overnight. Within a tier an actively-scheduled
// policy outranks an unscheduled one, so this deliberately overlaps teens'
// addresses to show that precedence in the UI.
func bedtimePolicy() models.Policy {
	p := models.NewPolicy()
	p.Name = "bedtime"
	p.SourceIPs = []string{"192.168.1.60"}
	p.Schedule.Enabled = true
	p.Schedule.ActiveWindows = []models.TimeWindow{
		{Days: []int{0, 1, 2, 3, 4, 5, 6}, Start: "21:30", End: "06:30"},
	}
	p.UrlFilter.Enabled = true
	p.UrlFilter.Mode = models.UrlFilterModeWhitelist
	p.UrlFilter.Allow = []string{"*.wikipedia.org", "*.khanacademy.org"}
	p.SafeSearch.Enabled = true
	p.TextClassifier.Enabled = true
	p.ImageClassifier.Enabled = true
	return p
}

func guestPolicy() models.Policy {
	p := models.NewPolicy()
	p.Name = "guest-wifi"
	p.Inactive = true
	p.SourceIPs = []string{"10.20.0.0/16"}
	p.UrlFilter.Enabled = true
	p.UrlFilter.CategoryActions = map[string]models.CategoryAction{"porn": models.CategoryActionBlock}
	p.SafeSearch.Enabled = true
	return p
}

// labPolicy matches the machine running the screenshot script (127.0.0.1),
// so the captured block page and censored/ad-filtered article come from a
// real pass through the proxy.
func labPolicy() models.Policy {
	p := models.NewPolicy()
	p.Name = "lab-pc"
	p.SourceIPs = []string{"127.0.0.1"}
	p.UrlFilter.Enabled = true
	p.UrlFilter.Block = []string{"blocked.example"}
	p.TextClassifier.Enabled = true
	p.TextClassifier.Mode = models.TextModeBoth
	p.Adblock.Enabled = true
	return p
}

// writeCategories installs a few small, made-up category lists so the
// policy editor's category table has rows (with counts) to show.
func writeCategories(root string) error {
	lists := map[string][]byte{}
	for name, n := range map[string]int{
		"porn": 412, "gambling": 128, "violence": 57, "social": 64,
		"banking": 93, "ads": 240, "shopping": 150, "games": 88,
	} {
		var b strings.Builder
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "%s-%d.example\n", name, i)
		}
		lists[name] = []byte(b.String())
	}
	_, err := categories.WriteCategories(filepath.Join(root, "categories"), "sample-data", lists, nil)
	return err
}

// writeAdblockLists installs tiny stand-ins for a few preset lists, plus an
// index.json, so the adblock UI shows installed lists without any network.
func writeAdblockLists(root string) error {
	dir := filepath.Join(root, "adblock")
	type entry struct {
		URL     string `json:"url"`
		Rules   int    `json:"rules"`
		Updated string `json:"updated,omitempty"`
		Error   string `json:"error,omitempty"`
	}
	index := map[string]entry{}
	updated := time.Now().UTC().Add(-3 * time.Hour).Format("2006-01-02T15:04:05Z")
	for name, rules := range map[string]int{"easylist": 81234, "easyprivacy": 52310, "fanboy-annoyance": 31877} {
		var raw bytes.Buffer
		raw.WriteString("[Adblock Plus 2.0]\n! sample data stand-in\n||ads.example^\n/ads/*\n##.ad-banner\n##.ad-slot\n###sponsored\n")
		var gz bytes.Buffer
		zw := gzip.NewWriter(&gz)
		zw.Write(raw.Bytes())
		zw.Close()
		if err := os.WriteFile(filepath.Join(dir, name+".txt.gz"), gz.Bytes(), 0o644); err != nil {
			return err
		}
		index[name] = entry{URL: "https://easylist.to/easylist/" + name + ".txt", Rules: rules, Updated: updated}
	}
	index["home-lab"] = entry{URL: "https://lists.example/home-lab.txt", Error: "download https://lists.example/home-lab.txt: HTTP 404"}
	return writeJSONFile(filepath.Join(dir, "index.json"), map[string]any{"lists": index})
}

// ---------------------------------------------------------------------------
// logs
// ---------------------------------------------------------------------------

type device struct {
	ip     string
	policy string
	agent  string
}

var devices = []device{
	{"192.168.1.50", "kids", "Mozilla/5.0 (iPad; CPU OS 17_5 like Mac OS X) AppleWebKit/605.1.15 Safari/604.1"},
	{"192.168.1.51", "kids", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126.0 Safari/537.36"},
	{"192.168.1.60", "teens", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/605.1.15 Safari/17.5"},
	{"192.168.1.61", "teens", "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 Chrome/126.0 Mobile Safari/537.36"},
	{"192.168.1.12", "default", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126.0 Safari/537.36"},
	{"192.168.1.13", "default", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/126.0 Safari/537.36"},
}

// allowedSites are ordinary destinations; blockedSites each come with the
// component that would have caught them, so the Analytics page's
// "Blocks by Filter" panel has a realistic spread rather than one bar.
var allowedSites = []struct{ host, path string }{
	{"www.wikipedia.org", "/wiki/Main_Page"},
	{"www.khanacademy.org", "/math/algebra"},
	{"github.com", "/golang/go"},
	{"news.ycombinator.com", "/"},
	{"www.google.com", "/search"},
	{"duckduckgo.com", "/"},
	{"search.brave.com", "/search"},
	{"www.youtube.com", "/watch"},
	{"cdn.jsdelivr.net", "/npm/alpinejs"},
	{"fonts.gstatic.com", "/s/roboto/v30/font.woff2"},
	{"api.weather.gov", "/points/40,-75"},
	{"www.bbc.co.uk", "/news"},
}

var blockedSites = []struct{ host, path, component, reason string }{
	{"adult-example.com", "/", "url_filter", "matched blocked category porn"},
	{"xxx-sample.net", "/videos", "url_filter", "matched blocked category porn"},
	{"bet-sample.com", "/live", "url_filter", "matched blocked category gambling"},
	{"ads.example.com", "/track.js", "url_filter", "matched block pattern ads.example.com"},
	{"www.tiktok.com", "/foryou", "url_filter", "matched block pattern *.tiktok.com"},
	{"www.google.com", "/search?udm=2", "safesearch", "Image search blocked by policy"},
	{"duckduckgo.com", "/duckchat", "safesearch", "AI search blocked by policy"},
	{"search.brave.com", "/ask", "safesearch", "AI search blocked by policy"},
	{"encrypted-tbn0.gstatic.com", "/images", "safesearch", "Image search blocked by policy"},
	{"forum-example.org", "/thread/912", "text_classifier", "Adult text content detected"},
	{"imgboard-example.com", "/img/4471.jpg", "image_classifier", "Adult image content detected"},
	{"blocked-doh.example", "/dns-query", "doh_filter", "Domain blocked by DNS filter"},
	{"www.youtube.com", "/watch?v=blocked", "youtube", "Channel not in allowlist"},
	{"securepubads.g.doubleclick.net", "/tag/js/gpt.js", "adblock", "Blocked by ad/tracker filter: ||g.doubleclick.net^"},
	{"www.google-analytics.com", "/analytics.js", "adblock", "Blocked by ad/tracker filter: ||google-analytics.com^$script,third-party"},
	{"connect.facebook.net", "/en_US/fbevents.js", "adblock", "Blocked by ad/tracker filter: ||connect.facebook.net^$third-party"},
	{"cdn.taboola.com", "/libtrc/loader.js", "adblock", "Blocked by ad/tracker filter: ||taboola.com^$third-party"},
}

func seedLogs(root string, hours, requests int) error {
	store, err := logstore.Configure(filepath.Join(root, "logs", "webfilter.db"), 30, true, true)
	if err != nil {
		return err
	}
	defer store.Close()

	rng := rand.New(rand.NewSource(20260813))
	now := time.Now()
	window := time.Duration(hours) * time.Hour

	for i := 0; i < requests; i++ {
		// Weight timestamps toward the recent end so the analytics timeline
		// has a visible shape instead of a flat bar.
		frac := rng.Float64() * rng.Float64()
		ts := now.Add(-time.Duration(frac * float64(window)))
		dev := devices[rng.Intn(len(devices))]

		// Roughly one in five requests is blocked; kids' devices more often.
		blockChance := 0.18
		if dev.policy == "kids" {
			blockChance = 0.32
		}

		if rng.Float64() < blockChance {
			b := blockedSites[rng.Intn(len(blockedSites))]
			url := "https://" + b.host + b.path
			if err := store.LogRequest(logstore.RequestEntry{
				TS: ts.Unix(), Method: "GET", Host: b.host, Path: b.path,
				Status: 200, Action: "blocked", Component: b.component,
				Policy: dev.policy, ClientIP: dev.ip, UserAgent: dev.agent,
			}); err != nil {
				return err
			}
			if err := store.LogBlock(logstore.BlockEntry{
				TS: ts.Unix(), Domain: b.host, URL: url, Reason: b.reason,
				Component: b.component, Policy: dev.policy, ClientIP: dev.ip,
			}); err != nil {
				return err
			}
			continue
		}

		a := allowedSites[rng.Intn(len(allowedSites))]
		action := "ok"
		component := ""
		// SafeSearch rewrites search URLs rather than blocking them, which is
		// what the "modified" action in the logs represents.
		if a.path == "/search" || a.host == "duckduckgo.com" {
			if rng.Float64() < 0.7 {
				action, component = "modified", "safesearch"
			}
		}
		// Censor mode masks words in place, which also logs as "modified".
		if a.host == "news.ycombinator.com" && dev.policy == "teens" && rng.Float64() < 0.5 {
			action, component = "modified", "text_classifier"
		}
		if err := store.LogRequest(logstore.RequestEntry{
			TS: ts.Unix(), Method: "GET", Host: a.host, Path: a.path,
			Status: 200, Action: action, Component: component,
			Policy: dev.policy, ClientIP: dev.ip, UserAgent: dev.agent,
		}); err != nil {
			return err
		}
	}

	// A short policy-change audit trail for the third Logs tab.
	changes := []logstore.PolicyChangeEntry{
		{Action: "created", PolicyName: "kids", ClientIP: "192.168.1.10"},
		{Action: "created", PolicyName: "teens", ClientIP: "192.168.1.10"},
		{Action: "updated", PolicyName: "kids", ClientIP: "192.168.1.10"},
		{Action: "created", PolicyName: "bedtime", ClientIP: "192.168.1.10"},
		{Action: "updated", PolicyName: "default", ClientIP: "127.0.0.1"},
		{Action: "updated", PolicyName: "bedtime", ClientIP: "192.168.1.10"},
		{Action: "created", PolicyName: "guest-wifi", ClientIP: "192.168.1.10"},
	}
	for i, c := range changes {
		c.TS = now.Add(-time.Duration(len(changes)-i) * 3 * time.Hour).Unix()
		if err := store.LogPolicyChange(c); err != nil {
			return err
		}
	}
	return nil
}
