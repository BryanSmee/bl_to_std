package converter

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/BryanSmee/bl_to_std/pkg/printer"
)

type Slot struct {
	Color string `json:"color"` // #RRGGBB
	Type  string `json:"type"`
	// Profile optionally overrides the filament_settings_id for this slot.
	Profile string `json:"profile,omitempty"`
	// Support marks a slot holding support material; color regions are not
	// mapped onto it unless no regular slot is available.
	Support bool `json:"support,omitempty"`
}

// IsSupportType reports whether a material type/sub-type denotes support
// filament (dissolvable or breakaway), which should not carry model colors.
func IsSupportType(materialType, subType string) bool {
	s := strings.ToLower(materialType + " " + subType)
	return strings.Contains(s, "support") || strings.Contains(s, "breakaway") ||
		strings.EqualFold(strings.TrimSpace(materialType), "PVA")
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
		slots[i].Support = slots[i].Support || IsSupportType(slots[i].Type, "")
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
	usedSlots := map[int]bool{}
	for src, dst := range explicit {
		if dst < 1 || dst > len(slots) {
			return nil, fmt.Errorf("mapping %d=%d: slot out of range (have %d slots)", src, dst, len(slots))
		}
		if !hasFilament(insp, src) {
			return nil, fmt.Errorf("mapping %d=%d: source file has no filament %d", src, dst, src)
		}
		mapping[src] = dst
		usedSlots[dst] = true
	}

	var free []Filament
	for _, f := range insp.Filaments {
		if _, ok := mapping[f.ID]; !ok {
			free = append(free, f)
		}
	}

	// Support slots are not color-mapping targets while regular slots exist;
	// the support filament stays configured but carries no model color.
	candidates := mappableSlots(slots)

	// When every source filament can have its own target slot, assign them
	// injectively so distinct source colors are never collapsed together;
	// only fall back to many-to-one nearest-color when colors outnumber slots.
	if len(insp.Filaments) <= len(candidates) {
		assignInjective(mapping, free, slots, usedSlots, candidates)
	} else {
		for _, f := range free {
			mapping[f.ID] = nearestSlot(f.Color, slots, candidates)
		}
	}
	return mapping, nil
}

// mappableSlots lists the 1-based indices eligible as color-mapping targets:
// the non-support slots, or all slots when every slot is support.
func mappableSlots(slots []Slot) []int {
	var regular []int
	for i, s := range slots {
		if !s.Support {
			regular = append(regular, i+1)
		}
	}
	if len(regular) > 0 {
		return regular
	}
	all := make([]int, len(slots))
	for i := range slots {
		all[i] = i + 1
	}
	return all
}

// assignInjective gives each free source filament its own slot from the
// candidate indices, greedily taking the globally closest source/slot color
// pair first.
func assignInjective(mapping map[int]int, free []Filament, slots []Slot, usedSlots map[int]bool, candidates []int) {
	type pair struct {
		src, slot int
		dist      float64
	}
	var pairs []pair
	for _, f := range free {
		r1, g1, b1 := rgb(f.Color)
		for _, slot := range candidates {
			if usedSlots[slot] {
				continue
			}
			r2, g2, b2 := rgb(slots[slot-1].Color)
			pairs = append(pairs, pair{f.ID, slot, colorDistance(r1, g1, b1, r2, g2, b2)})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].dist != pairs[j].dist {
			return pairs[i].dist < pairs[j].dist
		}
		if pairs[i].src != pairs[j].src {
			return pairs[i].src < pairs[j].src
		}
		return pairs[i].slot < pairs[j].slot
	})
	for _, p := range pairs {
		if _, ok := mapping[p.src]; ok || usedSlots[p.slot] {
			continue
		}
		mapping[p.src] = p.slot
		usedSlots[p.slot] = true
	}
	for _, f := range free {
		if _, ok := mapping[f.ID]; !ok {
			mapping[f.ID] = nearestSlot(f.Color, slots, candidates)
		}
	}
}

func hasFilament(insp *Inspection, id int) bool {
	for _, f := range insp.Filaments {
		if f.ID == id {
			return true
		}
	}
	return false
}

// nearestSlot returns the 1-based index of the closest candidate slot,
// preferring exact color matches and lower slot numbers on ties.
func nearestSlot(c string, slots []Slot, candidates []int) int {
	best, bestDist := candidates[0], math.Inf(1)
	r1, g1, b1 := rgb(c)
	for _, idx := range candidates {
		s := slots[idx-1]
		if s.Color == c {
			return idx
		}
		r2, g2, b2 := rgb(s.Color)
		if d := colorDistance(r1, g1, b1, r2, g2, b2); d < bestDist {
			best, bestDist = idx, d
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
