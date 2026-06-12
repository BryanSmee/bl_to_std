package converter

import (
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/BryanSmee/bl_to_std/pkg/printer"
)

type Slot struct {
	Color string `json:"color"` // #RRGGBB
	Type  string `json:"type"`
	// Profile optionally overrides the filament_settings_id for this slot.
	Profile string `json:"profile,omitempty"`
}

func resolvePlan(insp *Inspection, opts *Options) ([]Slot, map[int]int, error) {
	slots, err := resolveSlots(insp, opts)
	if err != nil {
		return nil, nil, err
	}
	mapping, err := resolveMapping(insp, opts.Mapping, slots)
	if err != nil {
		return nil, nil, err
	}
	return slots, mapping, nil
}

func resolveSlots(insp *Inspection, opts *Options) ([]Slot, error) {
	maxSlots := opts.Printer.FilamentSlots
	if len(opts.Slots) > maxSlots {
		return nil, fmt.Errorf("%d slots given but %s has only %d", len(opts.Slots), opts.Printer.DisplayName, maxSlots)
	}
	if len(opts.Slots) == 0 {
		return autoSlots(insp, opts.Printer), nil
	}
	slots := make([]Slot, len(opts.Slots))
	copy(slots, opts.Slots)
	for i := range slots {
		slots[i].Color = NormalizeColor(slots[i].Color)
		if slots[i].Type == "" {
			slots[i].Type = "PLA"
		}
	}
	return slots, nil
}

// autoSlots keeps the most-used source filaments (so the dominant colors
// survive), in filament-ID order.
func autoSlots(insp *Inspection, profile *printer.Profile) []Slot {
	fils := make([]Filament, len(insp.Filaments))
	copy(fils, insp.Filaments)
	sort.SliceStable(fils, func(i, j int) bool { return fils[i].UsedG > fils[j].UsedG })
	if len(fils) > profile.FilamentSlots {
		fils = fils[:profile.FilamentSlots]
	}
	sort.Slice(fils, func(i, j int) bool { return fils[i].ID < fils[j].ID })

	slots := make([]Slot, 0, len(fils))
	for _, f := range fils {
		t := f.Type
		if len(profile.FilamentProfiles) > 0 {
			if _, ok := profile.FilamentProfiles[t]; !ok {
				t = "PLA"
			}
		}
		slots = append(slots, Slot{Color: f.Color, Type: t})
	}
	return slots
}

func resolveMapping(insp *Inspection, explicit map[int]int, slots []Slot) (map[int]int, error) {
	mapping := make(map[int]int, len(insp.Filaments))
	for src, dst := range explicit {
		if dst < 1 || dst > len(slots) {
			return nil, fmt.Errorf("mapping %d=%d: slot out of range (have %d slots)", src, dst, len(slots))
		}
		if !hasFilament(insp, src) {
			return nil, fmt.Errorf("mapping %d=%d: source file has no filament %d", src, dst, src)
		}
		mapping[src] = dst
	}
	for _, f := range insp.Filaments {
		if _, ok := mapping[f.ID]; !ok {
			mapping[f.ID] = nearestSlot(f.Color, slots)
		}
	}
	return mapping, nil
}

func hasFilament(insp *Inspection, id int) bool {
	for _, f := range insp.Filaments {
		if f.ID == id {
			return true
		}
	}
	return false
}

// nearestSlot returns a 1-based index, preferring exact color matches and
// lower slot numbers on ties.
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
