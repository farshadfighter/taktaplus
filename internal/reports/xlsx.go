package reports

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// sheet is one worksheet: a header row and data rows. Cells hold strings or
// numbers (float64).
type sheet struct {
	Name   string
	Header []string
	Rows   [][]any
}

// writeXLSX writes a minimal Office Open XML workbook with right-to-left
// sheets and a bold header row.
func writeXLSX(w io.Writer, sheets []sheet) error {
	zw := zip.NewWriter(w)
	add := func(name, content string) error {
		f, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = io.WriteString(f, content)
		return err
	}
	var ct, wb, rels strings.Builder
	ct.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/>` +
		`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
		`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>`)
	wb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>`)
	rels.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	used := map[string]bool{}
	for i, sh := range sheets {
		n := i + 1
		name := sheetName(sh.Name, used)
		fmt.Fprintf(&ct, `<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, n)
		fmt.Fprintf(&wb, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, esc(name), n, n)
		fmt.Fprintf(&rels, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, n, n)
		if err := add(fmt.Sprintf("xl/worksheets/sheet%d.xml", n), sheetXML(sh)); err != nil {
			return err
		}
	}
	ct.WriteString(`</Types>`)
	wb.WriteString(`</sheets></workbook>`)
	fmt.Fprintf(&rels, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`, len(sheets)+1)
	parts := []struct{ name, content string }{
		{"[Content_Types].xml", ct.String()},
		{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`},
		{"xl/workbook.xml", wb.String()},
		{"xl/_rels/workbook.xml.rels", rels.String()},
		{"xl/styles.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
			`<fonts count="2"><font><sz val="11"/><name val="Tahoma"/></font><font><b/><sz val="11"/><name val="Tahoma"/></font></fonts>` +
			`<fills count="2"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill></fills>` +
			`<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>` +
			`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
			`<cellXfs count="3"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/><xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1"/>` +
			`<xf numFmtId="3" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/></cellXfs></styleSheet>`},
	}
	for _, p := range parts {
		if err := add(p.name, p.content); err != nil {
			return err
		}
	}
	return zw.Close()
}

func sheetXML(sh sheet) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
		`<sheetViews><sheetView rightToLeft="1" workbookViewId="0"/></sheetViews><sheetFormatPr defaultRowHeight="15"/>`)
	if len(sh.Header) > 0 {
		b.WriteString(`<cols>`)
		for i := range sh.Header {
			fmt.Fprintf(&b, `<col min="%d" max="%d" width="22" customWidth="1"/>`, i+1, i+1)
		}
		b.WriteString(`</cols>`)
	}
	b.WriteString(`<sheetData>`)
	row := 1
	writeRow := func(cells []any, style int) {
		fmt.Fprintf(&b, `<row r="%d">`, row)
		for i, v := range cells {
			ref := colName(i) + strconv.Itoa(row)
			switch x := v.(type) {
			case float64:
				st := 2
				if style != 0 {
					st = style
				}
				fmt.Fprintf(&b, `<c r="%s" s="%d"><v>%s</v></c>`, ref, st, strconv.FormatFloat(x, 'f', -1, 64))
			default:
				fmt.Fprintf(&b, `<c r="%s" s="%d" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, ref, style, esc(fmt.Sprint(v)))
			}
		}
		b.WriteString(`</row>`)
		row++
	}
	if len(sh.Header) > 0 {
		h := make([]any, len(sh.Header))
		for i, v := range sh.Header {
			h[i] = v
		}
		writeRow(h, 1)
	}
	for _, r := range sh.Rows {
		writeRow(r, 0)
	}
	b.WriteString(`</sheetData></worksheet>`)
	return b.String()
}

func colName(i int) string {
	name := ""
	for i++; i > 0; i = (i - 1) / 26 {
		name = string(rune('A'+(i-1)%26)) + name
	}
	return name
}

// sheetName makes a valid, unique sheet name (at most 31 characters, none
// of []:*?/\).
func sheetName(s string, used map[string]bool) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`[]:*?/\`, r) {
			return '-'
		}
		return r
	}, s)
	r := []rune(strings.TrimSpace(s))
	if len(r) == 0 {
		r = []rune("Sheet")
	}
	if len(r) > 28 {
		r = r[:28]
	}
	name := string(r)
	for i := 2; used[strings.ToLower(name)]; i++ {
		name = fmt.Sprintf("%s %d", string(r), i)
	}
	used[strings.ToLower(name)] = true
	return name
}

func esc(s string) string {
	// Drop characters XML 1.0 cannot carry.
	s = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' || r >= 0x20 && r != 0xFFFE && r != 0xFFFF {
			return r
		}
		return -1
	}, s)
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
