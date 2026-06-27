package converter

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strconv"
	"strings"

	"github.com/BryanSmee/bl_to_std/pkg/printer"
)

// batchEnv holds the shared inputs for emitting every batch file.
type batchEnv struct {
	zr           *zip.Reader
	meshByObject map[int]*objectMeshes
	allMeshPaths map[string]bool
	srcFil       map[int]Filament
	printer      *printer.Profile
	supports     bool
	source       map[string]any
}

// writeBatch produces one output 3MF containing only the batch's objects and
// its (renumbered) filaments.
func writeBatch(env batchEnv, b batch) ([]byte, error) {
	keep := make(map[int]bool, len(b.objectIDs))
	for _, id := range b.objectIDs {
		keep[id] = true
	}
	renum := make(map[int]int, len(b.colors))
	slots := make([]Slot, len(b.colors))
	for i, c := range b.colors {
		renum[c] = i + 1
		f := env.srcFil[c]
		color, typ := f.Color, f.Type
		if color == "" {
			color = "#FFFFFF"
		}
		if typ == "" {
			typ = "PLA"
		}
		slots[i] = Slot{Color: color, Type: typ, Profile: env.printer.FilamentProfile(typ)}
	}
	keptMesh := map[string]bool{}
	for id := range keep {
		if om := env.meshByObject[id]; om != nil {
			for _, mp := range om.meshPaths {
				keptMesh[mp] = true
			}
		}
	}

	newSettings, err := buildProjectSettings(env.printer, slots, len(slots), env.supports, env.source)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range env.zr.File {
		clean := normalizeMeshPath(f.Name)
		if strings.HasPrefix(clean, "..") {
			continue // zip-slip defence
		}
		switch {
		case f.Name == projectSettingsPath:
			if err := writeEntry(zw, f.Name, newSettings); err != nil {
				return nil, err
			}
		case f.Name == sliceInfoPath:
			if err := transformBatchEntry(zw, f, func(data []byte, w io.Writer) error {
				return rewriteSliceInfo(data, w, slots, renum, env.printer.ModelID)
			}); err != nil {
				return nil, err
			}
		case f.Name == modelSettingsPath:
			if err := transformBatchEntry(zw, f, func(data []byte, w io.Writer) error {
				return splitModelSettings(data, keep, renum, len(slots), w)
			}); err != nil {
				return nil, err
			}
		case f.Name == rootModelPath:
			if err := transformBatchEntry(zw, f, func(data []byte, w io.Writer) error {
				return splitRootModel(data, keep, renum, w)
			}); err != nil {
				return nil, err
			}
		case f.Name == rootModelRelsPath:
			if err := transformBatchEntry(zw, f, func(data []byte, w io.Writer) error {
				return filterRels(data, env.allMeshPaths, keptMesh, w)
			}); err != nil {
				return nil, err
			}
		case env.allMeshPaths[clean]:
			if keptMesh[clean] {
				if err := transformEntry(zw, f, func(rc io.Reader, w io.Writer) error {
					return rewriteModelPaint(rc, w, renum)
				}); err != nil {
					return nil, err
				}
			} // else: object not in this batch — drop its mesh
		default:
			if err := copyRaw(zw, f); err != nil {
				return nil, err
			}
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func transformBatchEntry(zw *zip.Writer, f *zip.File, fn func([]byte, io.Writer) error) error {
	data, err := readAll(f)
	if err != nil {
		return err
	}
	w, err := newDeflateEntry(zw, f.Name)
	if err != nil {
		return err
	}
	return fn(data, w)
}

// splitRootModel keeps only the batch's <object> resources and <build> items,
// remapping any inline triangle paint to the batch's renumbered filaments.
func splitRootModel(src []byte, keep map[int]bool, renum map[int]int, dst io.Writer) error {
	return streamFilter(src, dst, func(el *xml.StartElement) (skip bool, err error) {
		switch el.Name.Local {
		case "object":
			return !keep[attrInt(el, "id")], nil
		case "item":
			return !keep[attrInt(el, "objectid")], nil
		case "triangle":
			return false, remapPaintAttrs(el, renum)
		}
		return false, nil
	})
}

// splitModelSettings keeps only the batch's objects, plate instances and
// assemble items, and remaps extruder / filament_maps metadata.
func splitModelSettings(src []byte, keep map[int]bool, renum map[int]int, k int, dst io.Writer) error {
	dec := xml.NewDecoder(bytes.NewReader(src))
	out := newRawXMLWriter(dst)
	depth, skipping, skipDepth := 0, false, 0
	for {
		tok, err := dec.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if start, ok := tok.(xml.StartElement); ok && !skipping && start.Name.Local == "model_instance" {
			toks, err := bufferElement(dec, start)
			if err != nil {
				return err
			}
			if keep[modelInstanceObject(toks)] {
				for _, t := range toks {
					if err := out.writeToken(t); err != nil {
						return err
					}
				}
			}
			continue
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if skipping {
				depth++
				continue
			}
			if skip := skipModelSettings(&t, keep); skip {
				skipping, skipDepth = true, depth
				depth++
				continue
			}
			remapModelSettingsMeta(&t, renum, k)
			depth++
			if err := out.writeToken(t); err != nil {
				return err
			}
		case xml.EndElement:
			if skipping {
				depth--
				if depth == skipDepth {
					skipping = false
				}
				continue
			}
			depth--
			if err := out.writeToken(t); err != nil {
				return err
			}
		default:
			if skipping {
				continue
			}
			if err := out.writeToken(tok); err != nil {
				return err
			}
		}
	}
	return out.close()
}

func skipModelSettings(el *xml.StartElement, keep map[int]bool) bool {
	switch el.Name.Local {
	case "object":
		return !keep[attrInt(el, "id")]
	case "assemble_item":
		return !keep[attrInt(el, "object_id")]
	}
	return false
}

func remapModelSettingsMeta(el *xml.StartElement, renum map[int]int, k int) {
	if el.Name.Local != "metadata" {
		return
	}
	var key string
	for _, a := range el.Attr {
		if a.Name.Local == "key" {
			key = a.Value
		}
	}
	for i, a := range el.Attr {
		if a.Name.Local != "value" {
			continue
		}
		switch key {
		case "extruder":
			if v, err := strconv.Atoi(strings.TrimSpace(a.Value)); err == nil {
				if nv, ok := renum[v]; ok {
					el.Attr[i].Value = strconv.Itoa(nv)
				}
			}
		case "filament_maps":
			el.Attr[i].Value = strings.TrimSpace(strings.Repeat("1 ", k))
		}
	}
}

func modelInstanceObject(toks []xml.Token) int {
	for _, t := range toks {
		if s, ok := t.(xml.StartElement); ok && s.Name.Local == "metadata" {
			var key, val string
			for _, a := range s.Attr {
				if a.Name.Local == "key" {
					key = a.Value
				}
				if a.Name.Local == "value" {
					val = a.Value
				}
			}
			if key == "object_id" {
				n, _ := strconv.Atoi(strings.TrimSpace(val))
				return n
			}
		}
	}
	return -1
}

// filterRels drops relationships pointing at object meshes not in this batch.
func filterRels(src []byte, allMesh, keptMesh map[string]bool, dst io.Writer) error {
	return streamFilter(src, dst, func(el *xml.StartElement) (bool, error) {
		if el.Name.Local != "Relationship" {
			return false, nil
		}
		var target string
		for _, a := range el.Attr {
			if a.Name.Local == "Target" {
				target = normalizeMeshPath(a.Value)
			}
		}
		return allMesh[target] && !keptMesh[target], nil
	})
}

// streamFilter streams XML, letting decide skip a whole element subtree (by
// returning true on its StartElement) or edit it in place.
func streamFilter(src []byte, dst io.Writer, decide func(*xml.StartElement) (bool, error)) error {
	dec := xml.NewDecoder(bytes.NewReader(src))
	out := newRawXMLWriter(dst)
	depth, skipping, skipDepth := 0, false, 0
	for {
		tok, err := dec.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if skipping {
				depth++
				continue
			}
			skip, err := decide(&t)
			if err != nil {
				return err
			}
			if skip {
				skipping, skipDepth = true, depth
				depth++
				continue
			}
			depth++
			if err := out.writeToken(t); err != nil {
				return err
			}
		case xml.EndElement:
			if skipping {
				depth--
				if depth == skipDepth {
					skipping = false
				}
				continue
			}
			depth--
			if err := out.writeToken(t); err != nil {
				return err
			}
		default:
			if skipping {
				continue
			}
			if err := out.writeToken(tok); err != nil {
				return err
			}
		}
	}
	return out.close()
}

// bufferElement consumes an element's whole subtree (start..end) and returns
// the copied tokens.
func bufferElement(dec *xml.Decoder, start xml.StartElement) ([]xml.Token, error) {
	toks := []xml.Token{xml.CopyToken(start)}
	depth := 1
	for depth > 0 {
		tok, err := dec.RawToken()
		if err != nil {
			return nil, err
		}
		toks = append(toks, xml.CopyToken(tok))
		switch tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
	return toks, nil
}

func attrInt(el *xml.StartElement, name string) int {
	for _, a := range el.Attr {
		if a.Name.Local == name {
			n, _ := strconv.Atoi(strings.TrimSpace(a.Value))
			return n
		}
	}
	return -1
}
