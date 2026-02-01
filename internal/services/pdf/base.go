package pdf

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"strings"
	"time"

	"github.com/phpdave11/gofpdf"
)

// Color represents an RGB color.
type Color struct {
	R int
	G int
	B int
}

// Theme defines Estara PDF styling constants.
type Theme struct {
	Primary   Color
	Secondary Color
	Accent    Color
	Muted     Color
	Text      Color
	Light     Color
	Warning   Color
}

// DefaultTheme matches the www_v1 PDF generator theme.
var DefaultTheme = Theme{
	Primary:   Color{R: 30, G: 58, B: 138},  // #1e3a8a
	Secondary: Color{R: 59, G: 130, B: 246}, // #3b82f6
	Accent:    Color{R: 16, G: 185, B: 129}, // #10b981
	Muted:     Color{R: 100, G: 116, B: 139},
	Text:      Color{R: 30, G: 41, B: 59},
	Light:     Color{R: 248, G: 250, B: 252},
	Warning:   Color{R: 245, G: 158, B: 11},
}

// PageConfig holds page sizing details.
type PageConfig struct {
	Width        float64
	Height       float64
	MarginLeft   float64
	MarginRight  float64
	MarginTop    float64
	MarginBottom float64
}

// LetterPage config matches www_v1 letter layout.
var LetterPage = PageConfig{
	Width:        215.9,
	Height:       279.4,
	MarginLeft:   20,
	MarginRight:  20,
	MarginTop:    25,
	MarginBottom: 20,
}

// A4Page config for investment plan and projections PDFs.
var A4Page = PageConfig{
	Width:        210,
	Height:       297,
	MarginLeft:   18,
	MarginRight:  18,
	MarginTop:    20,
	MarginBottom: 18,
}

// NewPDF creates a new PDF with standard settings.
func NewPDF(orientation, unit, size string) *gofpdf.Fpdf {
	pdf := gofpdf.New(orientation, unit, size, "")
	pdf.SetMargins(0, 0, 0)
	pdf.SetAutoPageBreak(false, 0)
	pdf.SetFont("Helvetica", "", 10)
	return pdf
}

// AddCoverPage draws a branded cover.
func AddCoverPage(pdf *gofpdf.Fpdf, page PageConfig, theme Theme, title, subtitle, market string, generatedFor string) {
	pdf.AddPage()

	pdf.SetFillColor(theme.Primary.R, theme.Primary.G, theme.Primary.B)
	pdf.Rect(0, 0, page.Width, 80, "F")

	pdf.SetTextColor(255, 255, 255)
	pdf.SetFont("Helvetica", "B", 24)
	pdf.Text(page.Width/2-20, 35, "Estara-AI")
	pdf.SetFont("Helvetica", "", 10)
	pdf.Text(page.Width/2-35, 45, "REAL ESTATE INTELLIGENCE")

	pdf.SetTextColor(theme.Text.R, theme.Text.G, theme.Text.B)
	pdf.SetFont("Times", "B", 20)
	pdf.Text(page.Width/2-60, 110, title)

	pdf.SetFont("Helvetica", "", 12)
	pdf.Text(page.Width/2-45, 125, subtitle)

	pdf.SetFont("Helvetica", "B", 16)
	pdf.SetTextColor(theme.Primary.R, theme.Primary.G, theme.Primary.B)
	pdf.Text(page.Width/2-45, 150, market)

	if generatedFor != "" {
		pdf.SetTextColor(theme.Text.R, theme.Text.G, theme.Text.B)
		pdf.SetFont("Helvetica", "", 11)
		pdf.Text(page.Width/2-35, 165, fmt.Sprintf("Generated for %s", generatedFor))
	}

	pdf.SetTextColor(theme.Muted.R, theme.Muted.G, theme.Muted.B)
	pdf.SetFont("Helvetica", "", 10)
	pdf.Text(page.Width/2-30, 180, fmt.Sprintf("Generated: %s", time.Now().Format("January 2, 2006")))

	// Disclaimer banner
	bannerY := 195.0
	pdf.SetFillColor(254, 243, 199)
	pdf.Rect(page.MarginLeft, bannerY, page.Width-page.MarginLeft-page.MarginRight, 22, "F")
	pdf.SetDrawColor(theme.Warning.R, theme.Warning.G, theme.Warning.B)
	pdf.SetLineWidth(0.5)
	pdf.Rect(page.MarginLeft, bannerY, page.Width-page.MarginLeft-page.MarginRight, 22, "D")

	pdf.SetTextColor(120, 53, 15)
	pdf.SetFont("Helvetica", "B", 7)
	pdf.Text(page.MarginLeft+3, bannerY+5, "IMPORTANT NOTICE")
	pdf.SetFont("Helvetica", "", 7)
	disclaimer := "This report is general information only. It is NOT investment advice, NOT a recommendation, and NOT an offer to buy or sell any property. Results shown are illustrative estimates only. All investment decisions remain your responsibility."
	lines := pdf.SplitLines([]byte(disclaimer), page.Width-page.MarginLeft-page.MarginRight-6)
	y := bannerY + 10
	for _, line := range lines {
		pdf.Text(page.MarginLeft+3, y, string(line))
		y += 3.2
	}

	// Footer band
	pdf.SetFillColor(theme.Primary.R, theme.Primary.G, theme.Primary.B)
	pdf.Rect(0, page.Height-30, page.Width, 30, "F")
	pdf.SetTextColor(255, 255, 255)
	pdf.SetFont("Helvetica", "", 9)
	pdf.Text(page.Width/2-20, page.Height-15, "Powered by Estara AI")
	pdf.Text(page.Width/2-20, page.Height-8, "www.estara-ai.com")
}

