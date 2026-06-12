// Package moonraker queries a Klipper/Moonraker printer (such as the
// Snapmaker U1) for the filaments currently loaded in its extruders.
package moonraker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

// New accepts "ip", "ip:port" or a full http(s) URL; a bare host gets the
// default Moonraker port 7125.
func New(host, apiKey string) (*Client, error) {
	h := strings.TrimSpace(host)
	if h == "" {
		return nil, fmt.Errorf("moonraker: empty host")
	}
	if !strings.Contains(h, "://") {
		if !strings.Contains(h, ":") {
			h += ":7125"
		}
		h = "http://" + h
	}
	u, err := url.Parse(h)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("moonraker: invalid host %q", host)
	}
	return &Client{
		BaseURL: strings.TrimRight(u.String(), "/"),
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// ToolFilament is the filament state of one extruder/tool.
type ToolFilament struct {
	Tool     int    `json:"tool"`   // 0-based: extruder=0, extruder1=1, ...
	Object   string `json:"object"` // Moonraker object name
	Detected bool   `json:"detected"`
	Type     string `json:"type,omitempty"` // e.g. "PLA"
	SubType  string `json:"sub_type,omitempty"`
	Vendor   string `json:"vendor,omitempty"`
	Color    string `json:"color,omitempty"` // #RRGGBB
	// From the standard Klipper extruder object.
	Temperature float64 `json:"temperature"`
	Target      float64 `json:"target"`
	CanExtrude  bool    `json:"can_extrude"`
}

var extruderRe = regexp.MustCompile(`^extruder([0-9]*)$`)

// QueryFilaments asks the printer which extruder objects exist, queries
// them (GET /printer/objects/query?extruder&extruder1&...), and enriches
// the result with the per-channel RFID data from the filament_detect
// object when the firmware provides one (the Snapmaker U1 does; stock
// Klipper extruder objects carry no filament type/color).
func (c *Client) QueryFilaments(ctx context.Context) ([]ToolFilament, error) {
	available, err := c.listObjects(ctx)
	if err != nil {
		return nil, err
	}

	var extruders []string
	hasDetect := false
	for _, obj := range available {
		if extruderRe.MatchString(obj) {
			extruders = append(extruders, obj)
		}
		if obj == "filament_detect" {
			hasDetect = true
		}
	}
	if len(extruders) == 0 {
		return nil, fmt.Errorf("moonraker: printer reports no extruder objects")
	}
	sort.Slice(extruders, func(i, j int) bool { return toolIndex(extruders[i]) < toolIndex(extruders[j]) })

	query := extruders
	if hasDetect {
		query = append(append([]string{}, extruders...), "filament_detect")
	}
	status, err := c.queryObjects(ctx, query)
	if err != nil {
		return nil, err
	}

	tools := make([]ToolFilament, 0, len(extruders))
	for _, name := range extruders {
		tf := ToolFilament{Tool: toolIndex(name), Object: name}
		if obj, ok := status[name].(map[string]any); ok {
			parseExtruderObject(&tf, obj)
		}
		tools = append(tools, tf)
	}
	if detect, ok := status["filament_detect"].(map[string]any); ok {
		mergeFilamentDetect(tools, detect)
	}
	return tools, nil
}

func toolIndex(object string) int {
	m := extruderRe.FindStringSubmatch(object)
	if m == nil || m[1] == "" {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func (c *Client) listObjects(ctx context.Context) ([]string, error) {
	var res struct {
		Result struct {
			Objects []string `json:"objects"`
		} `json:"result"`
	}
	if err := c.get(ctx, "/printer/objects/list", &res); err != nil {
		return nil, err
	}
	return res.Result.Objects, nil
}

func (c *Client) queryObjects(ctx context.Context, objects []string) (map[string]any, error) {
	var res struct {
		Result struct {
			Status map[string]any `json:"status"`
		} `json:"result"`
	}
	if err := c.get(ctx, "/printer/objects/query?"+strings.Join(objects, "&"), &res); err != nil {
		return nil, err
	}
	return res.Result.Status, nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	if c.APIKey != "" {
		req.Header.Set("X-Api-Key", c.APIKey)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("moonraker: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("moonraker: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("moonraker: GET %s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("moonraker: GET %s: invalid JSON: %w", path, err)
	}
	return nil
}

// parseExtruderObject reads the standard Klipper fields plus, tolerantly,
// any filament fields a vendor firmware may have added to the extruder.
func parseExtruderObject(tf *ToolFilament, obj map[string]any) {
	tf.Temperature, _ = asFloat(obj["temperature"])
	tf.Target, _ = asFloat(obj["target"])
	tf.CanExtrude, _ = obj["can_extrude"].(bool)
	for _, key := range []string{"filament_type", "material"} {
		if s, ok := obj[key].(string); ok && s != "" {
			tf.Type = s
			tf.Detected = true
		}
	}
	for _, key := range []string{"filament_color", "filament_colour", "color"} {
		if c, ok := asColor(obj[key]); ok {
			tf.Color = c
			tf.Detected = true
		}
	}
}

// mergeFilamentDetect fills tool filaments from the Snapmaker U1 RFID data:
// result.status.filament_detect.info[channel] with MAIN_TYPE, SUB_TYPE,
// VENDOR and RGB_1 (24-bit integer color).
func mergeFilamentDetect(tools []ToolFilament, detect map[string]any) {
	info, ok := detect["info"].([]any)
	if !ok {
		return
	}
	for i := range tools {
		ch := tools[i].Tool
		if ch < 0 || ch >= len(info) {
			continue
		}
		entry, ok := info[ch].(map[string]any)
		if !ok {
			continue
		}
		if s, ok := entry["MAIN_TYPE"].(string); ok && s != "" {
			tools[i].Type = s
			tools[i].Detected = true
		}
		if s, ok := entry["SUB_TYPE"].(string); ok {
			tools[i].SubType = s
		}
		if s, ok := entry["VENDOR"].(string); ok {
			tools[i].Vendor = s
		}
		if c, ok := asColor(entry["RGB_1"]); ok {
			tools[i].Color = c
		}
	}
}

func asFloat(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok
}

// asColor accepts a 24-bit integer (filament_detect RGB_1) or a hex string.
func asColor(v any) (string, bool) {
	switch c := v.(type) {
	case float64:
		n := int64(c)
		if n < 0 || n > 0xFFFFFF {
			return "", false
		}
		return fmt.Sprintf("#%06X", n), true
	case string:
		s := strings.TrimPrefix(strings.TrimSpace(c), "#")
		if len(s) == 8 {
			s = s[:6]
		}
		if len(s) != 6 {
			return "", false
		}
		if _, err := strconv.ParseUint(s, 16, 32); err != nil {
			return "", false
		}
		return "#" + strings.ToUpper(s), true
	}
	return "", false
}
