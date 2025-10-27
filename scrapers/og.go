package scrapers

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/gocolly/colly"
)

type chapter struct {
	Chapter string   `json:"chapter"`
	Pages   []string `json:"pages"`
}

func scrapeChapter(url string) (chapter, error) {
	var result chapter

	// will only go to websites that are w these domains, so no wrong scraping on rediredted sites
	c := colly.NewCollector(
		colly.AllowedDomains("mangaread.org", "www.mangaread.org"),
		colly.Async(true),
	)

	// Set error handler
	c.OnError(func(r *colly.Response, err error) {
		fmt.Println("Request URL:", r.Request.URL, "failed with response:", r, "\nError:", err)
	})

	// on every occurence of the img element do the following
	c.OnHTML("img.wp-manga-chapter-img", func(e *colly.HTMLElement) {
		link := e.Attr("src")
		result.Pages = append(result.Pages, link)
		fmt.Println("Found:", link)
	})

	// runs every time before a site is visited
	c.OnRequest(func(r *colly.Request) {
		fmt.Println("visiting:", r.URL.String())
		site := strings.Split(r.URL.String(), "/")
		mangaAndChapter := site[len(site)-3] + "_" + site[len(site)-2]
		result.Chapter = mangaAndChapter

		fmt.Println(site)
		fmt.Println(mangaAndChapter)
	})

	// actually visit the site
	err := c.Visit(url)
	if err != nil {
		return result, err
	}
	c.Wait() // wait for this one to be done, async

	return result, nil
}

func main() {
	reader := bufio.NewReader(os.Stdin)
	// take usr input for manga name
	fmt.Println("Enter the name of the manga(check spelling:")
	mangaName, _ := reader.ReadString('\n')
	mangaName = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(mangaName)), " ", "-")
	fmt.Println("name is: ", mangaName)

	// take user input on if they want one or more chapters
	var more string
	var start, end int
	for {
		fmt.Println("Do you want to download more than one chapter: Enter y/n")
		fmt.Scanln(&more)

		more = strings.ToLower(strings.TrimSpace(more))
		if more == "y" || more == "n" {
			break
		}
		fmt.Println("input a valid input y (yes) or n (no)")
	}

	if more == "y" {
		// get end chapter
		for {
			fmt.Println("Enter start and end chapter number (space separated): ")
			_, err := fmt.Scanln(&start, &end)
			if err != nil {
				fmt.Println("Invalid input. Input 2 positive numbers, space separated")
				// clear input buffer
				for {
					var discard string
					_, err := fmt.Scanln(&discard)
					if err != nil {
						break // stop once buffer is empty
					}
				}
				continue // loops again instead of going below if condition
			}
			if end < 0 || start < 0 {
				fmt.Println("no negative numbers")
				continue
			}
			break
		}
	} else {
		for {
			fmt.Println("Enter chapter number: ")
			_, err := fmt.Scanln(&start)
			if err != nil {
				fmt.Println("Invalid input. Input a number")
				// clear input buffer
				var discard string
				fmt.Scanln(&discard)
				continue // loops again instead of going below if condition
			}
			if start < 0 {
				fmt.Println("Invalid input. Input a number")
				continue
			}
			break
		}
		end = start
		fmt.Println("Your chapter is:", start, end)
	}

	lenChaps := end - start + 1
	results := make([]chapter, lenChaps)
	errs := make([]error, lenChaps)

	var wg sync.WaitGroup

	for ch := start; ch <= end; ch++ {
		wg.Add(1)
		// get index where to store the chapter data
		idx := ch - start
		url := fmt.Sprintf("https://www.mangaread.org/manga/%s/chapter-%d/", mangaName, ch)
		fmt.Println("This is the site:", url)

		go func(index int, visitUrl string) {
			defer wg.Done()

			chap, err := scrapeChapter(visitUrl)
			if err != nil {
				errs[index] = err
				return
			}
			results[index] = chap
		}(idx, url)
	}

	wg.Wait()

	// handle errors
	for i, e := range errs {
		if e != nil {
			fmt.Printf("chapter %d failed: %v\n", start+i, e)
		}
	}

	for _, chap := range results {
		fmt.Println("Chapter: ", chap.Chapter, "\t pages:", len(chap.Pages))
	}
}
