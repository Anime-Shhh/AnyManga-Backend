package scrapers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestProviderPrioritiesAndChapterMerge(t *testing.T) {
	chapter := func(p, id, n string) sourceChapter {
		return sourceChapter{Provider: p, ID: id, Title: "Chapter " + n, Number: n, Language: "en"}
	}
	sources := map[string]*providerSeries{}
	for _, p := range metadataOrder {
		sources[p] = &providerSeries{Provider: p, Name: p, Kind: "ja", Description: p, Chapters: []sourceChapter{chapter(p, p+"-2", "2"), chapter(p, p+"-2.5", "2.5")}}
	}
	sources["mangapill"].Chapters = append(sources["mangapill"].Chapters, chapter("mangapill", "only-pill", "3"))
	index := mergeSeries("fixture", "en", sources)
	if index.MetadataSource != "mangadex" || index.Description != "mangadex" || len(index.Entries) != 3 {
		t.Fatalf("incorrect metadata/merge: %+v", index)
	}
	if index.Entries[0].ID != "mangaread-2" || index.Entries[1].ID != "mangaread-2.5" {
		t.Fatalf("old links or decimal ordering lost: %+v", index.Entries)
	}
	order := []string{}
	for _, c := range index.candidates[index.Entries[0].ID] {
		order = append(order, c.Provider)
	}
	if !slices.Equal(order, readingOrder("ja")) {
		t.Fatalf("incorrect manga fallback: %v", order)
	}
	for _, s := range sources {
		s.Kind = "ko"
	}
	index = mergeSeries("fixture-ko", "en", sources)
	order = nil
	for _, c := range index.candidates[index.Entries[0].ID] {
		order = append(order, c.Provider)
	}
	if !slices.Equal(order, []string{"weebcentral", "mangaread", "mangadex", "mangapill"}) {
		t.Fatalf("incorrect manhwa fallback: %v", order)
	}
	if titlesMatch("Solo Leveling: Ragnarok", []string{"Solo Leveling"}) || titlesMatch("One Piece (Colored)", []string{"One Piece"}) {
		t.Fatal("mixed different editions")
	}
}

func TestChapterFallbackAndExternalReleases(t *testing.T) {
	original := providerClient
	t.Cleanup(func() { providerClient = original })
	calls := []string{}
	providerClient = &http.Client{Transport: testTransport(func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.URL.Hostname())
		status := 200
		body := `<img class="js-page" data-src="https://cdn.readdetectiveconan.com/file/mangap/fixture/1.jpg">`
		if req.URL.Hostname() == "weebcentral.com" {
			status = 503
			body = "maintenance"
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})}
	index := &mangaIndex{Slug: "fallback-fixture", Language: "en", candidates: map[string][]sourceChapter{"chapter-1": {
		{Provider: "weebcentral", ID: "fixture-wc", URL: "https://weebcentral.com/chapters/fixture-wc"},
		{Provider: "mangapill", ID: "fixture-pill", URL: "https://mangapill.com/chapters/fixture-pill"},
		{Provider: "mangadex", ID: "external", ExternalURL: "https://mangaplus.shueisha.co.jp/viewer/fixture"},
	}}}
	content, err := loadContent(index, chapterLink{ID: "chapter-1"}, "")
	if err != nil || content.Provider != "mangapill" || len(content.Images) != 1 || !slices.Equal(calls, []string{"weebcentral.com", "mangapill.com"}) {
		t.Fatalf("fallback failed: %+v %v %v", content, err, calls)
	}
	_, err = loadContent(index, chapterLink{ID: "chapter-1"}, "")
	if err != nil || len(calls) != 2 {
		t.Fatal("content cache repeated upstream calls")
	}
	external, err := loadContent(index, chapterLink{ID: "chapter-1"}, "weebcentral,mangapill")
	if err != nil || external.ExternalURL == "" || len(external.Images) != 0 {
		t.Fatalf("lost publisher redirect: %+v %v", external, err)
	}
}
func TestImageProxyAndIdentifierRestrictions(t *testing.T) {
	for _, address := range []string{"http://uploads.mangadex.org/data/x", "https://127.0.0.1/a", "https://cdn.readdetectiveconan.com.evil.test/file/mangap/1", "https://user@uploads.mangadex.org/a", "https://www.mangaread.org/admin", "https://uploads.mangadex.org:8443/a"} {
		u, _ := url.Parse(address)
		if allowedImageURL(u) {
			t.Fatalf("unsafe image permitted: %s", address)
		}
	}
	for _, ref := range []string{"mp_Ly9sb2NhbGhvc3QvYQ", "md_../../secret", "wc_https://localhost"} {
		if _, _, err := parseSeriesRef(ref); err == nil {
			t.Fatalf("unsafe identifier accepted: %s", ref)
		}
	}
}
func TestMangaDexChapterTitleShape(t *testing.T) {
	var chapter mdEntity
	if err := json.Unmarshal([]byte(`{"id":"x","attributes":{"title":"A chapter title","chapter":"1","pages":3}}`), &chapter); err != nil {
		t.Fatal(err)
	}
}

