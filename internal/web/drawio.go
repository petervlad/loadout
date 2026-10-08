package web

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strconv"
)

// maxPageTabs is how many page names of a draw.io file become tabs; the
// viewer's own page arrows still reach the rest.
const maxPageTabs = 100

// drawioPages reads the page names of a draw.io file: the name of each
// <diagram>, "Page N" for one without, at most maxPageTabs of them, and
// how many pages there are. It only reads attributes, so a compressed
// page is named like a plain one and never inflated. encoding/xml expands
// no entity beyond the built-in five and fetches nothing, so a DOCTYPE
// cannot make it read anything else. ok is false when the XML cannot be
// read; the viewer may still draw what it can.
func drawioPages(data []byte) (names []string, total int, ok bool) {
	d := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, 0, false
		}
		el, isStart := tok.(xml.StartElement)
		if !isStart || el.Name.Local != "diagram" {
			continue
		}
		total++
		if len(names) == maxPageTabs {
			continue
		}
		name := "Page " + strconv.Itoa(total)
		for _, a := range el.Attr {
			if a.Name.Local == "name" && a.Value != "" {
				name = a.Value
			}
		}
		names = append(names, name)
	}
	if total == 0 {
		// A bare mxGraphModel is one page without a name.
		return []string{"Page 1"}, 1, true
	}
	return names, total, true
}