// AddHeaderFooter configures header/footer after cover page.
func AddHeaderFooter(pdf *gofpdf.Fpdf, page PageConfig, theme Theme, title string) {
	pdf.AliasNbPages("")
	pdf.SetHeaderFunc(func() {
		pdf.SetDrawColor(theme.Primary.R, theme.Primary.G, theme.Primary.B)
		pdf.SetLineWidth(0.8)
		pdf.Line(page.MarginLeft, 15, page.Width-page.MarginRight, 15)

		pdf.SetTextColor(theme.Primary.R, theme.Primary.G, theme.Primary.B)
		pdf.SetFont("Helvetica", "B", 9)
		pdf.Text(page.MarginLeft, 12, "Estara-AI")

		pdf.SetTextColor(theme.Muted.R, theme.Muted.G, theme.Muted.B)
		pdf.SetFont("Helvetica", "", 8)
		pdf.Text(page.Width-page.MarginRight-40, 12, title)
	})

	pdf.SetFooterFunc(func() {
		y := page.Height - 10
		pdf.SetDrawColor(theme.Primary.R, theme.Primary.G, theme.Primary.B)
		pdf.SetLineWidth(0.3)
		pdf.Line(page.MarginLeft, y-5, page.Width-page.MarginRight, y-5)

		pdf.SetTextColor(theme.Muted.R, theme.Muted.G, theme.Muted.B)
		pdf.SetFont("Helvetica", "", 7)
		pdf.Text(page.MarginLeft, y, "Confidential - For Intended Recipient Only")
		pdf.Text(page.Width/2-10, y, fmt.Sprintf("Page %d of {nb}", pdf.PageNo()))
		pdf.Text(page.Width-page.MarginRight-20, y, "© Estara AI")
	})
}

// AddSectionHeading draws a section heading and returns the next y position.
func AddSectionHeading(pdf *gofpdf.Fpdf, page PageConfig, theme Theme, text string, y float64) float64 {
	pdf.SetFillColor(theme.Primary.R, theme.Primary.G, theme.Primary.B)
	pdf.Rect(page.MarginLeft, y-2, 2, 6, "F")

	pdf.SetTextColor(theme.Text.R, theme.Text.G, theme.Text.B)
	pdf.SetFont("Helvetica", "B", 12)
	pdf.Text(page.MarginLeft+5, y+2, text)

	pdf.SetDrawColor(theme.Primary.R, theme.Primary.G, theme.Primary.B)
	pdf.SetLineWidth(0.2)
	pdf.Line(page.MarginLeft, y+6, page.Width-page.MarginRight, y+6)

	return y + 12
}

// AddParagraph writes wrapped body text.
func AddParagraph(pdf *gofpdf.Fpdf, page PageConfig, theme Theme, text string, y float64) float64 {
	pdf.SetTextColor(theme.Text.R, theme.Text.G, theme.Text.B)
	pdf.SetFont("Helvetica", "", 9)
	lines := pdf.SplitLines([]byte(text), page.Width-page.MarginLeft-page.MarginRight)
	for _, line := range lines {
		pdf.Text(page.MarginLeft, y, string(line))
		y += 4.5
	}
	return y + 2
}

// AddBulletList renders bullet points.
func AddBulletList(pdf *gofpdf.Fpdf, page PageConfig, theme Theme, items []string, y float64) float64 {
	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(theme.Text.R, theme.Text.G, theme.Text.B)
	for _, item := range items {
		pdf.SetFillColor(theme.Primary.R, theme.Primary.G, theme.Primary.B)
		pdf.Circle(page.MarginLeft+2, y-1, 1, "F")
		lines := pdf.SplitLines([]byte(item), page.Width-page.MarginLeft-page.MarginRight-8)
		for _, line := range lines {
			pdf.Text(page.MarginLeft+8, y, string(line))
			y += 4.2
		}
		y += 1.5
	}
	return y + 2
}

