package scrapers

import "net/url"

func seriesCards(series []providerSeries) []catalogItem {
	items := []catalogItem{}
	for _, s := range series {
		primary, fallback := imageDelivery(s.Image)
		items = append(items, catalogItem{Slug: seriesRef(s.Provider, s.ID), Title: s.Name, Image: primary, ImageFallback: fallback, Description: s.Description, Chapters: []string{}})
	}
	return items
}
func multiSearch(query string, page int) (searchResults, error) {
	var last error
	for _, provider := range metadataOrder {
		if provider == "mangaread" {
			result, err := cachedSearch(query, page)
			if err == nil && len(result.Results) > 0 {
				return result, nil
			}
			last = err
			continue
		}
		results, err := sourceSearch(provider, query, page)
		if err != nil {
			last = err
			continue
		}
		if len(results) > 0 {
			size := 25
			if provider == "weebcentral" {
				size = 24
			}
			return searchResults{Results: seriesCards(results), Total: (page-1)*size + len(results), HasMore: len(results) >= size}, nil
		}
	}
	if last != nil {
		return searchResults{}, last
	}
	return searchResults{Results: []catalogItem{}}, nil
}

// Keep the existing curated rows; use the metadata catalog if their upstream fails.
func fallbackCatalog(kind string) ([]catalogItem, error) {
	params := url.Values{"limit": {"24"}, "includes[]": {"cover_art", "author"}, "order[followedCount]": {"desc"}, "contentRating[]": {"safe", "suggestive"}}
	switch kind {
	case "manhwa":
		params.Set("originalLanguage[]", "ko")
	case "manhua":
		params["originalLanguage[]"] = []string{"zh", "zh-hk"}
	default:
		params.Set("originalLanguage[]", "ja")
	}
	var response struct {
		Data []mdEntity `json:"data"`
	}
	if err := mdJSON("/manga", params, &response); err != nil {
		return nil, err
	}
	series := []providerSeries{}
	for _, entity := range response.Data {
		series = append(series, mdConvert(entity))
	}
	return seriesCards(series), nil
}
