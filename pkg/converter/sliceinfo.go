package converter

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
)

// rewriteSliceInfo rebuilds the filament list of Metadata/slice_info.config.
//
// Original <filament> elements are removed and replaced by one entry per
// target slot, with used_m/used_g aggregated from the source filaments that
// were mapped onto the slot. The printer_model_id metadata is retargeted.
// Everything else (header, per-plate objects, warnings, ...) is preserved.
func rewriteSliceInfo(src []byte, dst io.Writer, slots []Slot, mapping map[int]int, modelID string) error {
	// First pass: per-plate usage aggregation, slot -> [metres, grams].
	var si sliceInfoXML
	if err := xml.Unmarshal(src, &si); err != nil {
		return fmt.Errorf("%s: %w", sliceInfoPath, err)
	}
	aggregate := func(fs []sliceInfoFilament) map[int][2]float64 {
		usage := map[int][2]float64{}
		for _, f := range fs {
			id, err := strconv.Atoi(f.ID)
			if err != nil {
				continue
			}
			slot, ok := mapping[id]
			if !ok {
				continue
			}
			u := usage[slot]
			if v, err := strconv.ParseFloat(f.UsedM, 64); err == nil {
				u[0] += v
			}
			if v, err := strconv.ParseFloat(f.UsedG, 64); err == nil {
				u[1] += v
			}
			usage[slot] = u
		}
		return usage
	}
	plateUsage := make([]map[int][2]float64, len(si.Plates))
	for i, p := range si.Plates {
		plateUsage[i] = aggregate(p.Filaments)
	}
	rootUsage := aggregate(si.Filaments)
	// Filaments normally live under <plate>; fall back to <config> for
	// files without plates.
	parent := "plate"
	if len(si.Plates) == 0 {
		parent = "config"
	}

	emitFilaments := func(out *rawXMLWriter, usage map[int][2]float64) error {
		for i, s := range slots {
			u := usage[i+1]
			el := xml.StartElement{
				Name: xml.Name{Local: "filament"},
				Attr: []xml.Attr{
					{Name: xml.Name{Local: "id"}, Value: strconv.Itoa(i + 1)},
					{Name: xml.Name{Local: "type"}, Value: s.Type},
					{Name: xml.Name{Local: "color"}, Value: s.Color},
					{Name: xml.Name{Local: "used_m"}, Value: strconv.FormatFloat(u[0], 'f', 2, 64)},
					{Name: xml.Name{Local: "used_g"}, Value: strconv.FormatFloat(u[1], 'f', 2, 64)},
				},
			}
			if err := out.writeToken(xml.CharData("  ")); err != nil {
				return err
			}
			if err := out.writeToken(el); err != nil {
				return err
			}
			if err := out.writeToken(el.End()); err != nil {
				return err
			}
			if err := out.writeToken(xml.CharData("\n")); err != nil {
				return err
			}
		}
		return nil
	}

	dec := xml.NewDecoder(bytes.NewReader(src))
	out := newRawXMLWriter(dst)
	plateIdx := -1
	for {
		tok, err := dec.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "filament" {
				continue
			}
			if t.Name.Local == "plate" {
				plateIdx++
			}
			if t.Name.Local == "metadata" && modelID != "" {
				isModelKey := false
				for _, a := range t.Attr {
					if a.Name.Local == "key" && a.Value == "printer_model_id" {
						isModelKey = true
					}
				}
				if isModelKey {
					for i, a := range t.Attr {
						if a.Name.Local == "value" {
							t.Attr[i].Value = modelID
						}
					}
				}
			}
			tok = t
		case xml.EndElement:
			if t.Name.Local == "filament" {
				continue
			}
			if t.Name.Local == parent {
				usage := rootUsage
				if parent == "plate" && plateIdx >= 0 && plateIdx < len(plateUsage) {
					usage = plateUsage[plateIdx]
				}
				if err := emitFilaments(out, usage); err != nil {
					return err
				}
			}
		}
		if err := out.writeToken(tok); err != nil {
			return err
		}
	}
	return out.close()
}
