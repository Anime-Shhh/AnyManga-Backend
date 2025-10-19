package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/gocolly/colly"
)

type chapter struct {
	Chapter string   `json:"chapter"`
	Pages   []string `json:"pages"`
}

func main() {
	i := chapter{}

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
		// get start chapter
		for {
			fmt.Println("Enter start chapter number: ")
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
		// get end chapter
		for {
			fmt.Println("Enter end chapter number: ")
			_, err := fmt.Scanln(&end)
			if err != nil {
				fmt.Println("Invalid input. Input a number")
				// clear input buffer
				var discard string
				fmt.Scanln(&discard)
				continue // loops again instead of going below if condition
			}
			if end < 0 {
				fmt.Println("Invalid input. Input a number")
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

	// will only go to websites that are w these domains, so no wrong scraping on rediredted sites
	c := colly.NewCollector(
		colly.AllowedDomains("mangaread.org", "www.mangaread.org"),
	)

	// on every occurence of the img element do the following
	c.OnHTML("img", func(e *colly.HTMLElement) {
		link := strings.TrimSpace(e.Attr("src"))

		re, err := regexp.Compile(`(?i)wp-content/uploads/WP-manga/data/manga_[^/]+/[^/]+/\d+\.(jpg|jpeg|png|webp)$`)
		if err != nil {
			fmt.Println("error compiling regex:", err)
		}
		if re.MatchString(link) {
			i.Pages = append(i.Pages, link)
			// print the link
			fmt.Println("Valid: ", link)
		}
	})

	// runs every time a site is visited
	c.OnRequest(func(r *colly.Request) {
		fmt.Println("visiting:", r.URL.String())
		site := strings.Split(r.URL.String(), "/")
		mangaAndChapter := site[len(site)-3] + "_" + site[len(site)-2]
		i.Chapter = mangaAndChapter

		fmt.Println(site)
		fmt.Println(mangaAndChapter)
	})

	c.Visit("https://www.mangaread.org/manga/one-piece/chapter-1152/")
}
