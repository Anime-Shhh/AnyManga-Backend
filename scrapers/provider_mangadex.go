package scrapers

import (
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
)

type mdEntity struct {
	ID         string `json:"id"`
	Attributes struct {
		CreatedAt                    string              `json:"createdAt"`
		ReadableAt                   string              `json:"readableAt"`
		Title                        json.RawMessage     `json:"title"`
		AltTitles                    []map[string]string `json:"altTitles"`
		Description                  map[string]string   `json:"description"`
		OriginalLanguage             string              `json:"originalLanguage"`
		AvailableTranslatedLanguages []string            `json:"availableTranslatedLanguages"`
		Status                       string              `json:"status"`
		Chapter                      *string             `json:"chapter"`
		Volume                       *string             `json:"volume"`
		TranslatedLanguage           string              `json:"translatedLanguage"`
		ExternalURL                  string              `json:"externalUrl"`
		Pages                        int                 `json:"pages"`
		Tags                         []struct {
			Attributes struct {
				Name map[string]string `json:"name"`
			} `json:"attributes"`
		} `json:"tags"`
	} `json:"attributes"`
	Relationships []struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Attributes struct {
			FileName string `json:"fileName"`
			Name     string `json:"name"`
		} `json:"attributes"`
	} `json:"relationships"`
}

func mdText(values map[string]string) string {
	if values["en"] != "" {
		return values["en"]
	}
	if values["ja-ro"] != "" {
		return values["ja-ro"]
	}
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
func mdTitles(entity mdEntity) map[string]string {
	var titles map[string]string
	_ = json.Unmarshal(entity.Attributes.Title, &titles)
	return titles
}
func mdConvert(entity mdEntity) providerSeries {
	s := providerSeries{Provider: "mangadex", ID: entity.ID, Name: mdText(mdTitles(entity)), Description: mdText(entity.Attributes.Description), Kind: entity.Attributes.OriginalLanguage, Status: entity.Attributes.Status, Languages: entity.Attributes.AvailableTranslatedLanguages}
	for _, t := range mdTitles(entity) {
		s.Aliases = append(s.Aliases, t)
	}
	for _, titles := range entity.Attributes.AltTitles {
		for _, t := range titles {
			s.Aliases = append(s.Aliases, t)
		}
	}
	for _, r := range entity.Relationships {
		if r.Type == "cover_art" && r.Attributes.FileName != "" {
			s.Image = "https://uploads.mangadex.org/covers/" + entity.ID + "/" + r.Attributes.FileName + ".256.jpg"
		}
		if r.Type == "author" {
			s.Authors = append(s.Authors, r.Attributes.Name)
		}
	}
	for _, tag := range entity.Attributes.Tags {
		s.Genres = append(s.Genres, mdText(tag.Attributes.Name))
	}
	return s
}
func mdSearch(query string, page int) ([]providerSeries, error) {
	var response struct {
		Data []mdEntity `json:"data"`
	}
	err := mdJSON("/manga", url.Values{"title": {query}, "limit": {"25"}, "offset": {strconv.Itoa((page - 1) * 25)}, "includes[]": {"cover_art", "author", "artist"}, "order[relevance]": {"desc"}}, &response)
	out := []providerSeries{}
	for _, entity := range response.Data {
		out = append(out, mdConvert(entity))
	}
	return out, err
}
func mdSeries(id, language string) (*providerSeries, error) {
	var response struct {
		Data mdEntity `json:"data"`
	}
	if err := mdJSON("/manga/"+id, url.Values{"includes[]": {"cover_art", "author", "artist"}}, &response); err != nil {
		return nil, err
	}
	s := mdConvert(response.Data)
	if s.Name == "" {
		return nil, errors.New("unrecognized MangaDex metadata")
	}
	for _, externalOnly := range []string{"0", "1"} {
		for offset := 0; offset < 10000; offset += 500 {
			var feed struct {
				Data  []mdEntity `json:"data"`
				Total int        `json:"total"`
			}
			err := mdJSON("/manga/"+id+"/feed", url.Values{"translatedLanguage[]": {language}, "limit": {"500"}, "offset": {strconv.Itoa(offset)}, "order[chapter]": {"asc"}, "includes[]": {"scanlation_group"}, "includeExternalUrl": {externalOnly}, "contentRating[]": {"safe", "suggestive", "erotica", "pornographic"}}, &feed)
			if err != nil {
				break
			}
			for _, entity := range feed.Data {
				a := entity.Attributes
				c := sourceChapter{Provider: "mangadex", ID: entity.ID, Language: a.TranslatedLanguage, ExternalURL: a.ExternalURL}
				if a.Chapter != nil {
					c.Number = canonicalNumber(*a.Chapter)
					c.Title = "Chapter " + *a.Chapter
				} else {
					c.Title = "Special release"
				}
				if a.Volume != nil {
					c.Volume = *a.Volume
				}
				// A MangaDex feed can list publisher links with no locally hosted pages.
				if a.Pages == 0 && c.ExternalURL == "" {
					continue
				}
				if c.ExternalURL != "" {
					u, err := url.Parse(c.ExternalURL)
					if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
						continue
					}
				}
				for _, r := range entity.Relationships {
					if r.Type == "scanlation_group" {
						c.Groups = append(c.Groups, r.Attributes.Name)
					}
				}
				s.Chapters = append(s.Chapters, c)
			}
			if offset+len(feed.Data) >= feed.Total || len(feed.Data) == 0 {
				break
			}
		}
	}
	return &s, nil
}
func mdPages(id string) ([]string, error) {
	if !uuidPattern.MatchString(id) {
		return nil, errors.New("invalid MangaDex chapter")
	}
	var manifest struct {
		BaseURL string `json:"baseUrl"`
		Chapter struct {
			Hash string   `json:"hash"`
			Data []string `json:"data"`
		} `json:"chapter"`
	}
	if err := mdJSON("/at-home/server/"+id, url.Values{"forcePort443": {"true"}}, &manifest); err != nil {
		return nil, err
	}
	base, err := url.Parse(manifest.BaseURL)
	if err != nil || !allowedImageURL(base) || manifest.Chapter.Hash == "" {
		return nil, errors.New("invalid MangaDex image server")
	}
	pages := []string{}
	for _, file := range manifest.Chapter.Data {
		pages = append(pages, manifest.BaseURL+"/data/"+manifest.Chapter.Hash+"/"+file)
	}
	if len(pages) == 0 {
		return nil, errors.New("MangaDex chapter has no hosted images")
	}
	return pages, nil
}
