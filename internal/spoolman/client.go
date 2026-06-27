// Package spoolman queries a Spoolman server
// (https://github.com/Donkie/Spoolman) for the filament spools in inventory.
package spoolman

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// New accepts "host", "host:port" or a full URL; a bare host gets Spoolman's
// default port 7912.
func New(host string) (*Client, error) {
	h := strings.TrimSpace(host)
	if h == "" {
		return nil, fmt.Errorf("spoolman: empty host")
	}
	if !strings.Contains(h, "://") {
		if !strings.Contains(h, ":") {
			h += ":7912"
		}
		h = "http://" + h
	}
	u, err := url.Parse(h)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("spoolman: invalid host %q", host)
	}
	return &Client{
		BaseURL: strings.TrimRight(u.String(), "/"),
		HTTP:    &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// Vendor is the filament manufacturer.
type Vendor struct {
	Name string `json:"name"`
}

// Filament is the filament definition referenced by a spool.
type Filament struct {
	ID              int     `json:"id"`
	Name            string  `json:"name"`
	Material        string  `json:"material"`
	ColorHex        string  `json:"color_hex"`
	MultiColorHexes string  `json:"multi_color_hexes"`
	Vendor          *Vendor `json:"vendor"`
}

// Spool is one physical spool in inventory.
type Spool struct {
	ID              int      `json:"id"`
	Archived        bool     `json:"archived"`
	RemainingWeight float64  `json:"remaining_weight"`
	Location        string   `json:"location"`
	Filament        Filament `json:"filament"`
}

// ListOptions filters the spools returned by ListSpools.
type ListOptions struct {
	Location string // exact Spoolman location, empty = any
	IDs      []int  // restrict to these spool IDs (and preserve their order)
}

// ListSpools returns the non-archived spools, filtered by opts. When IDs are
// given the result follows that order; otherwise spools are ordered by ID.
func (c *Client) ListSpools(ctx context.Context, opts ListOptions) ([]Spool, error) {
	q := url.Values{}
	q.Set("allow_archived", "false")
	if opts.Location != "" {
		q.Set("location", opts.Location)
	}
	var spools []Spool
	if err := c.get(ctx, "/api/v1/spool?"+q.Encode(), &spools); err != nil {
		return nil, err
	}
	if len(opts.IDs) > 0 {
		return selectByID(spools, opts.IDs)
	}
	sortByID(spools)
	return spools, nil
}

func selectByID(spools []Spool, ids []int) ([]Spool, error) {
	byID := make(map[int]Spool, len(spools))
	for _, s := range spools {
		byID[s.ID] = s
	}
	out := make([]Spool, 0, len(ids))
	for _, id := range ids {
		s, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("spoolman: spool %d not found (or archived)", id)
		}
		out = append(out, s)
	}
	return out, nil
}

func sortByID(spools []Spool) {
	for i := 1; i < len(spools); i++ {
		for j := i; j > 0 && spools[j-1].ID > spools[j].ID; j-- {
			spools[j-1], spools[j] = spools[j], spools[j-1]
		}
	}
}

// Color returns the spool's primary color as #RRGGBB (first of a multi-color
// filament), or empty when unknown.
func (s Spool) Color() string {
	hex := s.Filament.ColorHex
	if hex == "" && s.Filament.MultiColorHexes != "" {
		hex = strings.SplitN(s.Filament.MultiColorHexes, ",", 2)[0]
	}
	hex = strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(hex) >= 6 {
		if _, err := strconv.ParseUint(hex[:6], 16, 32); err == nil {
			return "#" + strings.ToUpper(hex[:6])
		}
	}
	return ""
}

// VendorName returns the filament vendor, or empty.
func (s Spool) VendorName() string {
	if s.Filament.Vendor != nil {
		return s.Filament.Vendor.Name
	}
	return ""
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("spoolman: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("spoolman: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("spoolman: GET %s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("spoolman: GET %s: invalid JSON: %w", path, err)
	}
	return nil
}
