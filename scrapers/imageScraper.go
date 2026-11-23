package scrapers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
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
	ctx, cancel := context.WithTimeout(c.Request.Context(), 290*time.Second)
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	if err := http.NewResponseController(c.Writer).SetWriteDeadline(time.Now().Add(295 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		c.JSON(500, gin.H{"error": "Could not start the download."})
		return
	}
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

	if totalPages == 0 {
		c.JSON(http.StatusBadGateway, gin.H{"error": "No downloadable chapter pages."})
		return
	}
	c.Header("Content-Type", "application/pdf")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.pdf"`, index.Slug))
	c.Header("Cache-Control", "no-store, no-transform")
	c.Header("X-Accel-Buffering", "no")
	c.Header("X-AnyManga-Pages", fmt.Sprint(totalPages))
	pdf := newStreamingPDF(c.Writer)
	c.Writer.Flush()
	budget := 64 << 20
	for _, pages := range results {
		for _, address := range pages {
			if c.Request.Context().Err() != nil {
				return
			}
			data, err := downloadPDFImage(c.Request, address, &budget)
			if err == nil {
				err = pdf.addPage(data)
			}
			if err != nil {
				// Headers may already be on the wire. Never append JSON to a PDF or
				// finalize a partial document; the client rejects missing EOF.
				_ = c.Error(err)
				return
			}
			c.Writer.Flush()
		}
	}
	if err := pdf.finish(); err != nil {
		_ = c.Error(err)
		return
	}
	c.Writer.Flush()
}

func downloadPDFImage(request *http.Request, address string, budget *int) ([]byte, error) {
	if strings.HasPrefix(address, "/api/manga/image?") {
		link, err := url.Parse(address)
		if err != nil {
			return nil, err
		}
		address = link.Query().Get("url")
	}
	data, _, err := fetchImage(request.Context(), address)
	if err != nil {
		return nil, err
	}
	if len(data) > *budget {
		return nil, fmt.Errorf("download exceeds the 64 MiB image budget; select fewer chapters")
	}
	*budget -= len(data)
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > 10000 || config.Height > 20000 || int64(config.Width)*int64(config.Height) > 40_000_000 {
		return nil, fmt.Errorf("unsupported download image")
	}
	if format != "jpeg" && format != "png" {
		return nil, fmt.Errorf("unsupported PDF image type %q", format)
	}
	return data, nil
}
