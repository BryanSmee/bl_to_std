package converter

import (
	"testing"

	"github.com/BryanSmee/bl_to_std/pkg/printer"
)

func u1() *printer.Profile { return printer.Builtin("snapmaker-u1") }

// A 2-color model converted against 4 distinct loaded filaments must keep
// its two colors on two distinct slots, never collapse onto one. This is
// the --from-printer case from the field report (green + black model,
// near-black/white/yellow/red loaded).
func TestMappingInjectiveWhenSlotsSpare(t *testing.T) {
	insp := &Inspection{Filaments: []Filament{
		{ID: 1, Color: "#3F8E43", Type: "PLA"}, // green
		{ID: 2, Color: "#000000", Type: "PLA"}, // black
	}}
	opts := &Options{
		Printer: u1(),
		Slots: []Slot{
			{Color: "#080A0D", Type: "PLA"}, // near black
			{Color: "#E2DEDB", Type: "PLA"}, // near white
			{Color: "#F6CE1B", Type: "PLA"}, // yellow
			{Color: "#E72F1D", Type: "PLA"}, // red
		},
	}
	_, mapping, err := resolvePlan(insp, opts)
	if err != nil {
		t.Fatal(err)
	}
	if mapping[1] == mapping[2] {
		t.Fatalf("distinct source colors collapsed onto slot %d: %v", mapping[1], mapping)
	}
	if mapping[2] != 1 {
		t.Errorf("black should map to the near-black slot 1, got %d", mapping[2])
	}
}

// Explicit --map entries are honored, and remaining filaments still get
// distinct slots when there is room.
func TestMappingInjectiveRespectsExplicit(t *testing.T) {
	insp := &Inspection{Filaments: []Filament{
		{ID: 1, Color: "#000000", Type: "PLA"},
		{ID: 2, Color: "#0A0A0A", Type: "PLA"},
		{ID: 3, Color: "#FF0000", Type: "PLA"},
	}}
	opts := &Options{
		Printer: u1(),
		Slots: []Slot{
			{Color: "#000000", Type: "PLA"},
			{Color: "#111111", Type: "PLA"},
			{Color: "#FF0000", Type: "PLA"},
			{Color: "#00FF00", Type: "PLA"},
		},
		Mapping: map[int]int{1: 4},
	}
	_, mapping, err := resolvePlan(insp, opts)
	if err != nil {
		t.Fatal(err)
	}
	if mapping[1] != 4 {
		t.Errorf("explicit map 1=4 not honored: %v", mapping)
	}
	if mapping[2] == mapping[3] || mapping[2] == 4 || mapping[3] == 4 {
		t.Errorf("free filaments should take distinct unused slots: %v", mapping)
	}
}

// A support filament loaded in the printer must not receive model colors
// while regular slots are available, even when its color is the closest.
func TestMappingAvoidsSupportSlot(t *testing.T) {
	insp := &Inspection{Filaments: []Filament{
		{ID: 1, Color: "#3F8E43", Type: "PLA"},
		{ID: 2, Color: "#000000", Type: "PLA"},
	}}
	opts := &Options{
		Printer: u1(),
		Slots: []Slot{
			{Color: "#080A0D", Type: "PLA"},                // black
			{Color: "#E2DEDB", Type: "PLA"},                // off-white
			{Color: "#F6CE1B", Type: "PLA"},                // yellow
			{Color: "#FFFFFF", Type: "PLA", Support: true}, // support (white)
		},
	}
	_, mapping, err := resolvePlan(insp, opts)
	if err != nil {
		t.Fatal(err)
	}
	for src, slot := range mapping {
		if slot == 4 {
			t.Errorf("source %d mapped to the support slot 4: %v", src, mapping)
		}
	}
	if mapping[1] == mapping[2] {
		t.Errorf("distinct colors collapsed: %v", mapping)
	}
}

// Explicit --map may still target a support slot, and when colors outnumber
// the regular slots the support slot is still avoided.
func TestMappingSupportRespectsExplicitAndCrowding(t *testing.T) {
	insp := &Inspection{Filaments: []Filament{
		{ID: 1, Color: "#FF0000", Type: "PLA"},
		{ID: 2, Color: "#00FF00", Type: "PLA"},
		{ID: 3, Color: "#0000FF", Type: "PLA"},
	}}
	opts := &Options{
		Printer: u1(),
		Slots: []Slot{
			{Color: "#FF0000", Type: "PLA"},
			{Color: "#00FF00", Type: "PLA", Support: true},
			{Color: "#1010FF", Type: "PLA"},
		},
		Mapping: map[int]int{2: 2}, // user forces green onto the support slot
	}
	_, mapping, err := resolvePlan(insp, opts)
	if err != nil {
		t.Fatal(err)
	}
	if mapping[2] != 2 {
		t.Errorf("explicit map to support slot not honored: %v", mapping)
	}
	// 1 and 3 must avoid the support slot 2 even though 3 colors > 2 regular slots.
	if mapping[1] == 2 || mapping[3] == 2 {
		t.Errorf("auto mapping used the support slot: %v", mapping)
	}
}

// More source colors than slots still collapses by nearest color.
func TestMappingManyToOneWhenColorsOutnumberSlots(t *testing.T) {
	insp := &Inspection{Filaments: []Filament{
		{ID: 1, Color: "#FF0000", Type: "PLA"},
		{ID: 2, Color: "#FE0000", Type: "PLA"},
		{ID: 3, Color: "#00FF00", Type: "PLA"},
	}}
	opts := &Options{
		Printer: u1(),
		Slots: []Slot{
			{Color: "#FF0000", Type: "PLA"},
			{Color: "#00FF00", Type: "PLA"},
		},
	}
	_, mapping, err := resolvePlan(insp, opts)
	if err != nil {
		t.Fatal(err)
	}
	if mapping[1] != 1 || mapping[2] != 1 {
		t.Errorf("near-identical reds should both fold onto slot 1: %v", mapping)
	}
	if mapping[3] != 2 {
		t.Errorf("green should map to slot 2: %v", mapping)
	}
}
