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

type SupportMode string

const (
	SupportsAuto SupportMode = "auto" // keep the source project's setting
	SupportsOn   SupportMode = "on"
	SupportsOff  SupportMode = "off"
)

type Options struct {
	Printer *printer.Profile // defaults to the built-in Snapmaker U1
	// Slots define the target filaments; when empty they are auto-derived
	// from the most-used source filaments.
	Slots []Slot
	// Mapping forces source filament IDs onto slot numbers (1-based).
	// Unlisted source filaments go to the slot with the nearest color.
	Mapping  map[int]int
	Supports SupportMode
	// ExactSlots treats Slots as a candidate pool (e.g. a Spoolman
	// inventory) rather than fixed printer tools: the printer's slot-count
	// cap and white-PLA padding are skipped, and slots no source maps onto
	// are dropped, so the output defines exactly the filaments the model
	// uses — which may exceed the printer's tool count.
	ExactSlots bool
}

type Result struct {
	Source          *Inspection `json:"source"`
	Printer         string      `json:"printer"`
	Slots           []Slot      `json:"slots"`
	Mapping         map[int]int `json:"mapping"`
	SupportsEnabled bool        `json:"supports_enabled"`
}

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

type conversionPlan struct {
	printer         *printer.Profile
	slots           []Slot // as chosen/derived, reported in Result
	paddedSlots     []Slot // extended to slotCount
	slotCount       int    // number of filaments the output declares
	mapping         map[int]int
	projectSettings []byte
	supports        bool
}

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
	plan, err := buildPlan(zr, insp, &opts)
	if err != nil {
		return nil, err
	}
	if err := writeConvertedArchive(zr, w, plan); err != nil {
		return nil, err
	}

	return &Result{
		Source:          insp,
		Printer:         opts.Printer.Name,
		Slots:           plan.slots,
		Mapping:         plan.mapping,
		SupportsEnabled: plan.supports,
	}, nil
}

func buildPlan(zr *zip.Reader, insp *Inspection, opts *Options) (*conversionPlan, error) {
	slots, mapping, err := resolvePlan(insp, opts)
	if err != nil {
		return nil, err
	}

	slotCount := opts.Printer.FilamentSlots
	if opts.ExactSlots {
		slots, mapping = pruneUnusedSlots(slots, mapping)
		if len(slots) == 0 {
			return nil, fmt.Errorf("no target filaments after mapping")
		}
		slotCount = len(slots)
	}

	sourceSettings, err := readSourceSettings(zr)
	if err != nil {
		return nil, err
	}
	supports := opts.Supports == SupportsOn
	if opts.Supports == SupportsAuto {
		supports = sourceSupportsEnabled(sourceSettings)
	}
	paddedSlots := padSlots(slots, slotCount)
	newSettings, err := buildProjectSettings(opts.Printer, paddedSlots, slotCount, supports, sourceSettings)
	if err != nil {
		return nil, err
	}

	return &conversionPlan{
		printer:         opts.Printer,
		slots:           slots,
		paddedSlots:     paddedSlots,
		slotCount:       slotCount,
		mapping:         mapping,
		projectSettings: newSettings,
		supports:        supports,
	}, nil
}

// The output always declares every hardware slot; unused ones get white PLA.
func padSlots(slots []Slot, n int) []Slot {
	padded := make([]Slot, n)
	for i := range padded {
		if i < len(slots) {
			padded[i] = slots[i]
		} else {
			padded[i] = Slot{Color: "#FFFFFF", Type: "PLA"}
		}
	}
	return padded
}

func readSourceSettings(zr *zip.Reader) (map[string]any, error) {
	settings := map[string]any{}
	data, err := readZipFile(zr, projectSettingsPath)
	if err != nil {
		return nil, err
	}
	if data != nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return nil, fmt.Errorf("%s: %w", projectSettingsPath, err)
		}
	}
	return settings, nil
}

func writeConvertedArchive(zr *zip.Reader, w io.Writer, plan *conversionPlan) error {
	zw := zip.NewWriter(w)
	wroteProjectSettings := false
	for _, f := range zr.File {
		clean := path.Clean(f.Name)
		if strings.HasPrefix(clean, "..") || strings.HasPrefix(clean, "/") {
			continue // zip-slip defence
		}
		if f.Name == projectSettingsPath {
			wroteProjectSettings = true
		}
		if err := writeConvertedEntry(zw, f, plan); err != nil {
			return err
		}
	}
	if !wroteProjectSettings {
		if err := writeEntry(zw, projectSettingsPath, plan.projectSettings); err != nil {
			return err
		}
	}
	return zw.Close()
}

func writeConvertedEntry(zw *zip.Writer, f *zip.File, plan *conversionPlan) error {
	switch {
	case f.Name == projectSettingsPath:
		return writeEntry(zw, f.Name, plan.projectSettings)
	case f.Name == sliceInfoPath:
		data, err := readAll(f)
		if err != nil {
			return err
		}
		ew, err := newDeflateEntry(zw, f.Name)
		if err != nil {
			return err
		}
		if err := rewriteSliceInfo(data, ew, plan.paddedSlots, plan.mapping, plan.printer.ModelID); err != nil {
			return fmt.Errorf("%s: %w", f.Name, err)
		}
		return nil
	case f.Name == modelSettingsPath:
		return transformEntry(zw, f, func(rc io.Reader, ew io.Writer) error {
			return rewriteModelSettings(rc, ew, plan.mapping, plan.slotCount)
		})
	case strings.HasSuffix(f.Name, ".model"):
		return transformEntry(zw, f, func(rc io.Reader, ew io.Writer) error {
			return rewriteModelPaint(rc, ew, plan.mapping)
		})
	default:
		return copyRaw(zw, f) // untouched entries are copied without recompression
	}
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
