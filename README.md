# bl2std

Convert Bambu Lab 3MF project files into standard 3MF projects for other
multi-filament printers — out of the box for the **Snapmaker U1** (slice the
result in [Snapmaker Orca](https://github.com/Snapmaker/OrcaSlicer)).

Inspired by [bl2u1](https://github.com/josuanbn/bl2u1), with two major
differences:

- **Many-to-one filament mapping.** A model painted with up to 16 filaments
  is not truncated: *every* source filament is mapped onto one of the target
  printer's slots (4 on the U1). Pick the 4 colors you want and the rest are
  folded into them — by explicit mapping or automatically by nearest color.
  The per-triangle paint data inside the 3D models is rewritten accordingly,
  so the multi-color paint job survives the conversion instead of pointing
  at filaments that no longer exist.
- **Printer profiles.** The target printer is a profile (model id, number of
  filament slots, baseline slicer settings, filament profile names). A
  Snapmaker U1 profile is built in; custom profiles are plain JSON files.

The whole logic lives in importable Go packages; the `bl2std` binary is a
thin CLI and HTTP API on top of them.

## Install

```sh
go install github.com/BryanSmee/bl_to_std/cmd/bl2std@latest
```

or from a checkout: `go build ./cmd/bl2std`.

## CLI

```sh
# What's inside the file?
bl2std inspect model.3mf

# Convert with defaults: Snapmaker U1, slots = the 4 most-used source
# filaments, every other filament mapped to the nearest color, supports
# kept as in the source project.
bl2std convert model.3mf

# Choose the 4 target colors yourself; all 16 source filaments are folded
# into them by nearest color:
bl2std convert model.3mf --colors "#000000,#FFFFFF,#D02020:PETG,#F0E040"

# Force specific source filaments onto specific slots (1-based), let the
# rest auto-map:
bl2std convert model.3mf --colors "#000000,#FFFFFF" --map "3=1,7=2"

# Other options
bl2std convert model.3mf -o out.3mf --supports on --printer snapmaker-u1 --json

# List printer profiles / start a custom one
bl2std printers
bl2std printers --export snapmaker-u1 > my-printer.json
bl2std convert model.3mf --printer my-printer.json
```

Each `--colors` entry is `#RRGGBB[:TYPE[:FILAMENT_PROFILE]]`; the material
type defaults to PLA and the slicer filament profile is derived from the
type via the printer profile.

## HTTP API

```sh
bl2std serve --addr :8080
```

| Endpoint | Description |
|---|---|
| `GET /api/v1/printers` | List available printer profiles. |
| `POST /api/v1/inspect` | Multipart upload (`file`): returns the source filaments as JSON. |
| `POST /api/v1/convert` | Multipart upload (`file`, optional `options` JSON field): returns the converted 3MF; the conversion report is in the `X-Bl2std-Report` header. |

```sh
curl -F file=@model.3mf localhost:8080/api/v1/inspect
curl -F file=@model.3mf \
     -F 'options={"slots":[{"color":"#000000"},{"color":"#FFFFFF","type":"PETG"}],"supports":"auto","mapping":{"5":1}}' \
     -o converted.3mf localhost:8080/api/v1/convert
```

The API is stateless: nothing is stored on the server.

## Library

```go
import (
    "github.com/BryanSmee/bl_to_std/pkg/converter"
    "github.com/BryanSmee/bl_to_std/pkg/printer"
)

insp, _ := converter.Inspect("model.3mf")          // list source filaments

res, err := converter.Convert("model.3mf", "out.3mf", converter.Options{
    Printer: printer.Builtin("snapmaker-u1"),
    Slots: []converter.Slot{
        {Color: "#000000", Type: "PLA"},
        {Color: "#FFFFFF", Type: "PLA"},
    },
    Mapping:  map[int]int{5: 1},        // optional explicit overrides
    Supports: converter.SupportsAuto,
})
// res.Mapping is the final source-filament -> slot assignment.
```

`converter.ConvertReader` / `converter.InspectReader` work on in-memory
archives. The low-level codec for the per-triangle paint data is exposed as
`pkg/paint`.

## Custom printer profiles

A profile is a JSON file (start from `bl2std printers --export snapmaker-u1`):

```jsonc
{
  "name": "my-printer",
  "display_name": "My Printer",
  "printer_model_id": "My Printer",   // written into slice_info.config
  "filament_slots": 4,                // number of filament slots
  "filament_profiles": {"PLA": "...", "PETG": "..."},
  "default_filament_profile": "...",
  "project_settings": { /* full project_settings.config of the target,
                           taken from a 3MF saved by the target's slicer */ }
}
```

`filament_slots` drives everything: how many slots sources are mapped onto,
the padding of the filament arrays, and the flush-volume matrix size.

## How it works

A Bambu Lab `.3mf` is a zip archive. The converter rewrites:

| File | Change |
|---|---|
| `Metadata/project_settings.config` | Replaced by the printer profile's baseline (e.g. Snapmaker U1, 0.20 Standard); filament color/type/profile arrays rebuilt for the chosen slots; support settings carried over from the source. |
| `Metadata/slice_info.config` | `printer_model_id` retargeted; filament list rebuilt with per-slot aggregated usage. |
| `Metadata/model_settings.config` | Per-object/per-part `extruder` assignments remapped through the filament mapping. |
| `3D/**/*.model` | Every `paint_color` (and `mmu_segmentation`) triangle attribute is decoded, its filament references remapped, and re-encoded. |

Everything else (geometry, thumbnails, auxiliary files) is copied
unchanged. XML is processed with streaming token rewriting
(`encoding/xml`), never with regexes, so unknown elements, attributes and
namespaces — including Bambu's proprietary extensions — survive untouched.
(A generic 3MF library such as go3mf was considered, but it cannot
round-trip Bambu's un-namespaced `paint_color` attributes, which are
exactly the data this tool must rewrite.)

The paint format is PrusaSlicer's `TriangleSelector` bitstream (Bambu
Studio and OrcaSlicer inherit it): a nibble-reversed hex string encoding a
recursive triangle-split tree whose leaves carry 1-based filament states.
The codec in `pkg/paint` is validated by round-tripping ~6000 paint strings
extracted from a real multi-color model, byte for byte.

## Credits & license

- [bl2u1](https://github.com/josuanbn/bl2u1) (GPL-3.0) — the original
  Bambu→U1 converter; the embedded Snapmaker U1 baseline settings derive
  from its template.
- [OrcaSlicer](https://github.com/SoftFever/OrcaSlicer) /
  [Snapmaker Orca](https://github.com/Snapmaker/OrcaSlicer) — target
  slicer and profile data.
- [PrusaSlicer](https://github.com/prusa3d/PrusaSlicer) /
  [Bambu Studio](https://github.com/bambulab/BambuStudio) — reference for
  the triangle paint serialization format.

Licensed under the [GPL-3.0](LICENSE).
