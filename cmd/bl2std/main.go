// Command bl2std converts Bambu Lab 3MF project files to standard 3MF
// projects for other multi-filament printers (e.g. the Snapmaker U1).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BryanSmee/bl_to_std/internal/config"
	"github.com/BryanSmee/bl_to_std/internal/httpapi"
	"github.com/BryanSmee/bl_to_std/internal/moonraker"
	"github.com/BryanSmee/bl_to_std/pkg/converter"
	"github.com/BryanSmee/bl_to_std/pkg/printer"
)

// setFlags returns the names of the flags explicitly set on the command line.
func setFlags(fs *flag.FlagSet) map[string]bool {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

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
	case "split":
		err = cmdSplit(os.Args[2:])
	case "filaments":
		err = cmdFilaments(os.Args[2:])
	case "config":
		err = cmdConfig(os.Args[2:])
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
  bl2std split <file.3mf> [-o outdir] [--printer P] [--supports mode] [--json]
  bl2std filaments [printer-ip[:port]] [--api-key K] [--json]
  bl2std config set [--printer P] [--ip IP] [--api-key K]
  bl2std config show | path | clear
  bl2std printers [--json]
  bl2std serve [--addr :8080]

Convert flags:
  -o <path>            output file (default: <input>-<printer>.3mf)
  --printer <name>     built-in profile name or path to a profile JSON
                       (default: saved config, else snapmaker-u1)
  --colors <list>      target slot colors, comma separated, each
                       "#RRGGBB[:TYPE[:PROFILE]]", e.g.
                       "#FF0000,#00FF00:PETG,#000000,#FFFFFF"
                       (default: colors of the most-used source filaments)
  --from-printer <ip>  use the filaments currently loaded in the printer
                       (queried over the Moonraker API) as the target slots;
                       mutually exclusive with --colors
  --api-key <key>      Moonraker API key, if the printer requires one
  --map <list>         force source filaments onto slots, comma separated
                       "src=slot" pairs, e.g. "5=1,6=4". Unlisted source
                       filaments go to the slot with the nearest color.
  --supports <mode>    auto|on|off (default auto: keep the source setting)
  --json               print the conversion report as JSON

Run "bl2std config set --ip <printer-ip>" once to use the printer without
passing its address every time: "filaments" and "convert" then fall back
to the saved IP (give --colors to convert without the printer).
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

func cmdSplit(args []string) error {
	fs := flag.NewFlagSet("split", flag.ExitOnError)
	outDir := fs.String("o", "", "output directory (default: <input>-plates)")
	printerName := fs.String("printer", "snapmaker-u1", "printer profile")
	supports := fs.String("supports", "auto", "supports: auto|on|off")
	asJSON := fs.Bool("json", false, "output JSON report")
	src, err := parseWithFile(fs, args, "bl2std split <file.3mf> [-o outdir] [flags]")
	if err != nil {
		return err
	}
	if !setFlags(fs)["printer"] {
		if cfg, _ := config.Load(); cfg.Printer != "" {
			*printerName = cfg.Printer
		}
	}
	profile, err := printer.Resolve(*printerName)
	if err != nil {
		return err
	}
	mode := converter.SupportMode(*supports)
	switch mode {
	case converter.SupportsAuto, converter.SupportsOn, converter.SupportsOff:
	default:
		return fmt.Errorf("--supports must be auto, on or off")
	}
	dir := *outDir
	if dir == "" {
		dir = strings.TrimSuffix(src, ".3mf") + "-plates"
	}

	res, err := converter.Split(src, dir, converter.SplitOptions{Printer: profile, Supports: mode})
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(res)
	}
	fmt.Printf("Split %s into %d file(s) in %s (each ≤%d colors):\n", src, len(res.Files), dir, profile.FilamentSlots)
	for _, f := range res.Files {
		fmt.Printf("  %s  objects %v  filaments %v\n", f.Name, f.ObjectIDs, f.Filaments)
	}
	return nil
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
	var cf convertFlags
	fs.StringVar(&cf.printer, "printer", "snapmaker-u1", "printer profile")
	fs.StringVar(&cf.colors, "colors", "", "target slot colors")
	fs.StringVar(&cf.fromPrinter, "from-printer", "", "printer IP to fetch loaded filaments from")
	fs.StringVar(&cf.apiKey, "api-key", "", "Moonraker API key")
	fs.StringVar(&cf.mapSpec, "map", "", "explicit source=slot mapping")
	fs.StringVar(&cf.supports, "supports", "auto", "supports: auto|on|off")
	asJSON := fs.Bool("json", false, "output JSON report")
	src, err := parseWithFile(fs, args, "bl2std convert <file.3mf> [flags]")
	if err != nil {
		return err
	}
	if err := applyConfigDefaults(&cf, setFlags(fs)); err != nil {
		return err
	}

	opts, emptySlots, err := buildConvertOptions(cf)
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
	warnMappingsToEmptyTools(res, emptySlots)
	return nil
}

