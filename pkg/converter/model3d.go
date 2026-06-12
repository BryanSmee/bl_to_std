package converter

import (
	"encoding/xml"
	"fmt"
	"io"

	"github.com/BryanSmee/bl_to_std/pkg/paint"
)

// paintAttrs are the per-triangle painting attributes that encode filament
// assignments: paint_color is written by Bambu Studio / OrcaSlicer,
// mmu_segmentation by PrusaSlicer. Both use the same bitstream format.
var paintAttrs = map[string]bool{
	"paint_color":      true,
	"mmu_segmentation": true,
}

// rewriteModelPaint streams a 3D/*.model file, remapping the filament
// numbers inside every painted triangle. This is what makes many-to-one
// filament mapping preserve the multi-color paint job instead of breaking
// it when filaments are renumbered.
func rewriteModelPaint(src io.Reader, dst io.Writer, mapping map[int]int) error {
	remap := func(filament int) int {
		if v, ok := mapping[filament]; ok {
			return v
		}
		return filament
	}
	return rewriteXML(src, dst, func(el *xml.StartElement) error {
		if el.Name.Local != "triangle" {
			return nil
		}
		for i, a := range el.Attr {
			if !paintAttrs[a.Name.Local] || a.Value == "" {
				continue
			}
			v, err := paint.Remap(a.Value, remap)
			if err != nil {
				return fmt.Errorf("triangle %s: %w", a.Name.Local, err)
			}
			el.Attr[i].Value = v
		}
		return nil
	})
}
