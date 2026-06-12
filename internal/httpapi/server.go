// Package httpapi exposes the converter as a small stateless HTTP API.
//
//	GET  /api/v1/printers          list printer profiles
//	POST /api/v1/inspect           multipart form, field "file": report filaments
//	POST /api/v1/convert           multipart form, field "file" plus optional
//	                               field "options" (JSON, see convertOptions):
//	                               responds with the converted 3MF
package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/BryanSmee/bl_to_std/pkg/converter"
	"github.com/BryanSmee/bl_to_std/pkg/printer"
)

// convertOptions is the JSON body of the "options" form field.
type convertOptions struct {
	Printer  string           `json:"printer"` // built-in profile name, default "snapmaker-u1"
	Slots    []converter.Slot `json:"slots"`
	Mapping  map[string]int   `json:"mapping"` // source filament ID -> slot number
	Supports string           `json:"supports"`
}

func ListenAndServe(addr string, maxUpload int64) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           Handler(maxUpload),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv.ListenAndServe()
}

func Handler(maxUpload int64) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/printers", handlePrinters)
	mux.HandleFunc("POST /api/v1/inspect", withUpload(maxUpload, handleInspect))
	mux.HandleFunc("POST /api/v1/convert", withUpload(maxUpload, handleConvert))
	return mux
}

func jsonError(w http.ResponseWriter, status int, format string, args ...any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf(format, args...)})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("httpapi: write response: %v", err)
	}
}

func handlePrinters(w http.ResponseWriter, _ *http.Request) {
	type info struct {
		Name             string            `json:"name"`
		DisplayName      string            `json:"display_name"`
		FilamentSlots    int               `json:"filament_slots"`
		FilamentProfiles map[string]string `json:"filament_profiles"`
	}
	var out []info
	for _, p := range printer.Builtins() {
		out = append(out, info{p.Name, p.DisplayName, p.FilamentSlots, p.FilamentProfiles})
	}
	writeJSON(w, out)
}

func withUpload(maxUpload int64, fn func(http.ResponseWriter, *http.Request, []byte, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
		f, hdr, err := r.FormFile("file")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "missing or oversized multipart field %q: %v", "file", err)
			return
		}
		defer f.Close()
		data, err := io.ReadAll(f)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "reading upload: %v", err)
			return
		}
		if !bytes.HasPrefix(data, []byte("PK\x03\x04")) {
			jsonError(w, http.StatusBadRequest, "uploaded file is not a 3MF/zip archive")
			return
		}
		fn(w, r, data, hdr.Filename)
	}
}

func handleInspect(w http.ResponseWriter, _ *http.Request, data []byte, _ string) {
	insp, err := converter.InspectReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		jsonError(w, http.StatusUnprocessableEntity, "%v", err)
		return
	}
	writeJSON(w, insp)
}

func handleConvert(w http.ResponseWriter, r *http.Request, data []byte, filename string) {
	opts, err := convertOptionsFromRequest(r)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "%v", err)
		return
	}

	var out bytes.Buffer
	res, err := converter.ConvertReader(bytes.NewReader(data), int64(len(data)), &out, opts)
	if err != nil {
		jsonError(w, http.StatusUnprocessableEntity, "%v", err)
		return
	}
	sendConvertedFile(w, &out, res, filename, opts.Printer.Name)
}

func convertOptionsFromRequest(r *http.Request) (converter.Options, error) {
	var reqOpts convertOptions
	if raw := r.FormValue("options"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &reqOpts); err != nil {
			return converter.Options{}, fmt.Errorf("invalid options JSON: %w", err)
		}
	}
	if reqOpts.Printer == "" {
		reqOpts.Printer = "snapmaker-u1"
	}
	profile := printer.Builtin(reqOpts.Printer)
	if profile == nil {
		return converter.Options{}, fmt.Errorf("unknown printer profile %q", reqOpts.Printer)
	}
	for _, s := range reqOpts.Slots {
		if !converter.ValidColor(s.Color) {
			return converter.Options{}, fmt.Errorf("invalid slot color %q", s.Color)
		}
	}
	mapping := map[int]int{}
	for k, v := range reqOpts.Mapping {
		var src int
		if _, err := fmt.Sscanf(k, "%d", &src); err != nil {
			return converter.Options{}, fmt.Errorf("invalid mapping key %q", k)
		}
		mapping[src] = v
	}
	supports := converter.SupportMode(reqOpts.Supports)
	if supports == "" {
		supports = converter.SupportsAuto
	}
	switch supports {
	case converter.SupportsAuto, converter.SupportsOn, converter.SupportsOff:
	default:
		return converter.Options{}, fmt.Errorf("supports must be auto, on or off")
	}
	return converter.Options{Printer: profile, Slots: reqOpts.Slots, Mapping: mapping, Supports: supports}, nil
}

func sendConvertedFile(w http.ResponseWriter, out *bytes.Buffer, res *converter.Result, filename, profileName string) {
	if report, err := json.Marshal(res); err == nil {
		w.Header().Set("X-Bl2std-Report", string(report))
	}
	base := strings.TrimSuffix(path.Base(filename), ".3mf")
	if base == "" || base == "." {
		base = "converted"
	}
	name := base + "-" + profileName + ".3mf"
	w.Header().Set("Content-Type", "model/3mf")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Content-Length", fmt.Sprint(out.Len()))
	if _, err := io.Copy(w, out); err != nil {
		log.Printf("httpapi: send converted file: %v", err)
	}
}