func warnMappingsToEmptyTools(res *converter.Result, emptySlots []int) {
	empty := map[int]bool{}
	for _, s := range emptySlots {
		empty[s] = true
	}
	for src, slot := range res.Mapping {
		if empty[slot] {
			fmt.Fprintf(os.Stderr, "warning: source filament %d is mapped to slot %d, but that tool has no filament loaded; override with --map %d=<slot>\n", src, slot, src)
		}
	}
}

type convertFlags struct {
	printer     string
	colors      string
	fromPrinter string
	apiKey      string
	mapSpec     string
	supports    string
}

// applyConfigDefaults fills convert flags the user did not pass from the
// saved config: the printer profile, the API key, and — unless --colors was
// given — the printer IP, so a saved IP makes convert use the printer.
func applyConfigDefaults(cf *convertFlags, set map[string]bool) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if !set["printer"] && cfg.Printer != "" {
		cf.printer = cfg.Printer
	}
	if cf.apiKey == "" {
		cf.apiKey = cfg.APIKey
	}
	if cf.colors != "" && set["from-printer"] {
		return fmt.Errorf("--colors and --from-printer are mutually exclusive")
	}
	if cf.colors == "" && cf.fromPrinter == "" && cfg.PrinterIP != "" {
		cf.fromPrinter = cfg.PrinterIP
	}
	return nil
}

func buildConvertOptions(cf convertFlags) (converter.Options, []int, error) {
	profile, err := printer.Resolve(cf.printer)
	if err != nil {
		return converter.Options{}, nil, err
	}
	if cf.colors != "" && cf.fromPrinter != "" {
		return converter.Options{}, nil, fmt.Errorf("--colors and --from-printer are mutually exclusive")
	}
	slots, err := parseSlots(cf.colors)
	if err != nil {
		return converter.Options{}, nil, err
	}
	var emptySlots []int
	if cf.fromPrinter != "" {
		slots, emptySlots, err = slotsFromPrinter(cf.fromPrinter, cf.apiKey, profile)
		if err != nil {
			return converter.Options{}, nil, err
		}
	}
	mapping, err := parseMapping(cf.mapSpec)
	if err != nil {
		return converter.Options{}, nil, err
	}
	mode := converter.SupportMode(cf.supports)
	switch mode {
	case converter.SupportsAuto, converter.SupportsOn, converter.SupportsOff:
	default:
		return converter.Options{}, nil, fmt.Errorf("--supports must be auto, on or off")
	}
	return converter.Options{Printer: profile, Slots: slots, Mapping: mapping, Supports: mode}, emptySlots, nil
}

