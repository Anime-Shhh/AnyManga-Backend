package scrapers

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jung-kurt/gofpdf"
)

type ChaptersRequest struct {
	Title    string   `json:"title"`
	Chapters []string `json:"chapters"`
}

func CleanURL(raw string) string {
	clean := raw

	// Remove HTML entity symbols
	clean = strings.ReplaceAll(clean, "&", "")
	clean = strings.ReplaceAll(clean, "#", "")
	clean = strings.ReplaceAll(clean, ";", "")

	// Remove unwanted punctuation
	clean = strings.ReplaceAll(clean, ",", "")
	clean = strings.ReplaceAll(clean, "'", "")
	clean = strings.ReplaceAll(clean, `"`, "")
	clean = strings.ReplaceAll(clean, " ", "-")

	// Replace multiple consecutive dashes with single dash
	re := regexp.MustCompile(`-+`)
	clean = re.ReplaceAllString(clean, "-")

	// Lowercase
	clean = strings.ToLower(clean)

	return clean
}

var pdfSlots = make(chan struct{}, 2)

func GetAllChapters(c *gin.Context) {
	select {
	case pdfSlots <- struct{}{}:
		defer func() { <-pdfSlots }()
	default:
		c.Header("Retry-After", "5")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Downloads are busy. Please try again shortly."})
		return
	}
	var req ChaptersRequest

	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	if req.Title == "" || len(req.Chapters) == 0 || len(req.Chapters) > 10 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Select between one and ten chapters."})
		return
	}
	index := indexForRequest(c, req.Title)
	if index == nil {
		return
	}
	results := make([][]string, 0, len(req.Chapters))
	totalPages := 0
	for _, requested := range req.Chapters {
		position, ok := index.lookup(requested)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "A selected chapter was not found."})
			return
		}
		content, err := loadContent(index, index.Entries[position], "")
		pages := content.Images
		if content.ExternalURL != "" {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "This chapter is hosted on the publisher’s website and cannot be downloaded here."})
			return
		}
		if err != nil {
			reportError(c, err, "Could not load a selected chapter.")
			return
		}
		totalPages += len(pages)
		if totalPages > 200 {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "Select fewer chapters; each PDF is limited to 200 pages."})
			return
		}
		results = append(results, pages)
	}

	budget := 64 << 20
	pdf := gofpdf.New("P", "mm", "A4", "")
	for _, pages := range results {
		for _, imgURL := range pages {
			if c.Request.Context().Err() != nil {
				return
			}
			err := addPDFPage(c.Request, pdf, imgURL, &budget)
			if err != nil {
				reportError(c, err, "Could not download a chapter image.")
				return
			}
		}
		if pdf.PageNo() == 0 {
			c.JSON(http.StatusBadGateway, gin.H{"error": "No downloadable chapter pages."})
			return
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
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s.pdf", index.Slug))
	c.Data(http.StatusOK, "application/pdf", buf.Bytes())
}

func addPDFPage(request *http.Request, pdf *gofpdf.Fpdf, address string, budget *int) error {
	if strings.HasPrefix(address, "/api/manga/image?") {
		link, err := url.Parse(address)
		if err != nil {
			return err
		}
		address = link.Query().Get("url")
	}
	data, _, err := fetchImage(request.Context(), address)
	if err != nil {
		return err
	}
	if len(data) > *budget {
		return fmt.Errorf("download exceeds the 64 MiB image budget; select fewer chapters")
	}
	*budget -= len(data)
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width > 10000 || config.Height > 20000 ||
		int64(config.Width)*int64(config.Height) > 40_000_000 {
		return fmt.Errorf("unsupported download image")
	}
	var imageType string
	switch format {
	case "jpeg":
		imageType = "JPG"
	case "png":
		imageType = "PNG"
	default:
		return fmt.Errorf("unsupported PDF image type %q", format)
	}
	width, height := float64(config.Width), float64(config.Height)
	scale := min(210/width, 297/height)
	pdf.AddPage()
	options := gofpdf.ImageOptions{ImageType: imageType, ReadDpi: true}
	pdf.RegisterImageOptionsReader(address, options, bytes.NewReader(data))
	pdf.ImageOptions(address, (210-width*scale)/2, 0, width*scale, height*scale, false, options, 0, "")
	return pdf.Error()
}
