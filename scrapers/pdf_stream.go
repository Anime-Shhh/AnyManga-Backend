package scrapers

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"image"
	"image/color"
	"io"
)

// PDF objects can be emitted before their referenced objects. Keep only byte
// offsets and page IDs, then write the page tree and cross-reference table last.
// Unlike gofpdf.Output, this does not retain the entire document in RAM.
type streamingPDF struct {
	w       io.Writer
	offset  int64
	offsets []int64
	pages   []int
	err     error
}

const maxPDFBytes = 128 << 20

func newStreamingPDF(w io.Writer) *streamingPDF {
	p := &streamingPDF{w: w, offsets: []int64{0, 0, 0}}
	p.write([]byte("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n"))
	p.object(1, []byte("<< /Type /Catalog /Pages 2 0 R >>"))
	return p
}

func (p *streamingPDF) write(data []byte) {
	if p.err != nil {
		return
	}
	if p.offset+int64(len(data)) > maxPDFBytes {
		p.err = fmt.Errorf("PDF exceeds output size limit")
		return
	}
	n, err := p.w.Write(data)
	p.offset += int64(n)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	p.err = err
}
func (p *streamingPDF) text(format string, args ...any) { p.write(fmt.Appendf(nil, format, args...)) }
func (p *streamingPDF) begin(id int) {
	for len(p.offsets) <= id {
		p.offsets = append(p.offsets, 0)
	}
	p.offsets[id] = p.offset
	p.text("%d 0 obj\n", id)
}
func (p *streamingPDF) object(id int, data []byte) { p.begin(id); p.write(data); p.text("\nendobj\n") }
func (p *streamingPDF) stream(id int, dictionary string, data []byte) {
	p.begin(id)
	p.text("<< %s /Length %d >>\nstream\n", dictionary, len(data))
	p.write(data)
	p.text("\nendstream\nendobj\n")
}

func (p *streamingPDF) addPage(data []byte) error {
	if p.err != nil {
		return p.err
	}
	if len(p.pages) >= 200 {
		return fmt.Errorf("PDF page limit exceeded")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > 10000 || config.Height > 20000 || int64(config.Width)*int64(config.Height) > 40_000_000 {
		return fmt.Errorf("invalid PDF image dimensions")
	}
	colorSpace, filter, decode := "/DeviceRGB", "/DCTDecode", ""
	switch format {
	case "jpeg":
		if config.ColorModel == color.GrayModel {
			colorSpace = "/DeviceGray"
		}
		if config.ColorModel == color.CMYKModel {
			colorSpace = "/DeviceCMYK"
			decode = " /Decode [1 0 1 0 1 0 1 0]"
		}
	case "png":
		// Losslessly encode RGB rows, flattening transparency onto the white page.
		// Only this image is buffered; no accumulated PDF pages are retained.
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return err
		}
		var compressed bytes.Buffer
		encoder := zlib.NewWriter(&compressed)
		row := make([]byte, config.Width*3)
		bounds := img.Bounds()
		for y := 0; y < config.Height; y++ {
			for x := 0; x < config.Width; x++ {
				r, g, b, a := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
				row[x*3], row[x*3+1], row[x*3+2] = uint8((r+0xffff-a)>>8), uint8((g+0xffff-a)>>8), uint8((b+0xffff-a)>>8)
			}
			if _, err := encoder.Write(row); err != nil {
				return err
			}
		}
		if err := encoder.Close(); err != nil {
			return err
		}
		data, filter = compressed.Bytes(), "/FlateDecode"
	default:
		return fmt.Errorf("unsupported PDF image type %q", format)
	}
	imageID := 3 + len(p.pages)*3
	contentID, pageID := imageID+1, imageID+2
	p.stream(imageID, fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace %s /BitsPerComponent 8 /Filter %s%s", config.Width, config.Height, colorSpace, filter, decode), data)
	const width, height = 595.2756, 841.8898 // A4 in PDF points.
	scale := min(width/float64(config.Width), height/float64(config.Height))
	w, h := float64(config.Width)*scale, float64(config.Height)*scale
	content := fmt.Appendf(nil, "q %.4f 0 0 %.4f %.4f %.4f cm /Im0 Do Q\n", w, h, (width-w)/2, height-h)
	p.stream(contentID, "", content)
	p.object(pageID, fmt.Appendf(nil, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.4f %.4f] /Resources << /XObject << /Im0 %d 0 R >> >> /Contents %d 0 R >>", width, height, imageID, contentID))
	p.pages = append(p.pages, pageID)
	return p.err
}

func (p *streamingPDF) finish() error {
	if p.err != nil {
		return p.err
	}
	if len(p.pages) == 0 {
		return fmt.Errorf("PDF has no pages")
	}
	var tree bytes.Buffer
	fmt.Fprintf(&tree, "<< /Type /Pages /Count %d /Kids [", len(p.pages))
	for _, id := range p.pages {
		fmt.Fprintf(&tree, "%d 0 R ", id)
	}
	tree.WriteString("] >>")
	p.object(2, tree.Bytes())
	xref := p.offset
	p.text("xref\n0 %d\n0000000000 65535 f \n", len(p.offsets))
	for _, offset := range p.offsets[1:] {
		p.text("%010d 00000 n \n", offset)
	}
	p.text("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(p.offsets), xref)
	return p.err
}
