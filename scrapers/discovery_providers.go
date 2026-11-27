package scrapers

import (
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

var discoveryDocuments = newCache[*goquery.Document](16, time.Minute)

func discoveryDocument(provider, address string) (*goquery.Document, error) {
	return discoveryDocuments.get(address, func() (*goquery.Document, error) { doc, _, err := providerDocument(provider, address); return doc, err })
}
func providerDiscovery(provider, kind string) ([]discoveryCard, error) {
	switch provider {
	case "mangadex":
		return mdDiscovery(kind)
	case "weebcentral":
		return wcDiscovery(kind)
	case "mangapill":
		return pillDiscovery(kind)
	case "mangaread":
		return mrDiscovery(kind)
	}
	return nil, fmt.Errorf("unknown discovery source")
}
func discoveryTime(value string) string {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02", "02.01.2006"} {
		if t, err := time.Parse(layout, strings.TrimSpace(value)); err == nil && !t.After(time.Now().Add(5*time.Minute)) {
			return t.UTC().Format("2006-01-02T15:04:05.000000000Z")
		}
	}
	return ""
}
func discoveryRelativeTime(value string, now time.Time) string {
	if parsed := discoveryTime(value); parsed != "" {
		return parsed
	}
	parts := strings.Fields(strings.ToLower(strings.TrimSpace(value)))
	if len(parts) != 3 || parts[2] != "ago" {
		return ""
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil || n < 0 {
		return ""
	}
	duration := time.Duration(0)
	switch strings.TrimSuffix(parts[1], "s") {
	case "min", "minute":
		duration = time.Minute
	case "hour", "hr":
		duration = time.Hour
	case "day":
		duration = 24 * time.Hour
	case "week":
		duration = 7 * 24 * time.Hour
	default:
		return ""
	}
	if time.Duration(n) > (10*365*24*time.Hour)/duration {
		return ""
	}
	return now.Add(-time.Duration(n) * duration).UTC().Format("2006-01-02T15:04:05.000000000Z")
}
func newDiscoveryCard(provider, id, name, image, label string, rank int) discoveryCard {
	primary, fallback := imageDelivery(image)
	return discoveryCard{catalogItem: catalogItem{Slug: seriesRef(provider, id), Title: cleanText(name), Image: primary, ImageFallback: fallback, Chapters: []string{}}, Signals: []discoverySignal{{Provider: provider, Label: label, Rank: rank}}}
}
func sectionHeading(doc *goquery.Document, text string) *goquery.Selection {
	return doc.Find("h2,h3,h4").FilterFunction(func(_ int, h *goquery.Selection) bool { return strings.Contains(cleanText(h.Text()), text) }).First()
}
func wcDiscovery(kind string) ([]discoveryCard, error) {
	doc, err := discoveryDocument("weebcentral", "https://weebcentral.com/")
	if err != nil {
		return nil, err
	}
	if kind == "popular" {
		ranked, e := discoveryDocument("weebcentral", "https://weebcentral.com/hot-series?sort=weekly_views")
		if e == nil {
			if items := parseWCLinks(ranked.Selection, "Weekly popularity"); len(items) > 0 {
				return items, nil
			}
		}
		// The XHR ranking can be blocked even while the public home feed works.
		// Never mislabel the fallback as a weekly view ranking.
		items := parseWCCards(sectionHeading(doc, "Hot Updates").Parent(), "Hot updates", false)
		return items, nil
	}
	if kind == "recent" {
		return parseWCCards(sectionHeading(doc, "Latest Updates").Parent(), "Latest updates", true), nil
	}
	return parseWCLinks(sectionHeading(doc, "Recently Added").Parent(), "Newly added"), nil
}
func parseWCLinks(scope *goquery.Selection, label string) []discoveryCard {
	out := []discoveryCard{}
	seen := map[string]bool{}
	scope.Find("a[href*='/series/']").Each(func(_ int, a *goquery.Selection) {
		u, e := url.Parse(a.AttrOr("href", ""))
		if e != nil || (u.Host != "" && u.Host != "weebcentral.com") {
			return
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) < 2 || parts[0] != "series" || !ulidPattern.MatchString(parts[1]) || seen[parts[1]] {
			return
		}
		id := parts[1]
		seen[id] = true
		out = append(out, newDiscoveryCard("weebcentral", id, a.Text(), "https://temp.compsci88.com/cover/fallback/"+id+".jpg", label, len(out)+1))
	})
	return limitDiscovery(out)
}

func parseWCCards(scope *goquery.Selection, label string, recent bool) []discoveryCard {
	out := []discoveryCard{}
	seen := map[string]bool{}
	// Both desktop and mobile variants may be in the same document.
	candidates := scope.Find("article")
	if candidates.Length() == 0 {
		candidates = scope.Find("a[href*='/series/']").FilterFunction(func(_ int, a *goquery.Selection) bool { return a.Find("img").Length() > 0 })
	}
	candidates.Each(func(_ int, card *goquery.Selection) {
		cover := card.Find("img[src*='/cover/']").First()
		address := cover.AttrOr("src", "")
		u, e := url.Parse(address)
		if e != nil || u.Hostname() != "temp.compsci88.com" {
			return
		}
		file := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(strings.Split(u.Path, "/")[len(strings.Split(u.Path, "/"))-1], ".jpg"), ".webp"), ".png")
		if !ulidPattern.MatchString(file) || seen[file] {
			return
		}
		seen[file] = true
		title := card.AttrOr("data-tip", strings.TrimSuffix(cover.AttrOr("alt", ""), " cover"))
		item := newDiscoveryCard("weebcentral", file, title, address, label, len(out)+1)
		card.Find("a[href*='/chapters/'] span").Each(func(_ int, s *goquery.Selection) {
			text := cleanText(s.Text())
			if chapterDigits.MatchString(text) && len(item.Chapters) == 0 {
				item.Chapters = append(item.Chapters, text)
			}
		})
		if recent {
			item.UpdatedAt = discoveryTime(card.Find("time[datetime]").First().AttrOr("datetime", ""))
		}
		out = append(out, item)
	})
	return limitDiscovery(out)
}
func pillDiscovery(kind string) ([]discoveryCard, error) {
	address := "https://mangapill.com/"
	if kind == "recent" {
		address += "chapters"
	}
	if kind == "new" {
		address += "mangas/new"
	}
	doc, err := discoveryDocument("mangapill", address)
	if err != nil {
		return nil, err
	}
	scope := doc.Selection
	label := "Trending list"
	if kind == "popular" {
		scope = sectionHeading(doc, "Trending Mangas").Parent().Parent()
	}
	if kind == "recent" {
		label = "Recent chapters"
	}
	if kind == "new" {
		label = "Newly added"
	}
	return parsePillDiscovery(scope, label, kind), nil
}
func parsePillDiscovery(scope *goquery.Selection, label, kind string) []discoveryCard {
	out := []discoveryCard{}
	seen := map[string]bool{}
	base, _ := url.Parse("https://mangapill.com/")
	scope.Find("a[href^='/manga/']").Each(func(_ int, a *goquery.Selection) {
		id := a.AttrOr("href", "")
		if !validMangaPillPath(id) || seen[id] || cleanText(a.Text()) == "" {
			return
		}
		card := a.Parent().Parent()
		if card.Find("img").Length() == 0 {
			return
		}
		title := cleanText(a.Find("div").First().Text())
		if title == "" {
			title = cleanText(a.Text())
		}
		seen[id] = true
		item := newDiscoveryCard("mangapill", id, title, imageURL(base, card.Find("img").First()), label, len(out)+1)
		if kind == "recent" {
			item.UpdatedAt = discoveryTime(card.Find("time-ago[datetime]").First().AttrOr("datetime", ""))
			chapter := cleanText(card.Find("a[href^='/chapters/'] div").First().Text())
			if chapter != "" {
				item.Chapters = []string{strings.Replace(chapter, "#", "Chapter ", 1)}
			}
		}
		out = append(out, item)
	})
	return limitDiscovery(out)
}
func mrDiscovery(kind string) ([]discoveryCard, error) {
	address := "https://www.mangaread.org/"
	if kind == "new" {
		address += "manga/?m_orderby=new-manga"
	}
	doc, err := discoveryDocument("mangaread", address)
	if err != nil {
		return nil, err
	}
	return parseMRDiscovery(doc, kind), nil
}
func parseMRDiscovery(doc *goquery.Document, kind string) []discoveryCard {
	selector, titleSelector, label := ".popular-item-wrap", "h5 a", "Popular list"
	if kind != "popular" {
		selector, titleSelector = ".page-item-detail.manga", searchTitle
		label = "Latest updates"
	}
	if kind == "new" {
		label = "Newly added"
	}
	out := []discoveryCard{}
	seen := map[string]bool{}
	doc.Find(selector).Each(func(_ int, card *goquery.Selection) {
		a := card.Find(titleSelector).First()
		u, parts := sourcePath(source, a.AttrOr("href", ""))
		if u == nil || len(parts) != 2 || !mangaSlug.MatchString(parts[1]) || seen[parts[1]] {
			return
		}
		seen[parts[1]] = true
		item := newDiscoveryCard("mangaread", parts[1], a.Text(), imageURL(source, card.Find("img").First()), label, len(out)+1)
		card.Find(".chapter-item .chapter a").Each(func(_ int, c *goquery.Selection) {
			if len(item.Chapters) < 2 {
				item.Chapters = append(item.Chapters, cleanText(c.Text()))
			}
		})
		if kind == "recent" {
			item.UpdatedAt = discoveryRelativeTime(card.Find(".post-on").First().Text(), time.Now())
		}
		out = append(out, item)
	})
	return limitDiscovery(out)
}
func limitDiscovery(items []discoveryCard) []discoveryCard {
	out := []discoveryCard{}
	for _, item := range items {
		if item.Image != "" && item.Title != "" {
			out = append(out, item)
			if len(out) == 24 {
				break
			}
		}
	}
	return out
}
func mdDiscovery(kind string) ([]discoveryCard, error) {
	params := url.Values{"limit": {"24"}, "includes[]": {"cover_art"}, "contentRating[]": {"safe", "suggestive"}, "availableTranslatedLanguage[]": {"en"}}
	var entries []mdEntity
	latest := map[string]string{}
	chapters := map[string]string{}
	if kind == "recent" {
		ids := []string{}
		for _, mode := range []string{"0", "1"} {
			var feed struct {
				Data []mdEntity `json:"data"`
			}
			err := mdJSON("/chapter", url.Values{"limit": {"60"}, "translatedLanguage[]": {"en"}, "order[readableAt]": {"desc"}, "includeExternalUrl": {mode}, "contentRating[]": {"safe", "suggestive"}}, &feed)
			if err != nil {
				return nil, err
			}
			for _, c := range feed.Data {
				timestamp := discoveryTime(c.Attributes.ReadableAt)
				if timestamp == "" {
					continue
				}
				for _, r := range c.Relationships {
					if r.Type == "manga" && uuidPattern.MatchString(r.ID) {
						if _, ok := latest[r.ID]; !ok {
							ids = append(ids, r.ID)
						}
						if timestamp > latest[r.ID] {
							latest[r.ID] = timestamp
							if c.Attributes.Chapter != nil {
								chapters[r.ID] = "Chapter " + *c.Attributes.Chapter
							} else {
								chapters[r.ID] = "New release"
							}
						}
					}
				}
			}
		}
		sortMDIDs(ids, latest)
		if len(ids) > 24 {
			ids = ids[:24]
		}
		if len(ids) == 0 {
			return nil, fmt.Errorf("no readable English releases")
		}
		params["ids[]"] = ids
		params.Del("availableTranslatedLanguage[]")
	} else {
		order := "followedCount"
		if kind == "new" {
			order = "createdAt"
		}
		params.Set("order["+order+"]", "desc")
	}
	var response struct {
		Data []mdEntity `json:"data"`
	}
	if err := mdJSON("/manga", params, &response); err != nil {
		return nil, err
	}
	entries = response.Data
	out := []discoveryCard{}
	ids := []string{}
	for _, entity := range entries {
		s := mdConvert(entity)
		label := "Follower ranking"
		if kind == "recent" {
			label = "English releases"
		}
		if kind == "new" {
			label = "Newly cataloged"
		}
		item := newDiscoveryCard("mangadex", s.ID, s.Name, s.Image, label, len(out)+1)
		item.Description = s.Description
		if runes := []rune(item.Description); len(runes) > 1600 {
			item.Description = string(runes[:1600]) + "…"
		}
		if kind == "recent" {
			item.UpdatedAt = latest[s.ID]
			item.Chapters = []string{chapters[s.ID]}
			item.Signals[0].Rank = 0
		}
		if kind == "new" {
			item.AddedAt = discoveryTime(entity.Attributes.CreatedAt)
		}
		out = append(out, item)
		ids = append(ids, s.ID)
	}
	if kind == "popular" && len(ids) > 0 {
		var statistics struct {
			Statistics map[string]struct {
				Follows int `json:"follows"`
				Rating  struct {
					Bayesian float64 `json:"bayesian"`
				} `json:"rating"`
			} `json:"statistics"`
		}
		if err := mdJSON("/statistics/manga", url.Values{"manga[]": ids}, &statistics); err == nil {
			for i := range out {
				stat := statistics.Statistics[strings.TrimPrefix(out[i].Slug, "md_")]
				out[i].Signals[0].Followers = stat.Follows
				out[i].Signals[0].Rating = stat.Rating.Bayesian
			}
		}
	}
	if kind == "recent" {
		slices.SortStableFunc(out, func(a, b discoveryCard) int { return strings.Compare(b.UpdatedAt, a.UpdatedAt) })
	}
	return limitDiscovery(out), nil
}
func sortMDIDs(ids []string, times map[string]string) {
	slices.SortStableFunc(ids, func(a, b string) int { return strings.Compare(times[b], times[a]) })
}
