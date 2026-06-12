// Package printer defines target printer profiles for the converter.
//
// A profile bundles everything the converter needs to retarget a project:
// the printer model identifier, the number of filament slots the machine
// supports, a baseline project_settings.config (as produced by the target
// slicer, e.g. Snapmaker Orca), and the mapping from filament material
// types to slicer filament profile names.
package printer

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

//go:embed templates/snapmaker_u1_settings.json
var snapmakerU1Settings []byte

// Profile describes a conversion target.
type Profile struct {
	// Name is the identifier used to select the profile (e.g. "snapmaker-u1").
	Name string `json:"name"`
	// DisplayName is a human readable printer name.
	DisplayName string `json:"display_name"`
	// ModelID is written as printer_model_id in Metadata/slice_info.config.
	ModelID string `json:"printer_model_id"`
	// FilamentSlots is the number of filament slots the printer supports.
	// Source filaments are mapped down (many-to-one) to this many slots.
	FilamentSlots int `json:"filament_slots"`
	// FilamentProfiles maps a material type (e.g. "PLA") to the slicer
	// filament profile name (filament_settings_id).
	FilamentProfiles map[string]string `json:"filament_profiles"`
	// DefaultFilamentProfile is used for material types not present in
	// FilamentProfiles.
	DefaultFilamentProfile string `json:"default_filament_profile"`
	// ProjectSettings is the baseline Metadata/project_settings.config for
	// the target printer. The converter overwrites the filament arrays and
	// support switches in a copy of it.
	ProjectSettings map[string]any `json:"project_settings"`
}

// Validate checks that the profile is usable.
func (p *Profile) Validate() error {
	if p.Name == "" {
		return fmt.Errorf("printer profile: name is required")
	}
	if p.FilamentSlots < 1 {
		return fmt.Errorf("printer profile %q: filament_slots must be >= 1", p.Name)
	}
	if len(p.ProjectSettings) == 0 {
		return fmt.Errorf("printer profile %q: project_settings is required", p.Name)
	}
	return nil
}

// FilamentProfile returns the filament_settings_id for a material type.
func (p *Profile) FilamentProfile(materialType string) string {
	if id, ok := p.FilamentProfiles[materialType]; ok {
		return id
	}
	return p.DefaultFilamentProfile
}

// MaterialTypes lists the material types the profile knows, sorted.
func (p *Profile) MaterialTypes() []string {
	types := make([]string, 0, len(p.FilamentProfiles))
	for t := range p.FilamentProfiles {
		types = append(types, t)
	}
	sort.Strings(types)
	return types
}

func snapmakerU1() *Profile {
	var settings map[string]any
	if err := json.Unmarshal(snapmakerU1Settings, &settings); err != nil {
		panic(fmt.Sprintf("printer: invalid embedded snapmaker u1 settings: %v", err))
	}
	return &Profile{
		Name:          "snapmaker-u1",
		DisplayName:   "Snapmaker U1",
		ModelID:       "Snapmaker U1",
		FilamentSlots: 4,
		FilamentProfiles: map[string]string{
			"PLA":     "Snapmaker PLA SnapSpeed @U1",
			"PETG":    "Snapmaker PETG HF",
			"PETG-HF": "Snapmaker PETG HF",
			"ABS":     "Generic ABS",
			"TPU":     "Generic TPU",
		},
		DefaultFilamentProfile: "Snapmaker PLA SnapSpeed @U1",
		ProjectSettings:        settings,
	}
}

// Builtin returns the built-in profile with the given name, or nil.
func Builtin(name string) *Profile {
	for _, p := range Builtins() {
		if p.Name == name {
			return p
		}
	}
	return nil
}

// Builtins returns all built-in profiles.
func Builtins() []*Profile {
	return []*Profile{snapmakerU1()}
}

// Load reads a custom profile from a JSON file. The file uses the same
// shape as the Profile struct, including the full project_settings object.
func Load(path string) (*Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("printer profile: %w", err)
	}
	var p Profile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("printer profile %s: %w", path, err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// Resolve returns a built-in profile by name, or loads a JSON profile if
// nameOrPath points to an existing file.
func Resolve(nameOrPath string) (*Profile, error) {
	if p := Builtin(nameOrPath); p != nil {
		return p, nil
	}
	if _, err := os.Stat(nameOrPath); err == nil {
		return Load(nameOrPath)
	}
	names := make([]string, 0)
	for _, p := range Builtins() {
		names = append(names, p.Name)
	}
	return nil, fmt.Errorf("unknown printer profile %q (built-in profiles: %v, or pass a path to a profile JSON file)", nameOrPath, names)
}
