package converter

import (
	"fmt"
	"math"
	"sort"
	"strconv"
)

// Slot is one target filament slot on the destination printer.
type Slot struct {
	Color string `json:"color"` // #RRGGBB
	Type  string `json:"type"`  // material type, e.g. PLA
	// Profile optionally overrides the filament_settings_id for this slot.
	Profile string `json:"profile,omitempty"`
}

// resolvePlan decides the target slots and the source->slot mapping.
//
// Slots: taken from opts.Slots when given; otherwise auto-derived from the
// most-used source filaments (so the dominant colors survive). Mapping:
// explicit entries from opts.Mapping win; every other source filament goes
// to the slot with the nearest color.
func resolvePlan(insp *Inspection, opts *Options) ([]Slot, map[int]int, error) {
	maxSlots := opts.Printer.FilamentSlots

	slots := make([]Slot, len(opts.Slots))
	copy(slots, opts.Slots)
	if len(slots) > maxSlots {
		return nil, nil, fmt.Errorf("%d slots given but %s has only %d", len(slots), opts.Printer.DisplayName, maxSlots)
	}
	for i := range slots {
		slots[i].Color = NormalizeColor(slots[i].Color)
		if slots[i].Type == "" {
			slots[i].Type = "PLA"
		}
	}

	if len(slots) == 0 {
		// Auto-derive slots: most-used filaments first, ties by ID.
		fils := make([]Filament, len(insp.Filaments))
		copy(fils, insp.Filaments)
		sort.SliceStable(fils, func(i, j int) bool { return fils[i].UsedG > fils[j].UsedG })
		if len(fils) > maxSlots {
			fils = fils[:maxSlots]
		}
		sort.Slice(fils, func(i, j int) bool { return fils[i].ID < fils[j].ID })
		for _, f := range fils {
			t := f.Type
			if len(opts.Printer.FilamentProfiles) > 0 {
				if _, ok := opts.Printer.FilamentProfiles[t]; !ok {
					t = "PLA"
				}
			}
			slots = append(slots, Slot{Color: f.Color, Type: t})
		}
	}

	mapping := make(map[int]int, len(insp.Filaments))
	for src, dst := range opts.Mapping {
		if dst < 1 || dst > len(slots) {
			return nil, nil, fmt.Errorf("mapping %d=%d: slot out of range (have %d slots)", src, dst, len(slots))
		}
		found := false
		for _, f := range insp.Filaments {
			if f.ID == src {
				found = true
				break
			}
		}
		if !found {
			return nil, nil, fmt.Errorf("mapping %d=%d: source file has no filament %d", src, dst, src)
		}
		mapping[src] = dst
	}
	for _, f := range insp.Filaments {
		if _, ok := mapping[f.ID]; ok {
			continue
		}
		mapping[f.ID] = nearestSlot(f.Color, slots)
	}
	return slots, mapping, nil
}

// nearestSlot returns the 1-based index of the slot whose color is closest
// to c, preferring exact matches and lower slot numbers on ties.
func nearestSlot(c string, slots []Slot) int {
	best, bestDist := 1, math.Inf(1)
	r1, g1, b1 := rgb(c)
	for i, s := range slots {
		if s.Color == c {
			return i + 1
		}
		r2, g2, b2 := rgb(s.Color)
		if d := colorDistance(r1, g1, b1, r2, g2, b2); d < bestDist {
			best, bestDist = i+1, d
		}
	}
	return best
}

func rgb(c string) (float64, float64, float64) {
	if len(c) != 7 {
		return 255, 255, 255
	}
	v, err := strconv.ParseUint(c[1:], 16, 32)
	if err != nil {
		return 255, 255, 255
	}
	return float64(v >> 16 & 0xFF), float64(v >> 8 & 0xFF), float64(v & 0xFF)
}

// colorDistance is the "redmean" approximation of perceptual RGB distance.
func colorDistance(r1, g1, b1, r2, g2, b2 float64) float64 {
	rMean := (r1 + r2) / 2
	dr, dg, db := r1-r2, g1-g2, b1-b2
	return math.Sqrt((2+rMean/256)*dr*dr + 4*dg*dg + (2+(255-rMean)/256)*db*db)
}
