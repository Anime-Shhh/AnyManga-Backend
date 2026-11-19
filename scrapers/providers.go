package scrapers

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

var seriesCache = newCache[*providerSeries](512, 5*time.Minute)
var providerSearchCache = newCache[[]providerSeries](512, 3*time.Minute)

func sourceSearch(provider, query string, page int) ([]providerSeries, error) {
	return providerSearchCache.get(fmt.Sprintf("%s:%d:%s", provider, page, query), func() ([]providerSeries, error) {
		if provider == "mangadex" {
			return mdSearch(query, page)
		}
		if provider == "mangaread" {
			result, err := cachedSearch(query, page)
			if err != nil {
				return nil, err
			}
			out := []providerSeries{}
			for _, item := range result.Results {
				out = append(out, providerSeries{Provider: provider, ID: item.Slug, Name: item.Title, Image: item.Image})
			}
			return out, nil
		}
		address := "https://mangapill.com/search?" + url.Values{"q": {query}, "page": {strconv.Itoa(page)}}.Encode()
		if provider == "weebcentral" {
			address = "https://weebcentral.com/search/data?" + url.Values{"text": {query}, "limit": {"24"}, "offset": {strconv.Itoa((page - 1) * 24)}, "sort": {"Best Match"}, "order": {"Descending"}, "official": {"Any"}, "anime": {"Any"}, "adult": {"Any"}, "display_mode": {"Full Display"}}.Encode()
		}
		doc, base, err := providerDocument(provider, address)
		if err != nil {
			return nil, err
		}
		selector := "a[href*='/manga/']"
		if provider == "weebcentral" {
			selector = "a[href*='/series/']"
		}
		out := []providerSeries{}
		seen := map[string]bool{}
		doc.Find(selector).Each(func(_ int, a *goquery.Selection) {
			if provider == "mangapill" && a.Find("img").Length() > 0 {
				return
			}
			link, err := base.Parse(a.AttrOr("href", ""))
			if err != nil || link.Host != base.Host {
				return
			}
			parts := strings.Split(strings.Trim(link.Path, "/"), "/")
			id := link.Path
			if provider == "weebcentral" {
				if len(parts) != 3 || !ulidPattern.MatchString(parts[1]) {
					return
				}
				id = parts[1]
			} else if !validMangaPillPath(id) {
				return
			}
			if seen[id] {
				return
			}
			name := cleanText(a.Text())
			name = strings.TrimPrefix(name, "Official ")
			if img := a.Find("img").First(); img.Length() > 0 {
				alt := img.AttrOr("alt", "")
				if alt != "" {
					name = strings.TrimSuffix(alt, " cover")
				}
			}
			if name == "" {
				return
			}
			seen[id] = true
			image := imageURL(base, a.Find("img").First())
			if provider == "mangapill" {
				image = imageURL(base, a.Parent().Parent().Find("img").First())
			}
			out = append(out, providerSeries{Provider: provider, ID: id, Name: name, Image: image})
		})
		return out, nil
	})
}

func sourceSeries(provider, id, language string) (*providerSeries, error) {
	return seriesCache.get(provider+":"+id+":"+language, func() (*providerSeries, error) {
		if provider == "mangadex" {
			return mdSeries(id, language)
		}
		if provider == "mangaread" {
			index, err := getIndex(id)
			if err != nil {
				return nil, err
			}
			s := &providerSeries{Provider: provider, ID: id, Name: index.Name, Image: index.Image, Description: index.Description, Kind: index.Kind, Aliases: index.AltTitles}
			for _, entry := range index.Entries {
				s.Chapters = append(s.Chapters, makeSourceChapter(provider, entry.ID, entry.Title, entry.URL))
			}
			return s, nil
		}
		address := "https://mangapill.com" + id
		if provider == "weebcentral" {
			address = "https://weebcentral.com/series/" + id
		}
		doc, base, err := providerDocument(provider, address)
		if err != nil {
			return nil, err
		}
		s := &providerSeries{Provider: provider, ID: id, Name: cleanText(doc.Find("h1").First().Text()), Image: doc.Find("meta[property='og:image']").AttrOr("content", ""), Description: doc.Find("meta[name='description']").AttrOr("content", "")}
		if s.Name == "" {
			s.Name = strings.Split(doc.Find("meta[property='og:title']").AttrOr("content", ""), " | ")[0]
		}
		if s.Name == "" {
			return nil, errors.New("unrecognized series markup")
		}
		chapterDoc := doc
		selector := "#chapters a"
		if provider == "weebcentral" {
			doc.Find("li").Each(func(_ int, li *goquery.Selection) {
				key := cleanText(li.Find("strong").First().Text())
				if key == "Type:" {
					s.Kind = strings.ToLower(cleanText(li.Find("a").First().Text()))
				}
			})
			s.Image = imageURL(base, doc.Find("img[alt*='cover']").First())
			chapterDoc, _, err = providerDocument(provider, "https://weebcentral.com/series/"+id+"/full-chapter-list")
			if err != nil {
				return nil, err
			}
			selector = "a[href*='/chapters/']"
		} else {
			doc.Find("label").Each(func(_ int, l *goquery.Selection) {
				if cleanText(l.Text()) == "Type" {
					s.Kind = cleanText(l.Next().Text())
				}
			})
		}
		seen := map[string]bool{}
		chapterDoc.Find(selector).Each(func(_ int, a *goquery.Selection) {
			link, err := base.Parse(a.AttrOr("href", ""))
			if err != nil || link.Host != base.Host || !strings.HasPrefix(link.Path, "/chapters/") || seen[link.Path] {
				return
			}
			title := cleanText(a.Text())
			if pos := strings.Index(title, " Last Read"); pos >= 0 {
				title = title[:pos]
			}
			if title == "" {
				return
			}
			seen[link.Path] = true
			parts := strings.Split(strings.Trim(link.Path, "/"), "/")
			s.Chapters = append(s.Chapters, makeSourceChapter(provider, parts[1], title, link.String()))
		})
		slices.Reverse(s.Chapters)
		return s, nil
	})
}
func makeSourceChapter(provider, id, title, address string) sourceChapter {
	c := sourceChapter{Provider: provider, ID: id, Title: title, URL: address, Language: "en"}
	if match := chapterDigits.FindStringSubmatch(title); len(match) > 1 {
		c.Number = canonicalNumber(match[1])
	}
	return c
}
func canonicalNumber(number string) string {
	n, err := strconv.ParseFloat(number, 64)
	if err != nil {
		return ""
	}
	return strconv.FormatFloat(n, 'f', -1, 64)
}

func sourcePages(chapter sourceChapter) ([]string, error) {
	if chapter.ExternalURL != "" {
		return nil, errors.New("chapter is an external release")
	}
	if chapter.Provider == "mangadex" {
		return mdPages(chapter.ID)
	}
	if chapter.Provider == "mangaread" {
		return loadPages(chapterLink{URL: chapter.URL})
	}
	return pageCache.get(chapter.Provider+":"+chapter.ID, func() ([]string, error) {
		address := chapter.URL
		selector := "img.js-page"
		if chapter.Provider == "weebcentral" {
			address += "/images?reading_style=long_strip"
			selector = "#chapter-images img"
		}
		doc, base, err := providerDocument(chapter.Provider, address)
		if err != nil {
			return nil, err
		}
		pages := []string{}
		doc.Find(selector).Each(func(_ int, img *goquery.Selection) {
			if address := imageURL(base, img); address != "" {
				pages = append(pages, address)
			}
		})
		if len(pages) == 0 {
			return nil, errors.New("chapter has no recognized page images")
		}
		return pages, nil
	})
}