// AddMetricsGrid renders two-column metric pairs.
func AddMetricsGrid(pdf *gofpdf.Fpdf, page PageConfig, theme Theme, metrics []MetricItem, y float64) float64 {
	colWidth := (page.Width - page.MarginLeft - page.MarginRight) / 2
	x := page.MarginLeft
	col := 0
	for _, metric := range metrics {
		if metric.Highlight {
			pdf.SetFillColor(252, 249, 240)
			pdf.Rect(x, y-4, colWidth-5, 14, "F")
		}

		pdf.SetTextColor(theme.Muted.R, theme.Muted.G, theme.Muted.B)
		pdf.SetFont("Helvetica", "", 8)
		pdf.Text(x+2, y, metric.Label)

		pdf.SetTextColor(theme.Text.R, theme.Text.G, theme.Text.B)
		pdf.SetFont("Helvetica", "B", 9)
		pdf.Text(x+2, y+6, metric.Value)

		col++
		if col%2 == 0 {
			col = 0
			x = page.MarginLeft
			y += 18
		} else {
			x = page.MarginLeft + colWidth
		}
	}
	if col != 0 {
		y += 18
	}
	return y + 2
}

// MetricItem represents a single metric.
type MetricItem struct {
	Label     string
	Value     string
	Highlight bool
}

// TableColumn defines a table column.
type TableColumn struct {
	Header string
	Width  float64
}

// AddTable renders a basic table with optional header.
func AddTable(pdf *gofpdf.Fpdf, page PageConfig, theme Theme, columns []TableColumn, rows [][]string, y float64) float64 {
	rowHeight := 6.0
	// Header
	pdf.SetFillColor(theme.Primary.R, theme.Primary.G, theme.Primary.B)
	pdf.SetTextColor(255, 255, 255)
	pdf.SetFont("Helvetica", "B", 8)
	x := page.MarginLeft
	for _, col := range columns {
		pdf.Rect(x, y, col.Width, rowHeight, "F")
		pdf.Text(x+2, y+4.2, col.Header)
		x += col.Width
	}
	y += rowHeight

	pdf.SetFont("Helvetica", "", 8)
	for _, row := range rows {
		if y+rowHeight > page.Height-page.MarginBottom {
			pdf.AddPage()
			y = page.MarginTop
		}
		x = page.MarginLeft
		pdf.SetTextColor(theme.Text.R, theme.Text.G, theme.Text.B)
		for i, col := range columns {
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			pdf.Rect(x, y, col.Width, rowHeight, "D")
			pdf.Text(x+2, y+4.2, cell)
			x += col.Width
		}
		y += rowHeight
	}
	return y + 4
}

// AddImageFromBase64 embeds a base64 image (data URI or raw).
func AddImageFromBase64(pdf *gofpdf.Fpdf, name string, data string, x, y, w, h float64) error {
	payload := data
	if strings.HasPrefix(data, "data:") {
		parts := strings.SplitN(data, ",", 2)
		if len(parts) == 2 {
			payload = parts[1]
		}
	}

	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return err
	}

	reader := bytes.NewReader(decoded)
	info := pdf.RegisterImageOptionsReader(name, gofpdf.ImageOptions{ImageType: "PNG", ReadDpi: true}, reader)
	if info == nil {
		return fmt.Errorf("failed to register image")
	}
	pdf.ImageOptions(name, x, y, w, h, false, gofpdf.ImageOptions{ImageType: "PNG"}, 0, "")
	return nil
}

// AddImageReader embeds an image from reader.
func AddImageReader(pdf *gofpdf.Fpdf, name string, reader io.Reader, x, y, w, h float64) error {
	_, _, err := image.DecodeConfig(reader)
	if err != nil {
		return err
	}
	seekable, ok := reader.(io.ReadSeeker)
	if !ok {
		return fmt.Errorf("image reader must be seekable")
	}
	if _, err := seekable.Seek(0, io.SeekStart); err != nil {
		return err
	}
	info := pdf.RegisterImageOptionsReader(name, gofpdf.ImageOptions{ImageType: "PNG", ReadDpi: true}, seekable)
	if info == nil {
		return fmt.Errorf("failed to register image")
	}
	pdf.ImageOptions(name, x, y, w, h, false, gofpdf.ImageOptions{ImageType: "PNG"}, 0, "")
	return nil
}
