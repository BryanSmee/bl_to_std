package converter

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
)

// filamentUsage accumulates used metres ([0]) and grams ([1]) per slot.
type filamentUsage map[int][2]float64

// rewriteSliceInfo rebuilds the filament list of Metadata/slice_info.config.
//
// Original <filament> elements are removed and replaced by one entry per
// target slot, with used_m/used_g aggregated from the source filaments that
// were mapped onto the slot. The printer_model_id metadata is retargeted.
// Everything else (header, per-plate objects, warnings, ...) is preserved.
func rewriteSliceInfo(src []byte, dst io.Writer, slots []Slot, mapping map[int]int, modelID string) error {
	plateUsage, rootUsage, parent, err := collectSliceUsage(src, mapping)
	if err != nil {
		return err
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
			retargetPrinterModel(&t, modelID)
			tok = t
		case xml.EndElement:
			if t.Name.Local == "filament" {
				continue
			}
			if t.Name.Local == parent {
				usage := usageForParent(parent, plateIdx, plateUsage, rootUsage)
				if err := emitSlotFilaments(out, slots, usage); err != nil {
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

// collectSliceUsage parses slice_info.config once to aggregate filament
// usage per plate (and at the config root, for files without plates) onto
// the target slots. parent is the element that holds <filament> entries.
func collectSliceUsage(src []byte, mapping map[int]int) (plateUsage []filamentUsage, rootUsage filamentUsage, parent string, err error) {
	var si sliceInfoXML
	if err := xml.Unmarshal(src, &si); err != nil {
		return nil, nil, "", fmt.Errorf("%s: %w", sliceInfoPath, err)
	}
	plateUsage = make([]filamentUsage, len(si.Plates))
	for i, p := range si.Plates {
		plateUsage[i] = aggregateUsage(p.Filaments, mapping)
	}
	rootUsage = aggregateUsage(si.Filaments, mapping)
	parent = "plate"
	if len(si.Plates) == 0 {
		parent = "config"
	}
	return plateUsage, rootUsage, parent, nil
}

// usageForParent picks the usage aggregate for the element whose closing
// tag triggers the filament emission: the current plate, or the config
// root for files without plates.
func usageForParent(parent string, plateIdx int, plateUsage []filamentUsage, rootUsage filamentUsage) filamentUsage {
	if parent == "plate" && plateIdx >= 0 && plateIdx < len(plateUsage) {
		return plateUsage[plateIdx]
	}
	return rootUsage
}

func aggregateUsage(fs []sliceInfoFilament, mapping map[int]int) filamentUsage {
	usage := filamentUsage{}
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

// emitSlotFilaments writes one <filament> element per target slot.
func emitSlotFilaments(out *rawXMLWriter, slots []Slot, usage filamentUsage) error {
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
		for _, tok := range []xml.Token{xml.CharData("  "), el, el.End(), xml.CharData("\n")} {
			if err := out.writeToken(tok); err != nil {
				return err
			}
		}
	}
	return nil
}

// retargetPrinterModel rewrites <metadata key="printer_model_id"> in place.
func retargetPrinterModel(el *xml.StartElement, modelID string) {
	if el.Name.Local != "metadata" || modelID == "" {
		return
	}
	isModelKey := false
	for _, a := range el.Attr {
		if a.Name.Local == "key" && a.Value == "printer_model_id" {
			isModelKey = true
		}
	}
	if !isModelKey {
		return
	}
	for i, a := range el.Attr {
		if a.Name.Local == "value" {
			el.Attr[i].Value = modelID
		}
	}
}
