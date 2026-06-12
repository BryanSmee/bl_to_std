package converter

import (
	"encoding/xml"
	"fmt"
	"io"

	"github.com/BryanSmee/bl_to_std/pkg/paint"
)

// paint_color is written by Bambu Studio / OrcaSlicer, mmu_segmentation by
// PrusaSlicer; both use the same bitstream format.
var paintAttrs = map[string]bool{
	"paint_color":      true,
	"mmu_segmentation": true,
}

// rewriteModelPaint remaps the filament numbers inside every painted
// triangle, so many-to-one mapping preserves the multi-color paint job
// instead of leaving references to filaments that no longer exist.
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
