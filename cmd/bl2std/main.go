// Command bl2std converts Bambu Lab 3MF project files to standard 3MF
// projects for other multi-filament printers (e.g. the Snapmaker U1),
// mapping any number of source filaments onto the target's filament slots
// while preserving multi-color paint data.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/BryanSmee/bl_to_std/internal/httpapi"
	"github.com/BryanSmee/bl_to_std/pkg/converter"
	"github.com/BryanSmee/bl_to_std/pkg/printer"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "inspect":
		err = cmdInspect(os.Args[2:])
	case "convert":
		err = cmdConvert(os.Args[2:])
	case "printers":
		err = cmdPrinters(os.Args[2:])
	case "serve":
		err = cmdServe(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `bl2std - convert Bambu Lab 3MF projects to standard 3MF (e.g. Snapmaker U1)

Usage:
  bl2std inspect <file.3mf> [--json]
  bl2std convert <file.3mf> [-o out.3mf] [flags]
  bl2std printers [--json]
  bl2std serve [--addr :8080]

Convert flags:
  -o <path>            output file (default: <input>-<printer>.3mf)
  --printer <name>     built-in profile name or path to a profile JSON
                       (default: snapmaker-u1)
  --colors <list>      target slot colors, comma separated, each
                       "#RRGGBB[:TYPE[:PROFILE]]", e.g.
                       "#FF0000,#00FF00:PETG,#000000,#FFFFFF"
                       (default: colors of the most-used source filaments)
  --map <list>         force source filaments onto slots, comma separated
                       "src=slot" pairs, e.g. "5=1,6=4". Unlisted source
                       filaments go to the slot with the nearest color.
  --supports <mode>    auto|on|off (default auto: keep the source setting)
  --json               print the conversion report as JSON
`)
}

// parseWithFile parses fs allowing flags before and after one positional
// file argument (Go's flag package alone stops at the first positional).
func parseWithFile(fs *flag.FlagSet, args []string, usage string) (string, error) {
	fs.Parse(args)
	rest := fs.Args()
	if len(rest) == 0 {
		return "", fmt.Errorf("usage: %s", usage)
	}
	file := rest[0]
	fs.Parse(rest[1:])
	if fs.NArg() != 0 {
		return "", fmt.Errorf("usage: %s", usage)
	}
	return file, nil
}

func cmdInspect(args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "output JSON")
	file, err := parseWithFile(fs, args, "bl2std inspect <file.3mf> [--json]")
	if err != nil {
		return err
	}
	insp, err := converter.Inspect(file)
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(insp)
	}
	if insp.PrinterModelID != "" {
		fmt.Printf("Printer model: %s\n", insp.PrinterModelID)
	}
	fmt.Printf("Plates: %d\nFilaments:\n", insp.Plates)
	for _, f := range insp.Filaments {
		fmt.Printf("  %2d  %s  %-8s  %.2f m / %.2f g\n", f.ID, f.Color, f.Type, f.UsedM, f.UsedG)
	}
	return nil
}

func cmdConvert(args []string) error {
	fs := flag.NewFlagSet("convert", flag.ExitOnError)
	out := fs.String("o", "", "output path")
	printerName := fs.String("printer", "snapmaker-u1", "printer profile")
	colors := fs.String("colors", "", "target slot colors")
	mapSpec := fs.String("map", "", "explicit source=slot mapping")
	supports := fs.String("supports", "auto", "supports: auto|on|off")
	asJSON := fs.Bool("json", false, "output JSON report")
	src, err := parseWithFile(fs, args, "bl2std convert <file.3mf> [flags]")
	if err != nil {
		return err
	}

	opts, err := buildConvertOptions(*printerName, *colors, *mapSpec, *supports)
	if err != nil {
		return err
	}
	dst := *out
	if dst == "" {
		dst = strings.TrimSuffix(src, ".3mf") + "-" + opts.Printer.Name + ".3mf"
	}

	res, err := converter.Convert(src, dst, opts)
	if err != nil {
		return err
	}

	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(struct {
			*converter.Result
			Output string `json:"output"`
		}{res, dst})
	}
	printConvertReport(res, opts.Printer, dst)
	return nil
}

