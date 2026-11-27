package scrapers

import (
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"hash/fnv"
	"net/http"
	"slices"
	"sort"
	"sync"
	"time"
)

type discoverySignal struct {
	Provider  string  `json:"provider"`
	Label     string  `json:"label"`
	Rank      int     `json:"rank,omitempty"`
	Followers int     `json:"followers,omitempty"`
	Rating    float64 `json:"rating,omitempty"`
}
type discoveryCard struct {
	catalogItem
	Signals         []discoverySignal `json:"signals"`
	UpdatedAt       string            `json:"updatedAt,omitempty"`
	AddedAt         string            `json:"addedAt,omitempty"`
	SpotlightKind   string            `json:"spotlightKind,omitempty"`
	SpotlightReason string            `json:"spotlightReason,omitempty"`
	score           float64
}
type discoveryFeed struct {
	Items     []discoveryCard
	FetchedAt time.Time
}
type discoveryAttempt struct {
	Feed  discoveryFeed
	Stale bool
	Err   error
}
type discoverySection struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Items       []discoveryCard `json:"items"`
}
type discoverySource struct {
	Provider    string   `json:"provider"`
	Name        string   `json:"name"`
	Available   []string `json:"availableFeeds"`
	Stale       []string `json:"staleFeeds"`
	Unavailable []string `json:"unavailableFeeds"`
}
type discoveryResponse struct {
	Featured  []discoveryCard    `json:"featured"`
	Sections  []discoverySection `json:"sections"`
	Sources   []discoverySource  `json:"sources"`
	UpdatedAt string             `json:"updatedAt"`
}

var discoveryProviders = []string{"mangadex", "weebcentral", "mangapill", "mangaread"}
var discoveryKinds = []string{"popular", "recent", "new"}
var discoveryFeeds = newCache[discoveryFeed](16, 5*time.Minute)
var discoveryAttempts = newCache[discoveryAttempt](16, time.Minute)
var discoverySnapshots = newCache[discoveryResponse](1, time.Minute)
var discoveryLastGood = struct {
	sync.Mutex
	values map[string]discoveryFeed
}{values: map[string]discoveryFeed{}}

