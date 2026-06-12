// Package converter transforms Bambu Lab project 3MF files into standard
// 3MF projects for other multi-filament printers (such as the Snapmaker
// U1), preserving multi-color paint data while mapping any number of
// source filaments many-to-one onto the target printer's filament slots.
package converter

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/BryanSmee/bl_to_std/pkg/printer"
)

// SupportMode controls the enable_support switch of the output project.
type SupportMode string

const (
	// SupportsAuto enables supports if the source project had them enabled.
	SupportsAuto SupportMode = "auto"
	SupportsOn   SupportMode = "on"
	SupportsOff  SupportMode = "off"
)

// Options configures a conversion.
type Options struct {
	// Printer is the target printer profile. Defaults to the built-in
	// Snapmaker U1 profile.
	Printer *printer.Profile
	// Slots define the target filaments (color/type). When empty, slots
	// are auto-derived from the most-used source filaments. At most
	// Printer.FilamentSlots entries.
	Slots []Slot
	// Mapping forces specific source filament IDs onto slot numbers
	// (1-based). Source filaments not listed are mapped to the slot with
	// the nearest color.
	Mapping map[int]int
	// Supports defaults to SupportsAuto.
	Supports SupportMode
}

// Result reports what a conversion did.
type Result struct {
	Source          *Inspection `json:"source"`
	Printer         string      `json:"printer"`
	Slots           []Slot      `json:"slots"`
	Mapping         map[int]int `json:"mapping"`
	SupportsEnabled bool        `json:"supports_enabled"`
}

// Convert reads the Bambu Lab 3MF at srcPath and writes the converted
// project to dstPath.
func Convert(srcPath, dstPath string, opts Options) (*Result, error) {
	src, err := os.Open(srcPath)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	st, err := src.Stat()
	if err != nil {
		return nil, err
	}
	dst, err := os.Create(dstPath)
	if err != nil {
		return nil, err
	}
	res, err := ConvertReader(src, st.Size(), dst, opts)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dstPath)
		return nil, err
	}
	return res, nil
}

// ConvertReader converts a 3MF archive read from r into w.
func ConvertReader(r io.ReaderAt, size int64, w io.Writer, opts Options) (*Result, error) {
	if opts.Printer == nil {
		opts.Printer = printer.Builtin("snapmaker-u1")
	}
	if err := opts.Printer.Validate(); err != nil {
		return nil, err
	}
	if opts.Supports == "" {
		opts.Supports = SupportsAuto
	}

	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("not a valid 3MF/zip archive: %w", err)
	}

	insp, err := inspectZip(zr)
	if err != nil {
		return nil, err
	}
	slots, mapping, err := resolvePlan(insp, &opts)
	if err != nil {
		return nil, err
	}
	// The output always declares the full set of hardware slots; unused
	// ones are padded with white PLA (matching the project settings).
	paddedSlots := make([]Slot, opts.Printer.FilamentSlots)
	for i := range paddedSlots {
		if i < len(slots) {
			paddedSlots[i] = slots[i]
		} else {
			paddedSlots[i] = Slot{Color: "#FFFFFF", Type: "PLA"}
		}
	}

	sourceSettings := map[string]any{}
	if data, err := readZipFile(zr, projectSettingsPath); err != nil {
		return nil, err
	} else if data != nil {
		if err := json.Unmarshal(data, &sourceSettings); err != nil {
			return nil, fmt.Errorf("%s: %w", projectSettingsPath, err)
		}
	}
	supports := opts.Supports == SupportsOn
	if opts.Supports == SupportsAuto {
		supports = sourceSupportsEnabled(sourceSettings)
	}

	newSettings, err := buildProjectSettings(opts.Printer, slots, supports, sourceSettings)
	if err != nil {
		return nil, err
	}

	zw := zip.NewWriter(w)
	wroteProjectSettings := false
	for _, f := range zr.File {
		clean := path.Clean(f.Name)
		if strings.HasPrefix(clean, "..") || strings.HasPrefix(clean, "/") {
			continue // zip-slip defence: drop suspicious entries
		}
		switch {
		case f.Name == projectSettingsPath:
			if err := writeEntry(zw, f.Name, newSettings); err != nil {
				return nil, err
			}
			wroteProjectSettings = true
		case f.Name == sliceInfoPath:
			data, err := readAll(f)
			if err != nil {
				return nil, err
			}
			ew, err := newDeflateEntry(zw, f.Name)
			if err != nil {
				return nil, err
			}
			if err := rewriteSliceInfo(data, ew, paddedSlots, mapping, opts.Printer.ModelID); err != nil {
				return nil, fmt.Errorf("%s: %w", f.Name, err)
			}
		case f.Name == modelSettingsPath:
			if err := transformEntry(zw, f, func(rc io.Reader, ew io.Writer) error {
				return rewriteModelSettings(rc, ew, mapping, opts.Printer.FilamentSlots)
			}); err != nil {
				return nil, err
			}
		case strings.HasSuffix(f.Name, ".model"):
			if err := transformEntry(zw, f, func(rc io.Reader, ew io.Writer) error {
				return rewriteModelPaint(rc, ew, mapping)
			}); err != nil {
				return nil, err
			}
		default:
			// Untouched entries are copied without recompression.
			if err := copyRaw(zw, f); err != nil {
				return nil, err
			}
		}
	}
	if !wroteProjectSettings {
		if err := writeEntry(zw, projectSettingsPath, newSettings); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}

	return &Result{
		Source:          insp,
		Printer:         opts.Printer.Name,
		Slots:           slots,
		Mapping:         mapping,
		SupportsEnabled: supports,
	}, nil
}

func sourceSupportsEnabled(settings map[string]any) bool {
	if v, ok := settings["enable_support"].(string); ok && v == "1" {
		return true
	}
	// Bambu Studio records user overrides in different_settings_to_system.
	if diff, ok := settings["different_settings_to_system"].([]any); ok {
		for _, d := range diff {
			if s, ok := d.(string); ok && strings.Contains(s, "enable_support") {
				return true
			}
		}
	}
	return false
}

func readAll(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func newDeflateEntry(zw *zip.Writer, name string) (io.Writer, error) {
	return zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
}

func writeEntry(zw *zip.Writer, name string, data []byte) error {
	w, err := newDeflateEntry(zw, name)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func transformEntry(zw *zip.Writer, f *zip.File, fn func(io.Reader, io.Writer) error) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	w, err := newDeflateEntry(zw, f.Name)
	if err != nil {
		return err
	}
	if err := fn(rc, w); err != nil {
		return fmt.Errorf("%s: %w", f.Name, err)
	}
	return nil
}

func copyRaw(zw *zip.Writer, f *zip.File) error {
	rc, err := f.OpenRaw()
	if err != nil {
		return err
	}
	header := f.FileHeader
	w, err := zw.CreateRaw(&header)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, rc)
	return err
}
