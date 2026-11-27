package scrapers

import (
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDiscoveryMergeAndSpotlight(t *testing.T) {
	card := func(p, id, title string) discoveryCard {
		return newDiscoveryCard(p, id, title, "https://uploads.mangadex.org/covers/fixture/a.jpg", "Ranking", 1)
	}
	a, b := card("mangadex", "a", "One Piece"), card("mangapill", "/manga/2/one-piece", "One Piece")
	a.Signals[0].Followers = 100
	b.UpdatedAt = "2026-01-02T00:00:00.000000000Z"
	b.Chapters = []string{"Chapter 2"}
	colored := card("weebcentral", "c", "One Piece (Color)")
	result := mergeDiscovery("popular", [][]discoveryCard{{a}, {b, colored}}, time.Now())
	if len(result) != 2 || len(result[0].Signals) != 2 || result[0].Slug != a.Slug || result[0].Chapters[0] != "Chapter 2" || result[0].Signals[0].Followers != 100 {
		t.Fatalf("merge lost metadata/edition/signals: %+v", result)
	}
	if len(a.Signals) != 1 || a.UpdatedAt != "" {
		t.Fatal("mutated cached input")
	}
	same := card("mangadex", "other", "One Piece")
	if got := mergeDiscovery("popular", [][]discoveryCard{{a, same}}, time.Now()); len(got) != 2 {
		t.Fatal("merged ambiguous same-provider series")
	}
	old := card("mangaread", "old", "Old")
	old.UpdatedAt = "2025-01-01T00:00:00.000000000Z"
	if got := mergeDiscovery("recent", [][]discoveryCard{{old}, {b}}, time.Now()); got[0].Title != b.Title {
		t.Fatal("recent feed not sorted by release time")
	}
	sections := []discoverySection{}
	for _, kind := range discoveryKinds {
		items := []discoveryCard{}
		for i := 0; i < 20; i++ {
			items = append(items, card("mangaread", fmt.Sprintf("%s-%d", kind, i), fmt.Sprintf("%s %d", kind, i)))
		}
		sections = append(sections, discoverySection{ID: kind, Title: kind, Items: items})
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	first := discoverySpotlight(sections, now)
	second := discoverySpotlight(sections, now.Add(time.Hour))
	if len(first) != 12 || first[0].Slug == second[0].Slug {
		t.Fatal("spotlight variety/limit failed")
	}
	seen := map[string]bool{}
	for i, c := range first {
		if seen[c.Title] || c.SpotlightReason != discoveryKinds[i%3] {
			t.Fatal("spotlight duplicate or missing reason")
		}
		seen[c.Title] = true
	}
	if len(discoverySpotlight([]discoverySection{{Items: []discoveryCard{a, a}}}, now)) != 1 {
		t.Fatal("exhausted duplicate queue")
	}
}
func TestDiscoveryDates(t *testing.T) {
	now := time.Now().UTC()
	for _, bad := range []string{"nonsense", "999999999999999 weeks ago", "525600 weeks ago", "-1 min ago", "2029-01-01T00:00:00Z"} {
		if discoveryRelativeTime(bad, now) != "" {
			t.Fatalf("accepted invalid date: %s", bad)
		}
	}
	got := discoveryRelativeTime("23 mins ago", now)
	want := now.Add(-23 * time.Minute).Format("2006-01-02T15:04:05.000000000Z")
	if got != want {
		t.Fatal(got, want)
	}
}
func TestDiscoveryHTMLFixtures(t *testing.T) {
	load := func(name string) *goquery.Document {
		f, e := os.Open("testdata/discovery/" + name + ".html")
		if e != nil {
			t.Fatal(e)
		}
		defer f.Close()
		doc, e := goquery.NewDocumentFromReader(f)
		if e != nil {
			t.Fatal(e)
		}
		return doc
	}
	for _, kind := range discoveryKinds {
		mr := parseMRDiscovery(load("mangaread-"+kind), kind)
		if len(mr) < 2 || mr[0].Title == "" || mr[0].Image == "" {
			t.Fatalf("MangaRead %s: %+v", kind, mr)
		}
		pill := parsePillDiscovery(load("mangapill-"+kind).Selection, "Test", kind)
		if len(pill) < 2 || pill[0].Title == "" || (kind == "recent" && (pill[0].UpdatedAt == "" || len(pill[0].Chapters) == 0)) {
			t.Fatalf("MangaPill %s: %+v", kind, pill)
		}
	}
	wc := parseWCCards(load("weebcentral-recent").Selection, "Latest updates", true)
	if len(wc) < 2 || wc[0].UpdatedAt == "" {
		t.Fatalf("WC recent: %+v", wc)
	}
	wc = parseWCCards(load("weebcentral-popular").Selection, "Hot updates", false)
	if len(wc) < 2 {
		t.Fatalf("WC hot: %+v", wc)
	}
	for _, kind := range []string{"new", "weekly"} {
		wc = parseWCLinks(load("weebcentral-"+kind).Selection, "Test")
		if len(wc) < 2 || !strings.HasPrefix(wc[0].Slug, "wc_") {
			t.Fatalf("WC %s: %+v", kind, wc)
		}
	}
}
func resetDiscoveryCaches() {
	discoveryFeeds = newCache[discoveryFeed](16, 5*time.Minute)
	discoveryAttempts = newCache[discoveryAttempt](16, time.Minute)
	discoverySnapshots = newCache[discoveryResponse](1, time.Minute)
	discoveryDocuments = newCache[*goquery.Document](16, time.Minute)
	discoveryLastGood.Lock()
	discoveryLastGood.values = map[string]discoveryFeed{}
	discoveryLastGood.Unlock()
}
func TestDiscoveryOutagesAndBoundedStaleFallback(t *testing.T) {
	original := providerClient
	t.Cleanup(func() { providerClient = original; resetDiscoveryCaches() })
	resetDiscoveryCaches()
	var calls atomic.Int32
	providerClient = &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("Unavailable")), Request: r}, nil
	})}
	stale := discoveryFeed{FetchedAt: time.Now().Add(-10 * time.Minute), Items: []discoveryCard{newDiscoveryCard("mangaread", "fixture", "Saved pick", "https://uploads.mangadex.org/covers/a/b.jpg", "Popular list", 1)}}
	discoveryLastGood.values["mangaread:popular"] = stale
	a := cachedDiscoveryFeed("mangaread", "popular")
	before := calls.Load()
	b := cachedDiscoveryFeed("mangaread", "popular")
	if !a.Stale || a.Err == nil || len(b.Feed.Items) != 1 || calls.Load() != before {
		t.Fatal("stale fallback/failure cache failed")
	}
	response, err := discoverySnapshot()
	if err != nil || len(response.Sections[0].Items) != 1 || len(response.Sources[3].Stale) != 1 || len(response.Sources[0].Unavailable) != 3 {
		t.Fatalf("partial outage removed healthy/saved picks: %+v %v", response, err)
	}
	resetDiscoveryCaches()
	stale.FetchedAt = time.Now().Add(-31 * time.Minute)
	discoveryLastGood.values["mangaread:popular"] = stale
	if got := cachedDiscoveryFeed("mangaread", "popular"); len(got.Feed.Items) > 0 || got.Stale {
		t.Fatal("served stale data beyond 30 minute limit")
	}
}
func TestDiscoveryMangaDexReleaseModesAndStatistics(t *testing.T) {
	mdJSONCache = newCache[[]byte](256, 3*time.Minute)
	t.Cleanup(func() { mdJSONCache = newCache[[]byte](256, 3*time.Minute) })
	original := providerClient
	t.Cleanup(func() { providerClient = original })
	modes := []string{}
	const id = "00000000-0000-4000-8000-000000000001"
	providerClient = &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"data":[{"id":"` + id + `","attributes":{"title":{"en":"Fixture"},"createdAt":"2026-01-01T00:00:00Z"},"relationships":[{"id":"cover","type":"cover_art","attributes":{"fileName":"x.jpg"}}]}]}`
		switch r.URL.Path {
		case "/chapter":
			mode := r.URL.Query().Get("includeExternalUrl")
			modes = append(modes, mode)
			if r.URL.Query().Get("translatedLanguage[]") != "en" || r.URL.Query().Get("order[readableAt]") != "desc" {
				t.Error("not an English release feed")
			}
			day := "01"
			if mode == "1" {
				day = "02"
			}
			body = `{"data":[{"attributes":{"chapter":"2","readableAt":"2026-01-` + day + `T00:00:00Z"},"relationships":[{"type":"manga","id":"` + id + `"}]}]}`
		case "/statistics/manga":
			if r.URL.Query().Get("manga[]") != id {
				t.Error("stats not batched by ID")
			}
			body = `{"statistics":{"` + id + `":{"follows":100,"rating":{"bayesian":9.1}}}}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	recent, err := mdDiscovery("recent")
	if err != nil || len(recent) != 1 || !slices.Equal(modes, []string{"0", "1"}) || !strings.Contains(recent[0].UpdatedAt, "2026-01-02") {
		t.Fatalf("lost publisher/hosted release: %+v %v %v", recent, modes, err)
	}
	popular, err := mdDiscovery("popular")
	if err != nil || popular[0].Signals[0].Followers != 100 || popular[0].Signals[0].Rating != 9.1 {
		t.Fatalf("lost real stats: %+v %v", popular, err)
	}
}
func TestLiveDiscovery(t *testing.T) {
	if os.Getenv("LIVE_DISCOVERY") != "1" {
		t.Skip("set LIVE_DISCOVERY=1 for public provider checks")
	}
	resetDiscoveryCaches()
	for _, provider := range discoveryProviders {
		for _, kind := range discoveryKinds {
			t.Run(provider+"/"+kind, func(t *testing.T) {
				items, err := providerDiscovery(provider, kind)
				if err != nil || len(items) == 0 {
					t.Fatalf("no feed: %v", err)
				}
				fmt.Printf("%s/%s: %d cards; first=%q signal=%+v release=%s added=%s\n", provider, kind, len(items), items[0].Title, items[0].Signals, items[0].UpdatedAt, items[0].AddedAt)
			})
		}
	}
}
