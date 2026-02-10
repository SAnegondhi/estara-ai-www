package pdf

import (
	"strings"

	"github.com/phpdave11/gofpdf"
)

// RenderMarkdown converts a markdown string to PDF content.
// Handles: ## headings, ### subheadings, | tables |, - bullet lists, paragraphs, --- rules.
// Inline ** markers are stripped (gofpdf does not support mid-line font switching easily).
func RenderMarkdown(pdf *gofpdf.Fpdf, page PageConfig, theme Theme, markdown string, y float64) float64 {
	lines := strings.Split(markdown, "\n")

	var (
		paraLines   []string
		tableHeader []string
		tableRows   [][]string
		listItems   []string
		inTable     bool
		inList      bool
		headerSeen  bool
	)

	flush := func() {
		if len(paraLines) > 0 {
			text := cleanInlineMarkdown(strings.Join(paraLines, " "))
			y = mdParagraph(pdf, page, theme, text, y)
			paraLines = nil
		}
		if inTable && (len(tableRows) > 0 || len(tableHeader) > 0) {
			y = mdTable(pdf, page, theme, tableHeader, tableRows, y)
			tableHeader = nil
			tableRows = nil
			headerSeen = false
			inTable = false
		}
		if inList && len(listItems) > 0 {
			y = mdBulletList(pdf, page, theme, listItems, y)
			listItems = nil
			inList = false
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Empty line — flush accumulated content
		if trimmed == "" {
			flush()
			continue
		}

		// # Top-level title (must check before ##)
		if strings.HasPrefix(trimmed, "# ") && !strings.HasPrefix(trimmed, "## ") {
			flush()
			heading := cleanInlineMarkdown(strings.TrimPrefix(trimmed, "# "))
			y = mdTitle(pdf, page, theme, heading, y)
			continue
		}

		// ## Section heading
		if strings.HasPrefix(trimmed, "## ") {
			flush()
			heading := cleanInlineMarkdown(strings.TrimPrefix(trimmed, "## "))
			y = AddSectionHeading(pdf, page, theme, heading, y)
			continue
		}

		// ### Subsection heading
		if strings.HasPrefix(trimmed, "### ") {
			flush()
			heading := cleanInlineMarkdown(strings.TrimPrefix(trimmed, "### "))
			y = mdSubHeading(pdf, page, theme, heading, y)
			continue
		}

		// Horizontal rule
		if trimmed == "---" || trimmed == "***" || trimmed == "___" {
			flush()
			y = mdHorizontalRule(pdf, page, theme, y)
			continue
		}

		// Table row (starts and ends with |)
		if strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") {
			// Entering table — flush any preceding paragraph
			if !inTable && len(paraLines) > 0 {
				flush()
			}

			// Separator row (|---|---|)
			if isTableSeparator(trimmed) {
				headerSeen = true
				inTable = true
				continue
			}

			cells := parseTableRow(trimmed)

			if !inTable {
				tableHeader = cells
				inTable = true
			} else if headerSeen {
				tableRows = append(tableRows, cells)
			} else {
				// Haven't seen separator yet — treat as header replacement
				tableHeader = cells
			}
			continue
		}

		// If we were in a table and hit a non-table line, flush
		if inTable {
			flush()
		}

		// Bullet list item (- or *)
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			if !inList && len(paraLines) > 0 {
				flush()
			}
			inList = true
			item := cleanInlineMarkdown(trimmed[2:])
			listItems = append(listItems, item)
			continue
		}

		// If we were in a list and hit a non-list line, flush
		if inList {
			flush()
		}

		// Regular text — accumulate as paragraph
		paraLines = append(paraLines, trimmed)
	}

	flush()
	return y
}

// mdParagraph renders wrapped text with page-break support.
func mdParagraph(pdf *gofpdf.Fpdf, page PageConfig, theme Theme, text string, y float64) float64 {
	pdf.SetTextColor(theme.Text.R, theme.Text.G, theme.Text.B)
	pdf.SetFont("Helvetica", "", 9)
	contentWidth := page.Width - page.MarginLeft - page.MarginRight
	lines := pdf.SplitLines([]byte(text), contentWidth)
	for _, line := range lines {
		if y+4.5 > page.Height-page.MarginBottom {
			pdf.AddPage()
			y = page.MarginTop
		}
		pdf.Text(page.MarginLeft, y, string(line))
		y += 4.5
	}
	return y + 2
}

// mdTitle renders a # top-level title (larger than ##, with primary color).
func mdTitle(pdf *gofpdf.Fpdf, page PageConfig, theme Theme, text string, y float64) float64 {
	if y > page.MarginTop+5 {
		y += 6
	}
	if y+20 > page.Height-page.MarginBottom {
		pdf.AddPage()
		y = page.MarginTop
	}

	pdf.SetTextColor(theme.Primary.R, theme.Primary.G, theme.Primary.B)
	pdf.SetFont("Helvetica", "B", 14)
	pdf.Text(page.MarginLeft, y+4, text)

	// Underline
	pdf.SetDrawColor(theme.Primary.R, theme.Primary.G, theme.Primary.B)
	pdf.SetLineWidth(0.4)
	pdf.Line(page.MarginLeft, y+8, page.Width-page.MarginRight, y+8)

	return y + 16
}

