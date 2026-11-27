package scrapers

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

var (
	source        = &url.URL{Scheme: "https", Host: "www.mangaread.org"}
	mangaSlug     = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N}\p{M}_.-]{0,199}$`)
	chapterNumber = regexp.MustCompile(`(?i)^chapter[ -]+([0-9]+(?:\.[0-9]+)?)(?:$|\s|[:\[\(–—-])`)
	missing       = errors.New("not found")
	upstreamSlots = make(chan struct{}, 6)
	upstream      = &http.Client{
		Timeout: 12 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 || req.URL.Scheme != "https" ||
				(req.URL.Host != "www.mangaread.org" && req.URL.Host != "mangaread.org") {
				return errors.New("unexpected MangaRead redirect")
			}
			return nil
		},
	}
)

type chapterLink struct {
	Number string `json:"number,omitempty"`
	ID     string `json:"id"`
	Title  string `json:"title"`
	URL    string `json:"-"`
}

type mangaIndex struct {
	Name           string        `json:"name"`
	Image          string        `json:"image"`
	ImageFallback  string        `json:"imageFallback,omitempty"`
	Description    string        `json:"description"`
	Slug           string        `json:"slug"`
	Entries        []chapterLink `json:"chapterEntries"`
	Chapters       []string      `json:"chapters"`
	Kind           string        `json:"kind,omitempty"`
	AltTitles      []string      `json:"-"`
	Authors        []string      `json:"authors,omitempty"`
	Genres         []string      `json:"genres,omitempty"`
	Languages      []string      `json:"languages,omitempty"`
	Language       string        `json:"language,omitempty"`
	Sources        []sourceInfo  `json:"sources,omitempty"`
	MetadataSource string        `json:"metadataSource,omitempty"`
	candidates     map[string][]sourceChapter
	positions      map[string]int
	aliases        map[string]int
	numbers        map[string]int
}

type catalogItem struct {
	Slug          string   `json:"slug"`
	Title         string   `json:"title"`
	Image         string   `json:"image"`
	ImageFallback string   `json:"imageFallback,omitempty"`
	Description   string   `json:"description"`
	Chapters      []string `json:"chapters"`
}

var (
	indexCache  = newCache[*mangaIndex](256, 5*time.Minute)
	pageCache   = newCache[[]string](512, 5*time.Minute)
	searchCache = newCache[searchResults](256, 2*time.Minute)
	browseCache = newCache[[]catalogItem](4, 5*time.Minute)
)

func loadDocument(address string) (*goquery.Document, *url.URL, error) {
	req, err := http.NewRequest(http.MethodGet, address, nil)
	if err != nil {
		return nil, nil, err
	}
	select {
	case upstreamSlots <- struct{}{}:
		defer func() { <-upstreamSlots }()
	default:
		return nil, nil, errors.New("MangaRead request capacity reached")
	}
	resp, err := upstream.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil, missing
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("MangaRead returned HTTP %d", resp.StatusCode)
	}
	doc, err := goquery.NewDocumentFromReader(io.LimitReader(resp.Body, 16<<20))
	return doc, resp.Request.URL, err
}

func mangaURL(slug string) string {
	return source.ResolveReference(&url.URL{Path: "/manga/" + slug + "/"}).String()
}

func sourcePath(base *url.URL, href string) (*url.URL, []string) {
	link, err := base.Parse(strings.TrimSpace(href))
	if href == "" || err != nil || link.User != nil ||
		(link.Host != "www.mangaread.org" && link.Host != "mangaread.org") ||
		(link.Scheme != "https" && link.Scheme != "http") {
		return nil, nil
	}
	link.Scheme = "https"
	link.Fragment = ""
	return link, strings.Split(strings.Trim(link.Path, "/"), "/")
}

func imageURL(base *url.URL, img *goquery.Selection) string {
	for _, attribute := range []string{"data-src", "data-lazy-src", "data-original", "src"} {
		value := strings.TrimSpace(img.AttrOr(attribute, ""))
		if value == "" || strings.HasPrefix(value, "#") {
			continue
		}
		link, err := base.Parse(value)
		if err == nil && (link.Scheme == "https" || link.Scheme == "http") && link.Host != "" && link.User == nil {
			link.Scheme = "https"
			return link.String()
		}
	}
	return ""
}

func cleanText(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// The series page lists newest first and provides the canonical chapter hrefs.
func parseIndex(doc *goquery.Document, base *url.URL, slug string) *mangaIndex {
	index := &mangaIndex{
		Name: cleanText(doc.Find(".post-title h1").First().Text()), Slug: slug,
		Image:       imageURL(base, doc.Find(".summary_image img").First()),
		Description: strings.TrimSpace(doc.Find(".summary__content").First().Text()),
		Entries:     []chapterLink{}, Chapters: []string{},
		positions: map[string]int{}, aliases: map[string]int{}, numbers: map[string]int{},
	}
	if index.Name == "" {
		index.Name = cleanText(doc.Find("h1").First().Text())
	}
	doc.Find(".post-content_item").Each(func(_ int, row *goquery.Selection) {
		heading := cleanText(row.Find(".summary-heading").Text())
		value := cleanText(row.Find(".summary-content").Text())
		if heading == "Type" {
			index.Kind = strings.ToLower(value)
		}
		if heading == "Alternative" {
			index.AltTitles = strings.FieldsFunc(value, func(r rune) bool { return r == ';' || r == ',' || r == '；' })
		}
	})
	seen := map[string]bool{}
	doc.Find("li.wp-manga-chapter > a").Each(func(_ int, anchor *goquery.Selection) {
		link, parts := sourcePath(base, anchor.AttrOr("href", ""))
		if len(parts) != 3 || parts[0] != "manga" || parts[1] != slug || parts[2] == "" || seen[parts[2]] {
			return
		}
		title := cleanText(anchor.Text())
		if title == "" {
			return
		}
		seen[parts[2]] = true
		entry := chapterLink{ID: parts[2], Title: title, URL: link.String()}
		if match := chapterNumber.FindStringSubmatch(title); match != nil {
			entry.Number = canonicalNumber(match[1])
		}
		index.Entries = append(index.Entries, entry)
	})
	slices.Reverse(index.Entries)
	for position, entry := range index.Entries {
		index.positions[entry.ID] = position
		index.Chapters = append(index.Chapters, entry.Title)
		indexAlias(index.aliases, CleanURL(entry.Title), position)
		if match := chapterNumber.FindStringSubmatch(entry.Title); match != nil {
			indexAlias(index.numbers, match[1], position)
		}
	}
	return index
}

func indexAlias(positions map[string]int, key string, position int) {
	if _, duplicate := positions[key]; duplicate {
		positions[key] = -1
	} else {
		positions[key] = position
	}
}

func getIndex(slug string) (*mangaIndex, error) {
	return indexCache.get(slug, func() (*mangaIndex, error) {
		doc, base, err := loadDocument(mangaURL(slug))
		if err != nil {
			return nil, err
		}
		_, parts := sourcePath(source, base.String())
		if len(parts) != 2 || parts[0] != "manga" || !mangaSlug.MatchString(parts[1]) {
			return nil, errors.New("unexpected MangaRead series page")
		}
		index := parseIndex(doc, base, parts[1])
		if index.Name == "" || (len(index.Entries) == 0 && index.Image == "" && index.Description == "") {
			return nil, errors.New("MangaRead series markup not recognized")
		}
		return index, nil
	})
}

func (index *mangaIndex) lookup(chapter string) (int, bool) {
	if position, ok := index.positions[chapter]; ok {
		return position, true
	}
	// Match historical links generated from display titles, and the legacy download form.
	chapter = strings.TrimSpace(chapter)
	if position, ok := index.aliases[CleanURL(chapter)]; ok {
		return position, position >= 0
	}
	if match := chapterNumber.FindStringSubmatch(chapter); match != nil && len(match[0]) == len(chapter) {
		position, ok := index.numbers[match[1]]
		return position, ok && position >= 0
	}
	return 0, false
}

func loadPages(entry chapterLink) ([]string, error) {
	return pageCache.get(entry.URL, func() ([]string, error) {
		doc, base, err := loadDocument(entry.URL)
		if err != nil {
			return nil, err
		}
		var images []string
		doc.Find("img.wp-manga-chapter-img").Each(func(_ int, img *goquery.Selection) {
			if address := imageURL(base, img); address != "" {
				images = append(images, address)
			}
		})
		if len(images) == 0 {
			return nil, errors.New("MangaRead chapter has no recognized page images")
		}
		return images, nil
	})
}
