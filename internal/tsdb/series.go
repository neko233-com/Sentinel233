package tsdb

import (
	"hash/fnv"
	"sort"
	"strings"
	"sync"
	"time"
)

type Sample struct {
	Timestamp int64
	Value     float64
}

type Label struct {
	Name  string
	Value string
}

type Labels []Label

func (l Labels) Get(name string) string {
	for _, lb := range l {
		if lb.Name == name {
			return lb.Value
		}
	}
	return ""
}

func (l Labels) String() string {
	parts := make([]string, len(l))
	for i, lb := range l {
		parts[i] = lb.Name + "=" + lb.Value
	}
	sort.Strings(parts)
	return "{" + strings.Join(parts, ",") + "}"
}

func (l Labels) Hash() uint64 {
	h := fnv.New64a()
	for _, lb := range l {
		h.Write([]byte(lb.Name))
		h.Write([]byte{0})
		h.Write([]byte(lb.Value))
		h.Write([]byte{0})
	}
	return h.Sum64()
}

type Series struct {
	Labels  Labels
	samples []Sample
	mu      sync.RWMutex
}

func NewSeries(labels Labels) *Series {
	return &Series{
		Labels:  labels,
		samples: make([]Sample, 0, 256),
	}
}

func (s *Series) Append(ts int64, v float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.samples = append(s.samples, Sample{Timestamp: ts, Value: v})
}

func (s *Series) Samples() []Sample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make([]Sample, len(s.samples))
	copy(cp, s.samples)
	return cp
}

func (s *Series) Range(mint, maxt int64) []Sample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []Sample
	for _, sp := range s.samples {
		if sp.Timestamp >= mint && sp.Timestamp <= maxt {
			result = append(result, sp)
		}
	}
	return result
}

func (s *Series) Latest() (Sample, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.samples) == 0 {
		return Sample{}, false
	}
	return s.samples[len(s.samples)-1], true
}

func (s *Series) TrimBefore(ts int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := sort.Search(len(s.samples), func(i int) bool {
		return s.samples[i].Timestamp >= ts
	})
	s.samples = s.samples[i:]
}

type DB struct {
	series map[uint64]*Series
	mu     sync.RWMutex

	retention  time.Duration
	dataDir    string
	wal        *WAL
	stopCh     chan struct{}
}

type DBConfig struct {
	DataDir       string
	Retention     time.Duration
	FlushInterval time.Duration
}

func OpenDB(cfg DBConfig) (*DB, error) {
	wal, err := NewWAL(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	db := &DB{
		series:    make(map[uint64]*Series),
		retention: cfg.Retention,
		dataDir:   cfg.DataDir,
		wal:       wal,
		stopCh:    make(chan struct{}),
	}
	if err := db.replayWAL(); err != nil {
		return nil, err
	}
	go db.runCompaction(cfg.FlushInterval)
	return db, nil
}

func (db *DB) replayWAL() error {
	entries, err := db.wal.ReadAll()
	if err != nil {
		return err
	}
	for _, e := range entries {
		s := db.getOrCreateSeries(e.Labels)
		s.Append(e.Timestamp, e.Value)
	}
	return nil
}

func (db *DB) getOrCreateSeries(labels Labels) *Series {
	h := labels.Hash()
	if s, ok := db.series[h]; ok {
		return s
	}
	s := NewSeries(labels)
	db.series[h] = s
	return s
}

func (db *DB) Append(labels Labels, ts int64, v float64) error {
	if err := db.wal.Write(WALEntry{Labels: labels, Timestamp: ts, Value: v}); err != nil {
		return err
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	s := db.getOrCreateSeries(labels)
	s.Append(ts, v)
	return nil
}

func (db *DB) Query(labels Labels, mint, maxt int64) []Sample {
	db.mu.RLock()
	defer db.mu.RUnlock()
	h := labels.Hash()
	s, ok := db.series[h]
	if !ok {
		return nil
	}
	return s.Range(mint, maxt)
}

func (db *DB) QueryByMatcher(matcher LabelMatcher, mint, maxt int64) map[uint64]*Series {
	db.mu.RLock()
	defer db.mu.RUnlock()
	result := make(map[uint64]*Series)
	for hash, s := range db.series {
		if matcher.Matches(s.Labels) {
			result[hash] = s
		}
	}
	return result
}

func (db *DB) AllSeries() []*Series {
	db.mu.RLock()
	defer db.mu.RUnlock()
	result := make([]*Series, 0, len(db.series))
	for _, s := range db.series {
		result = append(result, s)
	}
	return result
}

func (db *DB) SeriesCount() int {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return len(db.series)
}

func (db *DB) TotalSamples() int {
	db.mu.RLock()
	defer db.mu.RUnlock()
	total := 0
	for _, s := range db.series {
		total += len(s.Samples())
	}
	return total
}

func (db *DB) runCompaction(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			db.compact()
		case <-db.stopCh:
			return
		}
	}
}

func (db *DB) compact() {
	cutoff := time.Now().Add(-db.retention).UnixMilli()
	db.mu.RLock()
	series := make([]*Series, 0, len(db.series))
	for _, s := range db.series {
		series = append(series, s)
	}
	db.mu.RUnlock()
	for _, s := range series {
		s.TrimBefore(cutoff)
	}
	if err := db.wal.Truncate(); err != nil {
		_ = err
	}
}

func (db *DB) Close() error {
	close(db.stopCh)
	return db.wal.Close()
}

type LabelMatcher interface {
	Matches(labels Labels) bool
}

type EqualMatcher struct {
	Name, Value string
}

func (m EqualMatcher) Matches(labels Labels) bool {
	return labels.Get(m.Name) == m.Value
}

type NotEqualMatcher struct {
	Name, Value string
}

func (m NotEqualMatcher) Matches(labels Labels) bool {
	return labels.Get(m.Name) != m.Value
}

type RegexMatcher struct {
	Name  string
	Regex *Regexp
}

type Regexp struct {
	Pattern string
}

func (m RegexMatcher) Matches(labels Labels) bool {
	v := labels.Get(m.Name)
	return strings.Contains(v, m.Regex.Pattern)
}

type NotRegexMatcher struct {
	Name  string
	Regex *Regexp
}

func (m NotRegexMatcher) Matches(labels Labels) bool {
	v := labels.Get(m.Name)
	return !strings.Contains(v, m.Regex.Pattern)
}

type MultiMatcher struct {
	Matchers []LabelMatcher
}

func (m MultiMatcher) Matches(labels Labels) bool {
	for _, sub := range m.Matchers {
		if !sub.Matches(labels) {
			return false
		}
	}
	return true
}