// Opt-in network smoke test; normal unit tests remain offline and deterministic.
func TestLiveProviderPages(t *testing.T) {
	if os.Getenv("LIVE_PROVIDERS") != "1" {
		t.Skip("set LIVE_PROVIDERS=1 for upstream smoke tests")
	}
	fixtures := []struct{ p, id string }{
		{"mangaread", "one-piece"},
		{"weebcentral", "01J76XY7E9FNDZ1DBBM6PBJPFK"},
		{"mangapill", "/manga/2/one-piece"},
		{"mangadex", "be282a1e-5a13-4f89-9d98-7da56d5dbb1e"},
	}
	for _, f := range fixtures {
		t.Run(f.p, func(t *testing.T) {
			s, err := sourceSeries(f.p, f.id, "en")
			if err != nil {
				t.Fatal(err)
			}
			if s.Name == "" || len(s.Chapters) == 0 {
				t.Fatalf("empty series: %+v", s)
			}
			if s.Image != "" {
				_, _, coverErr := fetchImage(t.Context(), s.Image)
				if coverErr != nil {
					t.Fatalf("cover download: %v", coverErr)
				}
			}
			c := s.Chapters[len(s.Chapters)-1]
			for _, candidate := range s.Chapters {
				if candidate.Number == "1198" {
					c = candidate
					break
				}
			}
			pages, err := sourcePages(c)
			if err != nil {
				t.Fatal(err)
			}
			if len(pages) == 0 {
				t.Fatal("no pages")
			}
			data, mime, err := fetchImage(t.Context(), pages[0])
			if err != nil {
				t.Fatal(err)
			}
			matches, searchErr := sourceSearch(f.p, "One Piece", 1)
			if searchErr != nil {
				t.Fatal(searchErr)
			}
			found := false
			names := []string{}
			for _, match := range matches {
				names = append(names, match.Name)
				if titlesMatch(match.Name, []string{"One Piece"}) {
					found = true
					if f.p == "mangadex" {
						_, _, err := fetchImage(t.Context(), match.Image)
						if err != nil {
							t.Fatalf("One Piece cover: %v", err)
						}
					}
				}
			}
			if !found {
				t.Fatalf("main title missing in source search: %v", names)
			}
			fmt.Printf("%s: %s, %d chapters, %s: %d pages, first %s %d bytes\n", f.p, s.Name, len(s.Chapters), c.Title, len(pages), mime, len(data))
		})
	}
}

func TestMangaDexCombinesHostedAndExternalFeeds(t *testing.T) {
	original := providerClient
	t.Cleanup(func() { providerClient = original })
	modes := []string{}
	providerClient = &http.Client{Transport: testTransport(func(req *http.Request) (*http.Response, error) {
		body := `{"data":{"id":"00000000-0000-4000-8000-000000000001","attributes":{"title":{"en":"Feed fixture"}},"relationships":[]}}`
		if strings.HasSuffix(req.URL.Path, "/feed") {
			mode := req.URL.Query().Get("includeExternalUrl")
			modes = append(modes, mode)
			attributes := `"chapter":"1","title":"Hosted","pages":2,"translatedLanguage":"en","externalUrl":null`
			id := "hosted"
			if mode == "1" {
				attributes = `"chapter":"2","title":null,"pages":0,"translatedLanguage":"en","externalUrl":"https://publisher.example/read/2"`
				id = "external"
			}
			body = `{"data":[{"id":"` + id + `","attributes":{` + attributes + `}}],"total":1}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})}
	s, err := mdSeries("00000000-0000-4000-8000-000000000001", "en")
	if err != nil || len(s.Chapters) != 2 || !slices.Equal(modes, []string{"0", "1"}) || s.Chapters[0].ExternalURL != "" || s.Chapters[1].ExternalURL == "" {
		t.Fatalf("hosted/external feed lost: %+v %v %v", s, err, modes)
	}
}
