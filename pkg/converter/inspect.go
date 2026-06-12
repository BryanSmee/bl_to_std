package converter

import (
	"archive/zip"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

const (
	sliceInfoPath       = "Metadata/slice_info.config"
	modelSettingsPath   = "Metadata/model_settings.config"
	projectSettingsPath = "Metadata/project_settings.config"
)

// Filament describes one filament of the source project.
type Filament struct {
	ID    int     `json:"id"`
	Color string  `json:"color"` // #RRGGBB, uppercase
	Type  string  `json:"type"`
	UsedM float64 `json:"used_m"` // metres, summed over all plates
	UsedG float64 `json:"used_g"` // grams, summed over all plates
}

// Inspection summarizes a source 3MF project.
type Inspection struct {
	PrinterModelID string     `json:"printer_model_id,omitempty"`
	Filaments      []Filament `json:"filaments"`
	Plates         int        `json:"plates"`
}

// Inspect opens a 3MF file and reports its filaments.
func Inspect(path string) (*Inspection, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return InspectReader(f, st.Size())
}

// InspectReader is like Inspect for an already-open archive.
func InspectReader(r io.ReaderAt, size int64) (*Inspection, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("not a valid 3MF/zip archive: %w", err)
	}
	return inspectZip(zr)
}

func zipFile(zr *zip.Reader, name string) *zip.File {
	for _, f := range zr.File {
		if f.Name == name {
			return f
		}
	}
	return nil
}

func readZipFile(zr *zip.Reader, name string) ([]byte, error) {
	f := zipFile(zr, name)
	if f == nil {
		return nil, nil
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// sliceInfo mirrors the parts of Metadata/slice_info.config we care about.
type sliceInfoXML struct {
	Plates []struct {
		Metadata []struct {
			Key   string `xml:"key,attr"`
			Value string `xml:"value,attr"`
		} `xml:"metadata"`
		Filaments []sliceInfoFilament `xml:"filament"`
	} `xml:"plate"`
	// Some files keep filaments at the config root.
	Filaments []sliceInfoFilament `xml:"filament"`
}

type sliceInfoFilament struct {
	ID    string `xml:"id,attr"`
	Type  string `xml:"type,attr"`
	Color string `xml:"color,attr"`
	UsedM string `xml:"used_m,attr"`
	UsedG string `xml:"used_g,attr"`
}

func inspectZip(zr *zip.Reader) (*Inspection, error) {
	insp := &Inspection{}

	data, err := readZipFile(zr, sliceInfoPath)
	if err != nil {
		return nil, err
	}
	if data != nil {
		var si sliceInfoXML
		if err := xml.Unmarshal(data, &si); err != nil {
			return nil, fmt.Errorf("%s: %w", sliceInfoPath, err)
		}
		insp.Plates = len(si.Plates)
		byID := map[int]*Filament{}
		add := func(fs []sliceInfoFilament) {
			for _, f := range fs {
				id, err := strconv.Atoi(f.ID)
				if err != nil || id < 1 {
					continue
				}
				fil, ok := byID[id]
				if !ok {
					fil = &Filament{
						ID:    id,
						Color: NormalizeColor(f.Color),
						Type:  f.Type,
					}
					if fil.Type == "" {
						fil.Type = "PLA"
					}
					byID[id] = fil
				}
				if v, err := strconv.ParseFloat(f.UsedM, 64); err == nil {
					fil.UsedM += v
				}
				if v, err := strconv.ParseFloat(f.UsedG, 64); err == nil {
					fil.UsedG += v
				}
			}
		}
		for _, p := range si.Plates {
			add(p.Filaments)
			for _, m := range p.Metadata {
				if m.Key == "printer_model_id" && insp.PrinterModelID == "" {
					insp.PrinterModelID = m.Value
				}
			}
		}
		add(si.Filaments)
		for _, f := range byID {
			insp.Filaments = append(insp.Filaments, *f)
		}
		sort.Slice(insp.Filaments, func(i, j int) bool { return insp.Filaments[i].ID < insp.Filaments[j].ID })
	}

	if len(insp.Filaments) == 0 {
		// Fall back to the filament arrays in project_settings.config.
		data, err := readZipFile(zr, projectSettingsPath)
		if err != nil {
			return nil, err
		}
		if data != nil {
			var cfg struct {
				Colors []string `json:"filament_colour"`
				Types  []string `json:"filament_type"`
			}
			if err := json.Unmarshal(data, &cfg); err != nil {
				return nil, fmt.Errorf("%s: %w", projectSettingsPath, err)
			}
			for i, c := range cfg.Colors {
				f := Filament{ID: i + 1, Color: NormalizeColor(c), Type: "PLA"}
				if i < len(cfg.Types) && cfg.Types[i] != "" {
					f.Type = cfg.Types[i]
				}
				insp.Filaments = append(insp.Filaments, f)
			}
		}
	}

	if len(insp.Filaments) == 0 {
		return nil, fmt.Errorf("no filament information found in the 3MF (missing %s and %s)", sliceInfoPath, projectSettingsPath)
	}
	return insp, nil
}

// ValidColor reports whether c is a #RRGGBB or #RRGGBBAA hex color.
func ValidColor(c string) bool {
	c = strings.TrimPrefix(strings.TrimSpace(c), "#")
	if len(c) != 6 && len(c) != 8 {
		return false
	}
	_, err := strconv.ParseUint(c, 16, 64)
	return err == nil
}

// NormalizeColor returns an uppercase #RRGGBB color, dropping any alpha
// channel. Invalid input yields #FFFFFF.
func NormalizeColor(c string) string {
	c = strings.TrimPrefix(strings.TrimSpace(c), "#")
	if len(c) == 8 {
		c = c[:6]
	}
	if len(c) != 6 {
		return "#FFFFFF"
	}
	if _, err := strconv.ParseUint(c, 16, 32); err != nil {
		return "#FFFFFF"
	}
	return "#" + strings.ToUpper(c)
}
