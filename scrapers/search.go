package scrapers

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/gin-gonic/gin"
)

const searchCards = ".c-tabs-item__content"
const searchTitle = ".post-title h3 a"

var searchTotal = regexp.MustCompile(`^([\d,]+) results?\b`)

type searchResults struct {
	Results []catalogItem `json:"results"`
	Total   int           `json:"total"`
	HasMore bool          `json:"hasMore"`
}

func parseCards(doc *goquery.Document, base *url.URL, cards, titles string) []catalogItem {
	items := []catalogItem{}
	seen := map[string]bool{}
	doc.Find(cards).Each(func(_ int, card *goquery.Selection) {
		anchor := card.Find(titles).First()
		_, parts := sourcePath(base, anchor.AttrOr("href", ""))
		title := cleanText(anchor.Text())
		if len(parts) != 2 || parts[0] != "manga" || !mangaSlug.MatchString(parts[1]) ||
			title == "" || seen[parts[1]] {
			return
		}
		seen[parts[1]] = true
		item := catalogItem{
			Slug: parts[1], Title: title, Image: imageURL(base, card.Find("img").First()),
			Chapters: []string{},
		}
		item.Image, item.ImageFallback = imageDelivery(item.Image)
		card.Find(".chapter-item span a").Each(func(_ int, chapter *goquery.Selection) {
			item.Chapters = append(item.Chapters, cleanText(chapter.Text()))
		})
		items = append(items, item)
	})
	return items
}

func cachedSearch(query string, page int) (searchResults, error) {
	key := strconv.Itoa(page) + "\n" + query
	return searchCache.get(key, func() (searchResults, error) {
		params := url.Values{"s": {query}, "post_type": {"wp-manga"}}
		itemsBeforePage := 0
		if page > 1 {
			first, err := cachedSearch(query, 1)
			if err != nil {
				return searchResults{}, err
			}
			itemsBeforePage = (page - 1) * len(first.Results)
			if !first.HasMore || itemsBeforePage >= first.Total {
				return searchResults{Results: []catalogItem{}, Total: first.Total}, nil
			}
			params.Set("paged", strconv.Itoa(page))
		}
		address := source.ResolveReference(&url.URL{Path: "/", RawQuery: params.Encode()})
		doc, base, err := loadDocument(address.String())
		if page > 1 && errors.Is(err, missing) {
			return searchResults{Results: []catalogItem{}, Total: itemsBeforePage}, nil
		}
		if err != nil {
			return searchResults{}, err
		}
		result := searchResults{Results: parseCards(doc, base, searchCards, searchTitle)}
		if len(result.Results) == 0 && doc.Find(".not-found-content").Length() == 0 {
			return searchResults{}, errors.New("MangaRead search markup not recognized")
		}
		result.Total = itemsBeforePage + len(result.Results)
		heading := cleanText(doc.Find(".search-wrap h1").First().Text())
		if match := searchTotal.FindStringSubmatch(heading); len(match) > 1 {
			if total, err := strconv.Atoi(strings.ReplaceAll(match[1], ",", "")); err == nil {
				result.Total = max(result.Total, total)
			}
		}
		result.HasMore = len(result.Results) > 0 && result.Total > itemsBeforePage+len(result.Results)
		return result, nil
	})
}

func Search(c *gin.Context) {
	query := strings.TrimSpace(c.Query("q"))
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if query == "" || len(query) > 400 || err != nil || page < 1 || page > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Enter a search of up to 400 bytes and a page between 1 and 100."})
		return
	}
	result, err := multiSearch(query, page)
	if err != nil {
		reportError(c, err, "Could not load search results from the available sources.")
		return
	}
	c.JSON(http.StatusOK, result)
}