// mdSubHeading renders a ### level heading (smaller than ##).
func mdSubHeading(pdf *gofpdf.Fpdf, page PageConfig, theme Theme, text string, y float64) float64 {
	if y > page.MarginTop+5 {
		y += 4
	}
	if y+15 > page.Height-page.MarginBottom {
		pdf.AddPage()
		y = page.MarginTop
	}

	pdf.SetTextColor(theme.Text.R, theme.Text.G, theme.Text.B)
	pdf.SetFont("Helvetica", "B", 10)
	pdf.Text(page.MarginLeft, y+2, text)

	return y + 8
}

// mdHorizontalRule draws a thin line.
func mdHorizontalRule(pdf *gofpdf.Fpdf, page PageConfig, theme Theme, y float64) float64 {
	y += 3
	if y+5 > page.Height-page.MarginBottom {
		pdf.AddPage()
		y = page.MarginTop
	}
	pdf.SetDrawColor(theme.Muted.R, theme.Muted.G, theme.Muted.B)
	pdf.SetLineWidth(0.2)
	pdf.Line(page.MarginLeft, y, page.Width-page.MarginRight, y)
	return y + 5
}

// mdBulletList renders bullet items with page-break support.
func mdBulletList(pdf *gofpdf.Fpdf, page PageConfig, theme Theme, items []string, y float64) float64 {
	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(theme.Text.R, theme.Text.G, theme.Text.B)
	contentWidth := page.Width - page.MarginLeft - page.MarginRight - 8

	for _, item := range items {
		wrappedLines := pdf.SplitLines([]byte(item), contentWidth)
		neededHeight := float64(len(wrappedLines))*4.2 + 1.5

		if y+neededHeight > page.Height-page.MarginBottom {
			pdf.AddPage()
			y = page.MarginTop
		}

		pdf.SetFillColor(theme.Primary.R, theme.Primary.G, theme.Primary.B)
		pdf.Circle(page.MarginLeft+2, y-1, 1, "F")
		for _, line := range wrappedLines {
			pdf.Text(page.MarginLeft+8, y, string(line))
			y += 4.2
		}
		y += 1.5
	}
	return y + 2
}

// mdTable renders a markdown table using AddTable from base.go.
func mdTable(pdf *gofpdf.Fpdf, page PageConfig, theme Theme, header []string, rows [][]string, y float64) float64 {
	if len(header) == 0 {
		return y
	}

	contentWidth := page.Width - page.MarginLeft - page.MarginRight
	colCount := len(header)

	// Distribute column widths proportionally based on content length
	columns := distributeColumns(header, rows, contentWidth, colCount)

	// Clean inline markdown from all cells
	for i := range header {
		header[i] = cleanInlineMarkdown(header[i])
	}
	cleanedRows := make([][]string, len(rows))
	for i, row := range rows {
		cleanedRows[i] = make([]string, len(row))
		for j, cell := range row {
			cleanedRows[i][j] = cleanInlineMarkdown(cell)
		}
	}

	// Set column headers from cleaned header
	for i := range columns {
		if i < len(header) {
			columns[i].Header = header[i]
		}
	}

	return AddTable(pdf, page, theme, columns, cleanedRows, y)
}

// distributeColumns calculates proportional column widths based on content.
func distributeColumns(header []string, rows [][]string, totalWidth float64, colCount int) []TableColumn {
	if colCount == 0 {
		return nil
	}

	// Find max content length per column
	maxLen := make([]int, colCount)
	for i, h := range header {
		if len(h) > maxLen[i] {
			maxLen[i] = len(h)
		}
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < colCount && len(cell) > maxLen[i] {
				maxLen[i] = len(cell)
			}
		}
	}

	// Compute total character width, with a minimum per column
	totalChars := 0
	for i := range maxLen {
		if maxLen[i] < 5 {
			maxLen[i] = 5
		}
		totalChars += maxLen[i]
	}

	columns := make([]TableColumn, colCount)
	for i := range colCount {
		proportion := float64(maxLen[i]) / float64(totalChars)
		columns[i] = TableColumn{
			Width: proportion * totalWidth,
		}
	}

	return columns
}

// parseTableRow splits a markdown table row into cells.
func parseTableRow(line string) []string {
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	cells := make([]string, len(parts))
	for i, p := range parts {
		cells[i] = strings.TrimSpace(p)
	}
	return cells
}

// isTableSeparator returns true for separator rows like |---|---|---|.
func isTableSeparator(line string) bool {
	cleaned := strings.ReplaceAll(line, "|", "")
	cleaned = strings.ReplaceAll(cleaned, "-", "")
	cleaned = strings.ReplaceAll(cleaned, ":", "")
	cleaned = strings.TrimSpace(cleaned)
	return cleaned == ""
}

// cleanInlineMarkdown strips ** markers and transliterates Unicode characters
// that gofpdf cannot render (it uses ISO-8859-1 for built-in fonts).
func cleanInlineMarkdown(text string) string {
	text = strings.ReplaceAll(text, "**", "")
	text = strings.ReplaceAll(text, "`", "")
	text = sanitizeForPDF(text) // defined in investment_plan.go — strips non-Latin-1
	return strings.TrimSpace(text)
}
