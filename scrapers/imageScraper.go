package scrapers

import (
	"bytes"
	"fmt"
	"image"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/jung-kurt/gofpdf"
)

type ChaptersRequest struct {
	Title    string   `json:"title"`
	Chapters []string `json:"chapters"`
}

func GetAllChapters(c *gin.Context) {
	var req ChaptersRequest

	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	if req.Title == "" || len(req.Chapters) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing title or chapters"})
		return
	}

	title := req.Title
	title = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(title)), " ", "-")

	chapters := req.Chapters
	results := make([]chapter, len(chapters))

	var wg sync.WaitGroup

	for i, ch := range chapters {
		wg.Add(1)

		re := regexp.MustCompile(`[\s-]+`)
		ch = strings.ToLower(strings.TrimSpace(ch))
		ch = re.ReplaceAllString(ch, "-")
		// get index where to store the chapter data
		url := fmt.Sprintf("https://www.mangaread.org/manga/%s/%s/", title, ch)
		fmt.Println("This is the site:", url)

		go func(visitUrl string) {
			defer wg.Done()

			chap, _ := scrapeChapter(visitUrl)
			results[i] = chap
		}(url)
	}

	wg.Wait()

	pdf := gofpdf.New("P", "mm", "A4", "")
	for _, chap := range results {
		for _, imgURL := range chap.Pages {
			imgURL = strings.TrimSpace(imgURL)
			resp, err := http.Get(imgURL)
			if err != nil {
				fmt.Println("Error downloading image:", err)
				continue
			}
			defer resp.Body.Close()

			buf := new(bytes.Buffer)
			_, err = buf.ReadFrom(resp.Body)
			if err != nil {
				fmt.Println("Error reading image:", err)
				continue
			}

			// Detect image type
			img, imgType, err := image.Decode(bytes.NewReader(buf.Bytes()))
			if err != nil {
				fmt.Println("Decode error:", err)
				continue
			}

			// Convert image type to gofpdf string
			var pdfImgType string
			switch imgType {
			case "jpeg":
				pdfImgType = "JPG"
			case "png":
				pdfImgType = "PNG"
			default:
				fmt.Println("Unsupported image type:", imgType)
				continue
			}

			bounds := img.Bounds()
			width := float64(bounds.Dx()) * 0.264583
			height := float64(bounds.Dy()) * 0.264583

			pdf.AddPage()
			opt := gofpdf.ImageOptions{ImageType: pdfImgType, ReadDpi: true}
			pdf.RegisterImageOptionsReader(imgURL, opt, bytes.NewReader(buf.Bytes()))
			pdf.ImageOptions(imgURL, 0, 0, width, height, false, opt, 0, "")
		}
	}

	// Output PDF to bytes
	var buf bytes.Buffer
	err := pdf.Output(&buf)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create PDF"})
		return
	}

	// Send PDF to frontend
	c.Header("Content-Type", "application/pdf")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s.pdf", req.Title))
	c.Data(http.StatusOK, "application/pdf", buf.Bytes())
}
