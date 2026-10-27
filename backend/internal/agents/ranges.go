package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"strings"
	"sync"
	"time"

	"hatamoto.dev/core/internal/database"
)

// RangePersister stores fetched range lists so they survive a restart.
type RangePersister interface {
	// LoadRanges returns every persisted list keyed by registry range key.
	LoadRanges(ctx context.Context) (map[string][]netip.Prefix, error)
	SaveRanges(ctx context.Context, key string, prefixes []netip.Prefix, sourceURLs []string, fetchedAt time.Time) error
}

// RangeStore holds the operators' published IP ranges in memory. A key that
// was never loaded is distinct from one that is loaded and does not match: the
// verifier must not call a hit spoofed because a download has not succeeded yet.
type RangeStore struct {
	mu sync.RWMutex
	m  map[string][]netip.Prefix
}

func NewRangeStore() *RangeStore {
	return &RangeStore{m: map[string][]netip.Prefix{}}
}

// Load fills the store from persisted lists. Existing keys are overwritten.
func (s *RangeStore) Load(ctx context.Context, p RangePersister) error {
	all, err := p.LoadRanges(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	maps.Copy(s.m, all)
	return nil
}

// Set replaces one key's prefixes.
func (s *RangeStore) Set(key string, prefixes []netip.Prefix) {
	s.mu.Lock()
	s.m[key] = prefixes
	s.mu.Unlock()
}

// Has reports whether key has ever been loaded.
func (s *RangeStore) Has(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.m[key]
	return ok
}

// Contains reports whether ip is inside any of the keys' prefixes (found), and
// whether at least one of the keys has been loaded (loaded). ip is unmapped.
func (s *RangeStore) Contains(keys []string, ip netip.Addr) (found, loaded bool) {
	ip = ip.Unmap()
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, k := range keys {
		prefixes, ok := s.m[k]
		if !ok {
			continue
		}
		loaded = true
		for _, p := range prefixes {
			if p.Contains(ip) {
				return true, true
			}
		}
	}
	return false, loaded
}

// ParsePrefixes reads an operator range document. Accepted shapes:
//   - googlebot style: {"prefixes":[{"ipv4Prefix":"..."}|{"ipv6Prefix":"..."}]}
//   - {"ips":[ "1.2.3.0/24" | {"ip_address":"..."} ... ]} (Ahrefs-like)
//   - a plain JSON array of strings
//
// Bare addresses become /32 or /128. Unparseable entries are skipped; a
// document with no usable prefix is an error so the caller keeps old data.
func ParsePrefixes(body []byte) ([]netip.Prefix, error) {
	var raw []string

	var list []json.RawMessage
	var doc struct {
		Prefixes []struct {
			IPv4 string `json:"ipv4Prefix"`
			IPv6 string `json:"ipv6Prefix"`
		} `json:"prefixes"`
		IPs []json.RawMessage `json:"ips"`
	}
	if err := json.Unmarshal(body, &list); err == nil {
		raw = rangeStrings(list)
	} else if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("agents: parse ranges: %w", err)
	} else {
		for _, p := range doc.Prefixes {
			if p.IPv4 != "" {
				raw = append(raw, p.IPv4)
			} else if p.IPv6 != "" {
				raw = append(raw, p.IPv6)
			}
		}
		raw = append(raw, rangeStrings(doc.IPs)...)
	}

	out := make([]netip.Prefix, 0, len(raw))
	for _, s := range raw {
		if p, ok := parsePrefix(s); ok {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("agents: no usable prefixes in range document")
	}
	return out, nil
}

// rangeStrings extracts the address strings from string or {"ip_address"|"ip"|"prefix"} items.
func rangeStrings(items []json.RawMessage) []string {
	var out []string
	for _, it := range items {
		var s string
		if json.Unmarshal(it, &s) == nil {
			out = append(out, s)
			continue
		}
		var obj struct {
			IPAddress string `json:"ip_address"`
			IP        string `json:"ip"`
			Prefix    string `json:"prefix"`
		}
		if json.Unmarshal(it, &obj) != nil {
			continue
		}
		for _, v := range []string{obj.IPAddress, obj.IP, obj.Prefix} {
			if v != "" {
				out = append(out, v)
				break
			}
		}
	}
	return out
}

func parsePrefix(s string) (netip.Prefix, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return netip.Prefix{}, false
	}
	if !strings.Contains(s, "/") {
		a, err := netip.ParseAddr(s)
		if err != nil {
			return netip.Prefix{}, false
		}
		a = a.Unmap()
		return netip.PrefixFrom(a, a.BitLen()), true
	}
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return netip.Prefix{}, false
	}
	// A v4-mapped v6 prefix (::ffff:a.b.c.d/N, N>=96) is the v4 prefix /N-96.
	if a := p.Addr(); a.Is4In6() && p.Bits() >= 96 {
		p = netip.PrefixFrom(a.Unmap(), p.Bits()-96)
	}
	return p.Masked(), true
}

// RangeRepository is the Postgres RangePersister.
type RangeRepository struct {
	db *database.DB
}

func NewRangeRepository(db *database.DB) *RangeRepository { return &RangeRepository{db: db} }

func (r *RangeRepository) LoadRanges(ctx context.Context) (map[string][]netip.Prefix, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `SELECT key, prefixes FROM hatamoto_agent_ip_ranges`)
	if err != nil {
		return nil, fmt.Errorf("load ranges: %w", err)
	}
	defer rows.Close()
	out := map[string][]netip.Prefix{}
	for rows.Next() {
		var key string
		var texts []string
		if err := rows.Scan(&key, &texts); err != nil {
			return nil, fmt.Errorf("scan ranges: %w", err)
		}
		prefixes := make([]netip.Prefix, 0, len(texts))
		for _, t := range texts {
			if p, err := netip.ParsePrefix(t); err == nil {
				prefixes = append(prefixes, p)
			}
		}
		if len(prefixes) > 0 {
			out[key] = prefixes
		}
	}
	return out, rows.Err()
}

func (r *RangeRepository) SaveRanges(ctx context.Context, key string, prefixes []netip.Prefix, sourceURLs []string, fetchedAt time.Time) error {
	texts := make([]string, len(prefixes))
	for i, p := range prefixes {
		texts[i] = p.String()
	}
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	_, err = conn.Exec(ctx, `INSERT INTO hatamoto_agent_ip_ranges (key, prefixes, fetched_at, source_urls)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (key) DO UPDATE SET prefixes = EXCLUDED.prefixes,
			fetched_at = EXCLUDED.fetched_at, source_urls = EXCLUDED.source_urls`,
		key, texts, fetchedAt, sourceURLs)
	if err != nil {
		return fmt.Errorf("save ranges %s: %w", key, err)
	}
	return nil
}

// Size is the number of range keys loaded.
func (s *RangeStore) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.m)
}
