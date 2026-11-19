package scrapers

import (
	"encoding/base64"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

type providerSeries struct {
	Provider    string
	ID          string
	Name        string
	Aliases     []string
	Image       string
	Description string
	Kind        string
	Status      string
	Authors     []string
	Genres      []string
	Languages   []string
	Chapters    []sourceChapter
}

type sourceChapter struct {
	Provider    string   `json:"provider"`
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Number      string   `json:"number"`
	Volume      string   `json:"volume,omitempty"`
	Language    string   `json:"language"`
	URL         string   `json:"-"`
	ExternalURL string   `json:"externalUrl,omitempty"`
	Groups      []string `json:"groups,omitempty"`
}

type sourceInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Chapters int    `json:"chapters"`
}

var providerLabels = map[string]string{"mangadex": "MangaDex", "weebcentral": "WeebCentral", "mangapill": "MangaPill", "mangaread": "MangaRead"}
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var ulidPattern = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)
var chapterDigits = regexp.MustCompile(`(?i)(?:chapter|ch[.]?|episode)[\s_-]*([0-9]+(?:\.[0-9]+)?)`)

func seriesRef(provider, id string) string {
	switch provider {
	case "mangadex":
		return "md_" + id
	case "weebcentral":
		return "wc_" + id
	case "mangapill":
		return "mp_" + base64.RawURLEncoding.EncodeToString([]byte(id))
	}
	return id
}
func parseSeriesRef(ref string) (string, string, error) {
	switch {
	case strings.HasPrefix(ref, "md_"):
		id := strings.TrimPrefix(ref, "md_")
		if uuidPattern.MatchString(id) {
			return "mangadex", id, nil
		}
	case strings.HasPrefix(ref, "wc_"):
		id := strings.TrimPrefix(ref, "wc_")
		if ulidPattern.MatchString(id) {
			return "weebcentral", id, nil
		}
	case strings.HasPrefix(ref, "mp_"):
		data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(ref, "mp_"))
		if err == nil {
			id := string(data)
			if validMangaPillPath(id) {
				return "mangapill", id, nil
			}
		}
	default:
		if slug, ok := validSlug(ref); ok {
			return "mangaread", slug, nil
		}
	}
	return "", "", errors.New("invalid series identifier")
}
func validMangaPillPath(path string) bool {
	u, err := url.Parse(path)
	if err != nil || u.IsAbs() || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	return len(parts) == 3 && parts[0] == "manga" && regexp.MustCompile(`^[0-9]+$`).MatchString(parts[1]) && mangaSlug.MatchString(parts[2])
}
func normalizedTitle(title string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, title)
}
func titlesMatch(candidate string, titles []string) bool {
	key := normalizedTitle(candidate)
	if key == "" {
		return false
	}
	for _, title := range titles {
		if key == normalizedTitle(title) {
			return true
		}
	}
	return false
}
func readingOrder(kind string) []string {
	if strings.EqualFold(kind, "manhwa") || strings.EqualFold(kind, "manhua") || kind == "ko" || kind == "zh" || kind == "zh-hk" {
		return []string{"weebcentral", "mangaread", "mangadex", "mangapill"}
	}
	return []string{"weebcentral", "mangapill", "mangaread", "mangadex"}
}
