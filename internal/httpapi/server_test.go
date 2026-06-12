package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BryanSmee/bl_to_std/pkg/converter"
)

func fixture3MF(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string]string{
		"Metadata/slice_info.config": `<?xml version="1.0" encoding="UTF-8"?>
<config>
  <plate>
    <metadata key="printer_model_id" value="N2S"/>
    <filament id="1" type="PLA" color="#FF0000" used_m="1.00" used_g="3.00"/>
    <filament id="2" type="PLA" color="#00FF00" used_m="1.00" used_g="3.00"/>
  </plate>
</config>`,
		"Metadata/model_settings.config":   `<?xml version="1.0" encoding="UTF-8"?><config><object id="2"><metadata key="extruder" value="2"/></object></config>`,
		"Metadata/project_settings.config": `{"filament_colour":["#FF0000","#00FF00"],"filament_type":["PLA","PLA"],"enable_support":"0"}`,
		"3D/3dmodel.model":                 `<?xml version="1.0"?><model><resources/><build/></model>`,
	}
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(content))
	}
	zw.Close()
	return buf.Bytes()
}

func multipartBody(t *testing.T, file []byte, options string) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "test.3mf")
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(file)
	if options != "" {
		mw.WriteField("options", options)
	}
	mw.Close()
	return &body, mw.FormDataContentType()
}

func TestAPIInspectAndConvert(t *testing.T) {
	srv := httptest.NewServer(Handler(10 << 20))
	defer srv.Close()
	src := fixture3MF(t)

	// printers
	resp, err := http.Get(srv.URL + "/api/v1/printers")
	if err != nil {
		t.Fatal(err)
	}
	var printers []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&printers); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(printers) == 0 || printers[0]["name"] != "snapmaker-u1" {
		t.Fatalf("printers = %v", printers)
	}

	// inspect
	body, ctype := multipartBody(t, src, "")
	resp, err = http.Post(srv.URL+"/api/v1/inspect", ctype, body)
	if err != nil {
		t.Fatal(err)
	}
	var insp converter.Inspection
	if err := json.NewDecoder(resp.Body).Decode(&insp); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || len(insp.Filaments) != 2 {
		t.Fatalf("inspect: status %d, filaments %+v", resp.StatusCode, insp.Filaments)
	}

	// convert
	body, ctype = multipartBody(t, src, `{"slots":[{"color":"#112233","type":"PLA"}],"supports":"off"}`)
	resp, err = http.Post(srv.URL+"/api/v1/convert", ctype, body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("convert status %d", resp.StatusCode)
	}
	var report converter.Result
	if err := json.Unmarshal([]byte(resp.Header.Get("X-Bl2std-Report")), &report); err != nil {
		t.Fatalf("missing/invalid report header: %v", err)
	}
	if report.Mapping[1] != 1 || report.Mapping[2] != 1 {
		t.Errorf("mapping = %v, want everything on slot 1", report.Mapping)
	}
	var out bytes.Buffer
	out.ReadFrom(resp.Body)
	zr, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatalf("response is not a zip: %v", err)
	}
	found := false
	for _, f := range zr.File {
		if f.Name == "Metadata/project_settings.config" {
			found = true
		}
	}
	if !found {
		t.Error("converted archive missing project settings")
	}

	// bad upload
	body, ctype = multipartBody(t, []byte("not a zip"), "")
	resp, err = http.Post(srv.URL+"/api/v1/convert", ctype, body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad upload status = %d, want 400", resp.StatusCode)
	}
}