func cachedDiscoveryFeed(provider, kind string) discoveryAttempt {
	key := provider + ":" + kind
	result, _ := discoveryAttempts.get(key, func() (discoveryAttempt, error) {
		feed, err := discoveryFeeds.get(key, func() (discoveryFeed, error) {
			items, e := providerDiscovery(provider, kind)
			if e != nil {
				return discoveryFeed{}, e
			}
			if len(items) == 0 {
				return discoveryFeed{}, errors.New("empty or unrecognized discovery feed")
			}
			f := discoveryFeed{Items: items, FetchedAt: time.Now().UTC()}
			discoveryLastGood.Lock()
			discoveryLastGood.values[key] = f
			discoveryLastGood.Unlock()
			return f, nil
		})
		if err == nil {
			return discoveryAttempt{Feed: feed}, nil
		}
		discoveryLastGood.Lock()
		last, ok := discoveryLastGood.values[key]
		discoveryLastGood.Unlock()
		if ok && time.Since(last.FetchedAt) < 30*time.Minute {
			return discoveryAttempt{Feed: last, Stale: true, Err: err}, nil
		}
		return discoveryAttempt{Err: err}, nil
	})
	return result
}
func discoverySnapshot() (discoveryResponse, error) {
	return discoverySnapshots.get("all", func() (discoveryResponse, error) {
		type result struct {
			provider, kind string
			attempt        discoveryAttempt
		}
		out := make(chan result, 12)
		for _, p := range discoveryProviders {
			for _, k := range discoveryKinds {
				go func(p, k string) { out <- result{p, k, cachedDiscoveryFeed(p, k)} }(p, k)
			}
		}
		feeds := map[string]map[string]discoveryAttempt{}
		for range 12 {
			r := <-out
			if feeds[r.provider] == nil {
				feeds[r.provider] = map[string]discoveryAttempt{}
			}
			feeds[r.provider][r.kind] = r.attempt
		}
		response := discoveryResponse{UpdatedAt: time.Now().UTC().Format(time.RFC3339), Sources: []discoverySource{}, Sections: []discoverySection{}}
		for _, p := range discoveryProviders {
			source := discoverySource{Provider: p, Name: providerLabels[p], Available: []string{}, Stale: []string{}, Unavailable: []string{}}
			for _, k := range discoveryKinds {
				r := feeds[p][k]
				if len(r.Feed.Items) > 0 {
					source.Available = append(source.Available, k)
					if r.Stale {
						source.Stale = append(source.Stale, k)
					}
				} else {
					source.Unavailable = append(source.Unavailable, k)
				}
			}
			response.Sources = append(response.Sources, source)
		}
		total := 0
		for _, k := range discoveryKinds {
			combined := [][]discoveryCard{}
			for _, p := range discoveryProviders {
				combined = append(combined, feeds[p][k].Feed.Items)
			}
			items := mergeDiscovery(k, combined, time.Now())
			title, description := "Popular across sources", "Provider rankings and MangaDex followers; not a combined view count."
			if k == "recent" {
				title, description = "Latest chapter releases", "Recently updated series, with release times where providers publish them."
			}
			if k == "new" {
				title, description = "Newly added series", "New to provider catalogs — not necessarily newly published manga."
			}
			response.Sections = append(response.Sections, discoverySection{ID: k, Title: title, Description: description, Items: items})
			total += len(items)
		}
		if total == 0 {
			return response, errors.New("discovery providers are temporarily unavailable")
		}
		return response, nil
	})
}
func GetDiscovery(c *gin.Context) {
	response, err := discoverySnapshot()
	if err != nil {
		reportError(c, err, "Discovery feeds are temporarily unavailable. Please try again.")
		return
	}
	response.Featured = discoverySpotlight(response.Sections, time.Now())
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, response)
}
func cardIdentity(c discoveryCard) string {
	key := normalizedTitle(c.Title)
	if key == "" {
		return c.Slug
	}
	return key
}
func mergeDiscovery(kind string, feeds [][]discoveryCard, now time.Time) []discoveryCard {
	cards := []discoveryCard{}
	positions := map[string]int{}
	for _, feed := range feeds {
		for rank, item := range feed {
			if item.Slug == "" || item.Title == "" || item.Image == "" {
				continue
			}
			// Do not merge ambiguous editions with identical titles on the same provider.
			key := cardIdentity(item)
			position, exists := positions[key]
			if exists {
				duplicateProvider := false
				for _, a := range cards[position].Signals {
					for _, b := range item.Signals {
						if a.Provider == b.Provider {
							duplicateProvider = true
						}
					}
				}
				if duplicateProvider {
					key += "|" + item.Slug
					position, exists = positions[key]
				}
			}
			item.score = 1 / float64(10+rank+1)
			item.Signals = slices.Clone(item.Signals)
			if !exists {
				positions[key] = len(cards)
				cards = append(cards, item)
				continue
			}
			target := &cards[position]
			target.score += item.score
			target.Signals = append(target.Signals, item.Signals...)
			if target.Description == "" {
				target.Description = item.Description
			}
			if item.UpdatedAt > target.UpdatedAt {
				target.UpdatedAt = item.UpdatedAt
				target.Chapters = slices.Clone(item.Chapters)
			}
			if item.AddedAt > target.AddedAt {
				target.AddedAt = item.AddedAt
			}
		}
	}
	sort.SliceStable(cards, func(i, j int) bool {
		if kind == "recent" && cards[i].UpdatedAt != cards[j].UpdatedAt {
			return cards[i].UpdatedAt > cards[j].UpdatedAt
		}
		// 'New' lists rarely expose comparable catalog dates; combine their list
		// positions instead of pretending a publication year is an addition date.
		if cards[i].score != cards[j].score {
			return cards[i].score > cards[j].score
		}
		return cards[i].Slug < cards[j].Slug
	})
	if len(cards) > 24 {
		cards = cards[:24]
	}
	return cards
}
func discoverySpotlight(sections []discoverySection, now time.Time) []discoveryCard {
	result := []discoveryCard{}
	seen := map[string]bool{}
	queues := make([][]discoveryCard, len(sections))
	for i, s := range sections {
		items := slices.Clone(s.Items)
		if len(items) > 16 {
			items = items[:16]
		}
		// Deterministic hourly variety within the actual ranked/recent candidate pool.
		h := fnv.New32a()
		fmt.Fprintf(h, "%s:%d", s.ID, now.UTC().Unix()/3600)
		if len(items) > 0 {
			offset := int(h.Sum32()) % len(items)
			items = append(items[offset:], items[:offset]...)
		}
		queues[i] = items
	}
	for round := 0; len(result) < 12; round++ {
		added := false
		for i, q := range queues {
			if round >= len(q) {
				continue
			}
			item := q[round]
			key := cardIdentity(item)
			if seen[key] {
				continue
			}
			seen[key] = true
			added = true
			item.SpotlightKind = sections[i].ID
			item.SpotlightReason = sections[i].Title
			result = append(result, item)
			if len(result) == 12 {
				break
			}
		}
		if !added {
			remaining := false
			for _, q := range queues {
				if round+1 < len(q) {
					remaining = true
				}
			}
			if !remaining {
				break
			}
		}
	}
	return result
}
