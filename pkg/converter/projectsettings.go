package converter

import (
	"encoding/json"
	"fmt"

	"github.com/BryanSmee/bl_to_std/pkg/printer"
)

// Support tuning carried over from the source project when supports are
// enabled; these keys are shared across the PrusaSlicer/Bambu/Orca family.
var supportCarryKeys = []string{
	"support_type",
	"support_style",
	"support_threshold_angle",
	"support_on_build_plate_only",
}

func buildProjectSettings(profile *printer.Profile, slots []Slot, slotCount int, supports bool, source map[string]any) ([]byte, error) {
	cfg, err := copyBaseline(profile)
	if err != nil {
		return nil, err
	}
	applySlotArrays(cfg, profile, slots, slotCount)
	normalizeFilamentArrays(cfg, slotCount)
	applySupportSettings(cfg, supports, source, slotCount)

	out, err := json.MarshalIndent(cfg, "", "    ")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", projectSettingsPath, err)
	}
	return out, nil
}

// copyBaseline deep-copies the profile's settings so profiles stay reusable.
func copyBaseline(profile *printer.Profile) (map[string]any, error) {
	raw, err := json.Marshal(profile.ProjectSettings)
	if err != nil {
		return nil, err
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func applySlotArrays(cfg map[string]any, profile *printer.Profile, slots []Slot, n int) {
	colors := make([]any, n)
	types := make([]any, n)
	settingsIDs := make([]any, n)
	for i := 0; i < n; i++ {
		if i < len(slots) {
			colors[i] = slots[i].Color
			types[i] = slots[i].Type
			if slots[i].Profile != "" {
				settingsIDs[i] = slots[i].Profile
			} else {
				settingsIDs[i] = profile.FilamentProfile(slots[i].Type)
			}
		} else {
			colors[i] = "#FFFFFF"
			types[i] = "PLA"
			settingsIDs[i] = profile.FilamentProfile("PLA")
		}
	}
	cfg["filament_colour"] = colors
	cfg["filament_type"] = types
	cfg["filament_settings_id"] = settingsIDs
}

func normalizeFilamentArrays(cfg map[string]any, n int) {
	for key, val := range cfg {
		list, ok := val.([]any)
		if !ok || len(list) == 0 {
			continue
		}
		switch {
		case len(key) > 9 && key[:9] == "filament_":
			cfg[key] = resizeList(list, n)
		case key == "flush_volumes_matrix":
			cfg[key] = resizeList(list, n*n)
		}
	}
}

func applySupportSettings(cfg map[string]any, supports bool, source map[string]any, n int) {
	if !supports {
		cfg["enable_support"] = "0"
		delete(cfg, "different_settings_to_system")
		return
	}
	cfg["enable_support"] = "1"
	// different_settings_to_system lists the user overrides per settings
	// group: element 0 is the print profile ("key1;key2"), followed by one
	// element per filament and one for the printer.
	diffPrint := "enable_support"
	for _, k := range supportCarryKeys {
		if v, ok := source[k]; ok {
			cfg[k] = v
			diffPrint += ";" + k
		}
	}
	diff := make([]any, n+2)
	diff[0] = diffPrint
	for i := 1; i < len(diff); i++ {
		diff[i] = ""
	}
	cfg["different_settings_to_system"] = diff
}

func resizeList(list []any, n int) []any {
	if len(list) == n {
		return list
	}
	if len(list) > n {
		return list[:n]
	}
	out := make([]any, n)
	copy(out, list)
	for i := len(list); i < n; i++ {
		out[i] = list[len(list)-1]
	}
	return out
}
