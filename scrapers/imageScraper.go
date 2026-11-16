package scrapers

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
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

func GetAllChapters(c *gin.Context) {
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
	for _, requested := range req.Chapters {
		position, ok := index.lookup(requested)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "A selected chapter was not found."})
			return
		}
		pages, err := loadPages(index.Entries[position])
		if err != nil {
			reportError(c, err, "Could not load a selected chapter.")
			return
		}
		results = append(results, pages)
	}

	pdf := gofpdf.New("P", "mm", "A4", "")
	for _, pages := range results {
		for _, imgURL := range pages {
			if c.Request.Context().Err() != nil {
				return
			}
			err := addPDFPage(c.Request, pdf, imgURL)
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

func addPDFPage(request *http.Request, pdf *gofpdf.Fpdf, address string) error {
	link, err := url.Parse(address)
	if err != nil || link.Scheme != "https" ||
		(link.Host != "www.mangaread.org" && link.Host != "mangaread.org") ||
		!strings.HasPrefix(link.Path, "/wp-content/uploads/") {
		return fmt.Errorf("unrecognized MangaRead download image")
	}
	select {
	case upstreamSlots <- struct{}{}:
		defer func() { <-upstreamSlots }()
	default:
		return fmt.Errorf("MangaRead request capacity reached")
	}
	req, err := http.NewRequestWithContext(request.Context(), http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	resp, err := upstream.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download image returned HTTP %d", resp.StatusCode)
	}
	const maxImageBytes = 12 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil || len(data) > maxImageBytes {
		return fmt.Errorf("could not read the download image within the size limit")
	}
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
