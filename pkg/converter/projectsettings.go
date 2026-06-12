package converter

import (
	"encoding/json"
	"fmt"

	"github.com/BryanSmee/bl_to_std/pkg/printer"
)

// supportCarryKeys are support-related settings carried over from the
// source project when supports are enabled, so a model tuned for tree
// supports keeps that intent after conversion. The keys are shared across
// the PrusaSlicer/Bambu Studio/OrcaSlicer family.
var supportCarryKeys = []string{
	"support_type",
	"support_style",
	"support_threshold_angle",
	"support_on_build_plate_only",
}

// buildProjectSettings produces the target Metadata/project_settings.config:
// the printer profile's baseline with the filament arrays rewritten for the
// chosen slots and support settings applied.
func buildProjectSettings(profile *printer.Profile, slots []Slot, supports bool, source map[string]any) ([]byte, error) {
	// Deep-copy the baseline so profiles stay reusable.
	raw, err := json.Marshal(profile.ProjectSettings)
	if err != nil {
		return nil, err
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}

	n := profile.FilamentSlots
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
			// Unused slots are padded with white PLA.
			colors[i] = "#FFFFFF"
			types[i] = "PLA"
			settingsIDs[i] = profile.FilamentProfile("PLA")
		}
	}
	cfg["filament_colour"] = colors
	cfg["filament_type"] = types
	cfg["filament_settings_id"] = settingsIDs

	// Normalize every per-filament array to the slot count.
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

	if supports {
		cfg["enable_support"] = "1"
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
	} else {
		cfg["enable_support"] = "0"
		delete(cfg, "different_settings_to_system")
	}

	out, err := json.MarshalIndent(cfg, "", "    ")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", projectSettingsPath, err)
	}
	return out, nil
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
