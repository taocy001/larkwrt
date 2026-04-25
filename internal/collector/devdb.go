package collector

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// DevRecord is the persistent profile of a device, keyed by MAC address.
type DevRecord struct {
	MAC       string    `json:"mac"`
	Hostname  string    `json:"hostname,omitempty"`
	Vendor    string    `json:"vendor,omitempty"`
	Type      string    `json:"type,omitempty"` // DeviceType string value
	Note      string    `json:"note,omitempty"` // user-supplied label
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	LastIP    string    `json:"last_ip,omitempty"`
	RandomMAC bool      `json:"random_mac,omitempty"`
}

// DevDB is a thread-safe, file-backed database of device records keyed by MAC.
type DevDB struct {
	path string
	mu   sync.RWMutex
	recs map[string]*DevRecord // lowercase MAC → record
}

func NewDevDB(path string) *DevDB {
	db := &DevDB{path: path, recs: make(map[string]*DevRecord)}
	if err := db.load(); err != nil && !os.IsNotExist(err) {
		log.Warn().Err(err).Str("path", path).Msg("devdb: load failed, starting fresh")
	}
	return db
}

func (db *DevDB) load() error {
	data, err := os.ReadFile(db.path)
	if err != nil {
		return err
	}
	var recs []*DevRecord
	if err := json.Unmarshal(data, &recs); err != nil {
		return err
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	for _, r := range recs {
		db.recs[strings.ToLower(r.MAC)] = r
	}
	log.Info().Int("count", len(db.recs)).Str("path", db.path).Msg("devdb: loaded")
	return nil
}

func (db *DevDB) save() {
	db.mu.RLock()
	recs := make([]*DevRecord, 0, len(db.recs))
	for _, r := range db.recs {
		recs = append(recs, r)
	}
	db.mu.RUnlock()

	data, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		log.Error().Err(err).Msg("devdb: marshal failed")
		return
	}
	if err := os.MkdirAll(filepath.Dir(db.path), 0755); err != nil {
		log.Error().Err(err).Msg("devdb: mkdir failed")
		return
	}
	tmp := db.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		log.Error().Err(err).Msg("devdb: write failed")
		return
	}
	if err := os.Rename(tmp, db.path); err != nil {
		log.Error().Err(err).Msg("devdb: rename failed")
	}
}

// UpsertDevices merges a fresh device scan into the database.
func (db *DevDB) UpsertDevices(devices []Device) {
	if len(devices) == 0 {
		return
	}
	now := time.Now()
	changed := false

	db.mu.Lock()
	for _, d := range devices {
		if d.MAC == "" {
			continue
		}
		mac := strings.ToLower(d.MAC)
		r, exists := db.recs[mac]
		if !exists {
			r = &DevRecord{MAC: mac, FirstSeen: now}
			db.recs[mac] = r
			changed = true
		}
		r.LastSeen = now
		if d.IP != "" {
			r.LastIP = d.IP
		}
		if d.Hostname != "" && len(d.Hostname) > len(r.Hostname) {
			r.Hostname = d.Hostname
			changed = true
		}
		if d.Vendor != "" && r.Vendor == "" {
			r.Vendor = d.Vendor
			changed = true
		}
		if d.Type != TypeUnknown && r.Type == "" {
			r.Type = string(d.Type)
			changed = true
		}
		if d.RandomMAC {
			r.RandomMAC = true
		}
	}
	db.mu.Unlock()

	if changed {
		go db.save()
	}
}

// SetNote sets a user note on the device with the given MAC or IP address.
// Returns the MAC found, or "" if not found.
func (db *DevDB) SetNote(macOrIP, note string) string {
	key := strings.ToLower(macOrIP)

	db.mu.Lock()
	defer db.mu.Unlock()

	// Try direct MAC lookup first
	if r, ok := db.recs[key]; ok {
		r.Note = note
		go db.save()
		return r.MAC
	}
	// Try IP lookup
	for _, r := range db.recs {
		if r.LastIP == macOrIP {
			r.Note = note
			go db.save()
			return r.MAC
		}
	}
	return ""
}

// Get returns the record for a MAC address (or empty record if not found).
func (db *DevDB) Get(mac string) (DevRecord, bool) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	r, ok := db.recs[strings.ToLower(mac)]
	if !ok {
		return DevRecord{}, false
	}
	return *r, true
}

// GetByIP returns the record whose LastIP matches.
func (db *DevDB) GetByIP(ip string) (DevRecord, bool) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	for _, r := range db.recs {
		if r.LastIP == ip {
			return *r, true
		}
	}
	return DevRecord{}, false
}

// All returns a snapshot of all records.
func (db *DevDB) All() []DevRecord {
	db.mu.RLock()
	defer db.mu.RUnlock()
	out := make([]DevRecord, 0, len(db.recs))
	for _, r := range db.recs {
		out = append(out, *r)
	}
	return out
}

// EnrichDevices fills the Note field (and upgrades Hostname/Type from DB history)
// for devices that have a MAC with a known DB record.
func (db *DevDB) EnrichDevices(devices []Device) []Device {
	db.mu.RLock()
	defer db.mu.RUnlock()
	out := make([]Device, len(devices))
	copy(out, devices)
	for i, d := range out {
		if d.MAC == "" {
			continue
		}
		r, ok := db.recs[strings.ToLower(d.MAC)]
		if !ok {
			continue
		}
		if r.Note != "" {
			out[i].Note = r.Note
		}
		// Prefer DB hostname if richer (e.g. learned from mDNS in previous run)
		if r.Hostname != "" && len(r.Hostname) > len(d.Hostname) {
			out[i].Hostname = r.Hostname
		}
		// Upgrade type from DB if current is unknown
		if out[i].Type == TypeUnknown && r.Type != "" {
			out[i].Type = DeviceType(r.Type)
		}
	}
	return out
}