// slotsFromPrinter turns the loaded filaments into positional slots: slot N
// must stay tool N on the machine, so empty middle tools get a white PLA
// placeholder (returned as 1-based emptySlots) and only trailing empty
// tools are trimmed.
func slotsFromPrinter(host, apiKey string, profile *printer.Profile) ([]converter.Slot, []int, error) {
	tools, err := queryPrinterFilaments(host, apiKey)
	if err != nil {
		return nil, nil, err
	}
	if len(tools) > profile.FilamentSlots {
		tools = tools[:profile.FilamentSlots]
	}
	lastDetected := -1
	for i, tf := range tools {
		if tf.Detected {
			lastDetected = i
		}
	}
	if lastDetected < 0 {
		return nil, nil, fmt.Errorf("printer %s reports no loaded filaments; load filament or use --colors", host)
	}
	var slots []converter.Slot
	var emptySlots []int
	for i, tf := range tools[:lastDetected+1] {
		s := converter.Slot{
			Color:   tf.Color,
			Type:    tf.Type,
			Profile: profile.ResolveFilamentProfile(tf.Vendor, tf.Type, tf.SubType),
			Support: tf.Support,
		}
		if !tf.Detected {
			s = converter.Slot{Color: "#FFFFFF", Type: "PLA"}
			emptySlots = append(emptySlots, i+1)
		}
		if s.Color == "" {
			s.Color = "#FFFFFF"
		}
		if s.Type == "" {
			s.Type = "PLA"
		}
		slots = append(slots, s)
	}
	return slots, emptySlots, nil
}

func queryPrinterFilaments(host, apiKey string) ([]moonraker.ToolFilament, error) {
	client, err := moonraker.New(host, apiKey)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return client.QueryFilaments(ctx)
}

func cmdFilaments(args []string) error {
	fs := flag.NewFlagSet("filaments", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "output JSON")
	apiKey := fs.String("api-key", "", "Moonraker API key")
	fs.Parse(args)
	var host string
	if rest := fs.Args(); len(rest) > 0 {
		host = rest[0]
		fs.Parse(rest[1:])
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if host == "" {
		host = cfg.PrinterIP
	}
	key := *apiKey
	if key == "" {
		key = cfg.APIKey
	}
	if host == "" {
		return fmt.Errorf("no printer IP given and none saved; pass an IP or run: bl2std config set --ip <printer-ip>")
	}
	tools, err := queryPrinterFilaments(host, key)
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(tools)
	}
	fmt.Printf("Loaded filaments on %s:\n", host)
	for _, tf := range tools {
		state := "empty"
		if tf.Detected {
			state = strings.TrimSpace(fmt.Sprintf("%s  %s %s %s", tf.Color, tf.Vendor, tf.Type, tf.SubType))
			if tf.Support {
				state += "  (support)"
			}
		}
		fmt.Printf("  tool %d (%s): %-40s  %.0f°C / %.0f°C\n", tf.Tool+1, tf.Object, state, tf.Temperature, tf.Target)
	}
	return nil
}

func cmdConfig(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: bl2std config set|show|path|clear")
	}
	switch args[0] {
	case "set":
		return configSet(args[1:])
	case "show":
		return configShow()
	case "path":
		path, err := config.Path()
		if err != nil {
			return err
		}
		fmt.Println(path)
		return nil
	case "clear":
		if err := config.Clear(); err != nil {
			return err
		}
		fmt.Println("config cleared")
		return nil
	default:
		return fmt.Errorf("unknown config command %q (use set|show|path|clear)", args[0])
	}
}

func configSet(args []string) error {
	fs := flag.NewFlagSet("config set", flag.ExitOnError)
	printerName := fs.String("printer", "", "default printer profile")
	ip := fs.String("ip", "", "printer IP[:port]")
	apiKey := fs.String("api-key", "", "Moonraker API key")
	fs.Parse(args)
	set := setFlags(fs)
	if len(set) == 0 {
		return fmt.Errorf("nothing to set; pass --printer, --ip and/or --api-key (use \"\" to unset)")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if set["printer"] {
		if *printerName != "" {
			if _, err := printer.Resolve(*printerName); err != nil {
				return err
			}
		}
		cfg.Printer = *printerName
	}
	if set["ip"] {
		cfg.PrinterIP = *ip
	}
	if set["api-key"] {
		cfg.APIKey = *apiKey
	}
	if err := config.Save(cfg); err != nil {
		return err
	}
	path, _ := config.Path()
	fmt.Printf("saved to %s\n", path)
	return configShow()
}

func configShow() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	apiKey := "(unset)"
	if cfg.APIKey != "" {
		apiKey = "***"
	}
	fmt.Printf("printer: %s\nip:      %s\napi-key: %s\n",
		orUnset(cfg.Printer), orUnset(cfg.PrinterIP), apiKey)
	return nil
}

func orUnset(s string) string {
	if s == "" {
		return "(unset)"
	}
	return s
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
