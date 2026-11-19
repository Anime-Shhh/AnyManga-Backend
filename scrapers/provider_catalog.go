package scrapers

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var unifiedCache = newCache[*mangaIndex](256, 3*time.Minute)
var metadataOrder = []string{"mangadex", "mangaread", "weebcentral", "mangapill"}

func findSeries(provider string, titles []string, language string) (*providerSeries, error) {
	seen := map[string]bool{}
	for _, title := range titles {
		if seen[normalizedTitle(title)] {
			continue
		}
		seen[normalizedTitle(title)] = true
		if len(seen) > 3 {
			break
		}
		results, err := sourceSearch(provider, title, 1)
		if err != nil {
			return nil, err
		}
		matches := []providerSeries{}
		for _, candidate := range results {
			if titlesMatch(candidate.Name, titles) {
				matches = append(matches, candidate)
				continue
			}
			for _, alias := range candidate.Aliases {
				if titlesMatch(alias, titles) {
					matches = append(matches, candidate)
					break
				}
			}
		}
		// Ambiguous editions and adaptations require an explicit series choice.
		if len(matches) == 1 {
			return sourceSeries(provider, matches[0].ID, language)
		}
	}
	return nil, missing
}
func getUnifiedIndex(ref, language string) (*mangaIndex, error) {
	return unifiedCache.get(ref+":"+language, func() (*mangaIndex, error) {
		provider, id, err := parseSeriesRef(ref)
		if err != nil {
			return nil, err
		}
		anchor, err := sourceSeries(provider, id, language)
		if err != nil && provider == "mangaread" {
			guessed := strings.ReplaceAll(id, "-", " ")
			for _, p := range metadataOrder {
				if p == provider {
					continue
				}
				if match, e := findSeries(p, []string{guessed}, language); e == nil {
					anchor = match
					provider = p
					err = nil
					break
				}
			}
		}
		if err != nil {
			return nil, err
		}
		sources := map[string]*providerSeries{provider: anchor}
		titles := append([]string{anchor.Name}, anchor.Aliases...)
		if provider != "mangadex" {
			if md, err := findSeries("mangadex", titles, language); err == nil {
				sources["mangadex"] = md
				titles = append(titles, md.Aliases...)
			}
		}
		var wg sync.WaitGroup
		var mu sync.Mutex
		pending := []string{}
		for _, p := range metadataOrder {
			if sources[p] == nil {
				pending = append(pending, p)
			}
		}
		for _, p := range pending {
			wg.Add(1)
			go func(p string) {
				defer wg.Done()
				if s, err := findSeries(p, titles, language); err == nil {
					mu.Lock()
					sources[p] = s
					mu.Unlock()
				}
			}(p)
		}
		wg.Wait()
		return mergeSeries(ref, language, sources), nil
	})
}
func mergeSeries(ref, language string, sources map[string]*providerSeries) *mangaIndex {
	index := &mangaIndex{Slug: ref, Entries: []chapterLink{}, Chapters: []string{}, positions: map[string]int{}, aliases: map[string]int{}, numbers: map[string]int{}, candidates: map[string][]sourceChapter{}}
	for _, p := range metadataOrder {
		s := sources[p]
		if s == nil {
			continue
		}
		index.Sources = append(index.Sources, sourceInfo{ID: p, Name: providerLabels[p], Chapters: len(s.Chapters)})
		if index.Name == "" {
			index.Name = s.Name
			index.MetadataSource = p
		}
		if index.Description == "" {
			index.Description = s.Description
		}
		if index.Image == "" {
			index.Image = imageProxy(s.Image)
		}
		if index.Kind == "" {
			index.Kind = s.Kind
		}
		if len(index.Authors) == 0 {
			index.Authors = s.Authors
		}
		if len(index.Genres) == 0 {
			index.Genres = s.Genres
		}
		if len(index.Languages) == 0 {
			index.Languages = s.Languages
		}
	}
	index.Language = language
	groups := map[string][]sourceChapter{}
	orderedKeys := []string{}
	for _, p := range readingOrder(index.Kind) {
		s := sources[p]
		if s == nil {
			continue
		}
		counts := map[string]map[string]bool{}
		for _, c := range s.Chapters {
			if c.Number != "" {
				if counts[c.Number] == nil {
					counts[c.Number] = map[string]bool{}
				}
				counts[c.Number][c.Volume] = true
			}
		}
		for _, c := range s.Chapters {
			if c.Language != language {
				continue
			}
			key := "special:" + p + ":" + c.ID
			if c.Number != "" {
				key = "chapter-" + c.Number
				// Repeated numbers in different volumes must never silently match another volume.
				if len(counts[c.Number]) > 1 && c.Volume != "" {
					key = p + "-volume-" + c.Volume + "-chapter-" + c.Number
				}
			}
			if _, ok := groups[key]; !ok {
				orderedKeys = append(orderedKeys, key)
			}
			groups[key] = append(groups[key], c)
		}
	}
	sort.SliceStable(orderedKeys, func(i, j int) bool {
		a, b := groups[orderedKeys[i]][0], groups[orderedKeys[j]][0]
		if a.Number == "" {
			return false
		}
		if b.Number == "" {
			return true
		}
		an, _ := strconv.ParseFloat(a.Number, 64)
		bn, _ := strconv.ParseFloat(b.Number, 64)
		return an < bn
	})
	for _, key := range orderedKeys {
		candidates := groups[key]
		selected := candidates[0]
		id := key
		// Keep existing MangaRead bookmarks and download selections working.
		for _, c := range candidates {
			if c.Provider == "mangaread" {
				id = c.ID
				break
			}
		}
		position := len(index.Entries)
		entry := chapterLink{ID: id, Title: selected.Title}
		index.Entries = append(index.Entries, entry)
		index.Chapters = append(index.Chapters, entry.Title)
		index.positions[id] = position
		indexAlias(index.aliases, CleanURL(entry.Title), position)
		if selected.Number != "" {
			indexAlias(index.numbers, selected.Number, position)
		}
		for _, c := range candidates {
			if _, ok := index.positions[c.ID]; !ok {
				index.positions[c.ID] = position
			}
		}
		index.candidates[id] = candidates
	}
	return index
}

