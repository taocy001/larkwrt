package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// SingBoxStatus holds state fetched from the sing-box Clash-compatible REST API.
// Fields are populated on a best-effort basis; missing endpoints leave zero values.
type SingBoxStatus struct {
	Version     string
	Groups      []SingBoxGroup
	Connections int
	UpTotal     int64
	DownTotal   int64
}

// SingBoxGroup is a Selector-type proxy group.
type SingBoxGroup struct {
	Name    string
	Current string   // currently selected node
	Options []string // all available nodes
}

// FetchSingBoxStatus queries the sing-box REST API and returns what it can.
// Errors on individual endpoints are silently ignored; the caller gets partial data.
func FetchSingBoxStatus(apiURL, secret string) *SingBoxStatus {
	c := &http.Client{Timeout: 5 * time.Second}
	s := &SingBoxStatus{}

	if resp, err := doGet(c, apiURL+"/version", secret); err == nil {
		var v struct {
			Version string `json:"version"`
		}
		json.NewDecoder(resp.Body).Decode(&v)
		resp.Body.Close()
		s.Version = v.Version
	}

	if resp, err := doGet(c, apiURL+"/proxies", secret); err == nil {
		var result struct {
			Proxies map[string]json.RawMessage `json:"proxies"`
		}
		if json.NewDecoder(resp.Body).Decode(&result) == nil {
			for name, raw := range result.Proxies {
				var p struct {
					Type string   `json:"type"`
					Now  string   `json:"now"`
					All  []string `json:"all"`
				}
				if json.Unmarshal(raw, &p) == nil && strings.EqualFold(p.Type, "Selector") {
					s.Groups = append(s.Groups, SingBoxGroup{
						Name:    name,
						Current: p.Now,
						Options: p.All,
					})
				}
			}
		}
		resp.Body.Close()
		sort.Slice(s.Groups, func(i, j int) bool { return s.Groups[i].Name < s.Groups[j].Name })
	}

	if resp, err := doGet(c, apiURL+"/connections", secret); err == nil {
		var result struct {
			Connections []json.RawMessage `json:"connections"`
			UpTotal     int64             `json:"uploadTotal"`
			DownTotal   int64             `json:"downloadTotal"`
		}
		if json.NewDecoder(resp.Body).Decode(&result) == nil {
			s.Connections = len(result.Connections)
			s.UpTotal = result.UpTotal
			s.DownTotal = result.DownTotal
		}
		resp.Body.Close()
	}

	return s
}

// SwitchSingBoxProxy sends PUT /proxies/{group} to change the selected node.
func SwitchSingBoxProxy(apiURL, secret, group, node string) error {
	c := &http.Client{Timeout: 5 * time.Second}
	body, _ := json.Marshal(map[string]string{"name": node})
	req, err := http.NewRequest(http.MethodPut, apiURL+"/proxies/"+group, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("切换失败: HTTP %d", resp.StatusCode)
	}
	return nil
}

func doGet(c *http.Client, url, secret string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	return c.Do(req)
}