// buildConvertOptions turns the convert flags into converter.Options.
func buildConvertOptions(printerName, colors, mapSpec, supports string) (converter.Options, error) {
	profile, err := printer.Resolve(printerName)
	if err != nil {
		return converter.Options{}, err
	}
	slots, err := parseSlots(colors)
	if err != nil {
		return converter.Options{}, err
	}
	mapping, err := parseMapping(mapSpec)
	if err != nil {
		return converter.Options{}, err
	}
	mode := converter.SupportMode(supports)
	switch mode {
	case converter.SupportsAuto, converter.SupportsOn, converter.SupportsOff:
	default:
		return converter.Options{}, fmt.Errorf("--supports must be auto, on or off")
	}
	return converter.Options{Printer: profile, Slots: slots, Mapping: mapping, Supports: mode}, nil
}

func printConvertReport(res *converter.Result, profile *printer.Profile, dst string) {
	fmt.Printf("Converted for %s -> %s\n", profile.DisplayName, dst)
	fmt.Println("Slots:")
	for i, s := range res.Slots {
		profileNote := s.Profile
		if profileNote == "" {
			profileNote = profile.FilamentProfile(s.Type)
		}
		fmt.Printf("  %d  %s  %-8s (%s)\n", i+1, s.Color, s.Type, profileNote)
	}
	fmt.Println("Filament mapping:")
	ids := make([]int, 0, len(res.Mapping))
	for id := range res.Mapping {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		var srcFil converter.Filament
		for _, f := range res.Source.Filaments {
			if f.ID == id {
				srcFil = f
			}
		}
		slot := res.Slots[res.Mapping[id]-1]
		fmt.Printf("  %2d %s %-8s -> slot %d %s %s\n", id, srcFil.Color, srcFil.Type, res.Mapping[id], slot.Color, slot.Type)
	}
	fmt.Printf("Supports: %v\n", onOff(res.SupportsEnabled))
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func parseSlots(spec string) ([]converter.Slot, error) {
	if spec == "" {
		return nil, nil
	}
	var slots []converter.Slot
	for _, part := range strings.Split(spec, ",") {
		fields := strings.SplitN(strings.TrimSpace(part), ":", 3)
		s := converter.Slot{Color: fields[0], Type: "PLA"}
		if len(fields) > 1 && fields[1] != "" {
			s.Type = fields[1]
		}
		if len(fields) > 2 {
			s.Profile = fields[2]
		}
		if !converter.ValidColor(s.Color) {
			return nil, fmt.Errorf("invalid color %q in --colors (want #RRGGBB)", fields[0])
		}
		slots = append(slots, s)
	}
	return slots, nil
}

func parseMapping(spec string) (map[int]int, error) {
	if spec == "" {
		return nil, nil
	}
	m := map[int]int{}
	for _, part := range strings.Split(spec, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("invalid --map entry %q (want src=slot)", part)
		}
		src, err1 := strconv.Atoi(kv[0])
		dst, err2 := strconv.Atoi(kv[1])
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("invalid --map entry %q (want src=slot)", part)
		}
		m[src] = dst
	}
	return m, nil
}

func cmdPrinters(args []string) error {
	fs := flag.NewFlagSet("printers", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "output JSON")
	export := fs.String("export", "", "dump a built-in profile as JSON (use as a template for custom profiles)")
	fs.Parse(args)
	if *export != "" {
		p := printer.Builtin(*export)
		if p == nil {
			return fmt.Errorf("unknown built-in profile %q", *export)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(p)
	}
	profiles := printer.Builtins()
	if *asJSON {
		type info struct {
			Name          string   `json:"name"`
			DisplayName   string   `json:"display_name"`
			FilamentSlots int      `json:"filament_slots"`
			MaterialTypes []string `json:"material_types"`
		}
		out := make([]info, 0, len(profiles))
		for _, p := range profiles {
			out = append(out, info{p.Name, p.DisplayName, p.FilamentSlots, p.MaterialTypes()})
		}
		return json.NewEncoder(os.Stdout).Encode(out)
	}
	for _, p := range profiles {
		fmt.Printf("%-14s %s (%d filament slots, materials: %s)\n",
			p.Name, p.DisplayName, p.FilamentSlots, strings.Join(p.MaterialTypes(), ", "))
	}
	fmt.Println("\nCustom profiles: pass --printer <path/to/profile.json> (see README).")
	return nil
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", ":8080", "listen address")
	maxUpload := fs.Int64("max-upload-mb", 200, "maximum upload size in MB")
	fs.Parse(args)
	fmt.Printf("bl2std API listening on %s\n", *addr)
	return httpapi.ListenAndServe(*addr, *maxUpload<<20)
}
