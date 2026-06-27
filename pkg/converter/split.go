package converter

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BryanSmee/bl_to_std/pkg/printer"
)

const rootModelPath = "3D/3dmodel.model"
const rootModelRelsPath = "3D/_rels/3dmodel.model.rels"

// SplitOptions configures splitting a >N-color model into printable batches.
type SplitOptions struct {
	Printer  *printer.Profile // defaults to the built-in Snapmaker U1
	Supports SupportMode
	// MaxColors caps the colors per output file; 0 uses the printer's slots.
	MaxColors int
}

// SplitFile is one output 3MF covering a subset of the model's objects.
type SplitFile struct {
	Name      string `json:"name"`
	Data      []byte `json:"-"`
	ObjectIDs []int  `json:"object_ids"`
	Filaments []int  `json:"filaments"` // source filament numbers used
}

// SplitResult reports the batches a model was split into.
type SplitResult struct {
	Source *Inspection `json:"source"`
	Files  []SplitFile `json:"files"`
}

// objectColors is the set of source filament numbers a built object uses.
type objectColors struct {
	id     int
	colors []int // sorted, unique
}

// Split reads the model at srcPath and writes one 3MF per color batch into
// dstDir, named "<base>-plate<n>.3mf". It errors (listing the objects) if any
// single object uses more colors than the printer can hold.
func Split(srcPath, dstDir string, opts SplitOptions) (*SplitResult, error) {
	f, err := os.Open(srcPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	base := strings.TrimSuffix(filepath.Base(srcPath), ".3mf")
	res, err := SplitReader(f, st.Size(), base, opts)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return nil, err
	}
	for i, b := range res.Files {
		p := filepath.Join(dstDir, b.Name)
		if err := os.WriteFile(p, b.Data, 0o644); err != nil {
			return nil, err
		}
		res.Files[i].Data = nil // free after writing
	}
	return res, nil
}

// SplitReader splits an in-memory 3MF, returning the batch files.
func SplitReader(r io.ReaderAt, size int64, base string, opts SplitOptions) (*SplitResult, error) {
	if opts.Printer == nil {
		opts.Printer = printer.Builtin("snapmaker-u1")
	}
	if opts.Supports == "" {
		opts.Supports = SupportsAuto
	}
	maxColors := opts.MaxColors
	if maxColors == 0 {
		maxColors = opts.Printer.FilamentSlots
	}

	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("not a valid 3MF/zip archive: %w", err)
	}
	insp, err := inspectZip(zr)
	if err != nil {
		return nil, err
	}
	srcFil := map[int]Filament{}
	for _, fil := range insp.Filaments {
		srcFil[fil.ID] = fil
	}

	meshByObject, allMeshPaths, err := analyzeObjects(zr)
	if err != nil {
		return nil, err
	}
	objects, err := objectColorSets(zr, meshByObject)
	if err != nil {
		return nil, err
	}
	if len(objects) == 0 {
		return nil, fmt.Errorf("no printable objects found in the model")
	}
	if err := checkColorCounts(objects, maxColors); err != nil {
		return nil, err
	}

	batches := groupByColor(objects, maxColors)
	sourceSettings, err := readSourceSettings(zr)
	if err != nil {
		return nil, err
	}
	supports := opts.Supports == SupportsOn
	if opts.Supports == SupportsAuto {
		supports = sourceSupportsEnabled(sourceSettings)
	}

	env := batchEnv{
		zr:           zr,
		meshByObject: meshByObject,
		allMeshPaths: allMeshPaths,
		srcFil:       srcFil,
		printer:      opts.Printer,
		supports:     supports,
		source:       sourceSettings,
	}
	out := &SplitResult{Source: insp}
	for i, b := range batches {
		name := fmt.Sprintf("%s-plate%d.3mf", base, i+1)
		data, err := writeBatch(env, b)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out.Files = append(out.Files, SplitFile{
			Name: name, Data: data, ObjectIDs: b.objectIDs, Filaments: b.colors,
		})
	}
	return out, nil
}

func checkColorCounts(objects []objectColors, max int) error {
	var bad []string
	for _, o := range objects {
		if len(o.colors) > max {
			bad = append(bad, fmt.Sprintf("object %d uses %d colors %v", o.id, len(o.colors), o.colors))
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("cannot split: %d object(s) individually exceed %d colors and cannot print on this printer:\n  %s",
			len(bad), max, strings.Join(bad, "\n  "))
	}
	return nil
}

// batch is one group of objects whose combined colors fit in maxColors.
type batch struct {
	objectIDs []int
	colors    []int // sorted source filament numbers
}

// groupByColor packs objects (largest palette first) into batches whose
// combined color count stays within max — first-fit-decreasing.
func groupByColor(objects []objectColors, max int) []batch {
	ordered := append([]objectColors(nil), objects...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if len(ordered[i].colors) != len(ordered[j].colors) {
			return len(ordered[i].colors) > len(ordered[j].colors)
		}
		return ordered[i].id < ordered[j].id
	})

	var batches []batch
	for _, o := range ordered {
		placed := false
		for bi := range batches {
			if union := mergeColors(batches[bi].colors, o.colors); len(union) <= max {
				batches[bi].colors = union
				batches[bi].objectIDs = append(batches[bi].objectIDs, o.id)
				placed = true
				break
			}
		}
		if !placed {
			batches = append(batches, batch{
				objectIDs: []int{o.id},
				colors:    append([]int(nil), o.colors...),
			})
		}
	}
	for bi := range batches {
		sort.Ints(batches[bi].objectIDs)
	}
	return batches
}

func mergeColors(a, b []int) []int {
	seen := map[int]bool{}
	for _, c := range a {
		seen[c] = true
	}
	for _, c := range b {
		seen[c] = true
	}
	out := make([]int, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Ints(out)
	return out
}
