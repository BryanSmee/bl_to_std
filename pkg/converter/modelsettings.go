package converter

import (
	"encoding/xml"
	"io"
	"strconv"
	"strings"
)

// rewriteModelSettings remaps the per-object and per-part extruder
// assignments in Metadata/model_settings.config through the filament
// mapping, preserving everything else in the file.
func rewriteModelSettings(src io.Reader, dst io.Writer, mapping map[int]int, slots int) error {
	return rewriteXML(src, dst, func(el *xml.StartElement) error {
		if el.Name.Local != "metadata" {
			return nil
		}
		var key string
		for _, a := range el.Attr {
			if a.Name.Local == "key" {
				key = a.Value
				break
			}
		}
		switch key {
		case "extruder":
			for i, a := range el.Attr {
				if a.Name.Local != "value" {
					continue
				}
				if v, err := strconv.Atoi(strings.TrimSpace(a.Value)); err == nil {
					if dst, ok := mapping[v]; ok {
						el.Attr[i].Value = strconv.Itoa(dst)
					} else if v > slots {
						el.Attr[i].Value = "1"
					}
				}
			}
		case "filament_maps":
			// Multi-nozzle filament-to-extruder grouping; the target
			// printers handled here have a single tool path per slot.
			for i, a := range el.Attr {
				if a.Name.Local == "value" {
					el.Attr[i].Value = strings.TrimSpace(strings.Repeat("1 ", slots))
				}
			}
		}
		return nil
	})
}