type chapterContent struct {
	Images      []string
	Provider    string
	ExternalURL string
	Groups      []string
}

var contentCache = newCache[chapterContent](512, 3*time.Minute)

func loadContent(index *mangaIndex, entry chapterLink, skip string) (chapterContent, error) {
	if index.candidates == nil {
		pages, err := loadPages(entry)
		return chapterContent{Images: pages, Provider: "mangaread"}, err
	}
	return contentCache.get(index.Slug+":"+index.Language+":"+entry.ID+":"+skip, func() (chapterContent, error) {
		var external *sourceChapter
		var failures []string
		for _, candidate := range index.candidates[entry.ID] {
			if slices.Contains(strings.Split(skip, ","), candidate.Provider) {
				continue
			}
			if candidate.ExternalURL != "" {
				if external == nil {
					copy := candidate
					external = &copy
				}
				continue
			}
			pages, err := sourcePages(candidate)
			if err != nil {
				failures = append(failures, candidate.Provider)
				continue
			}
			pages = slices.Clone(pages)
			for i, address := range pages {
				pages[i] = imageProxy(address)
			}
			return chapterContent{Images: pages, Provider: candidate.Provider, Groups: candidate.Groups}, nil
		}
		if external != nil {
			return chapterContent{Images: []string{}, Provider: external.Provider, ExternalURL: external.ExternalURL, Groups: external.Groups}, nil
		}
		if len(failures) == 0 {
			return chapterContent{}, missing
		}
		return chapterContent{}, fmt.Errorf("chapter sources unavailable: %s", strings.Join(failures, ", "))
	})
}

var prefetchSlots = make(chan struct{}, 2)

func prefetchNext(index *mangaIndex, position int) {
	if index.candidates == nil || position+1 >= len(index.Entries) {
		return
	}
	select {
	case prefetchSlots <- struct{}{}:
		go func() { defer func() { <-prefetchSlots }(); _, _ = loadContent(index, index.Entries[position+1], "") }()
	default:
	}
}

var languageCode = regexp.MustCompile(`^[a-z]{2,3}(?:-[a-z]{2,4})?$`)

func validLanguage(language string) bool { return languageCode.MatchString(language) }
