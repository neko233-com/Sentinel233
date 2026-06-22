package tsdb

import (
	"regexp"
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
	const (
		fnvOffset64 = 14695981039346656037
		fnvPrime64  = 1099511628211
	)
	h := uint64(fnvOffset64)
	labels := sortedLabels(l)
	for _, lb := range labels {
		for i := 0; i < len(lb.Name); i++ {
			h ^= uint64(lb.Name[i])
			h *= fnvPrime64
		}
		h *= fnvPrime64
		for i := 0; i < len(lb.Value); i++ {
			h ^= uint64(lb.Value[i])
			h *= fnvPrime64
		}
		h *= fnvPrime64
	}
	return h
}

func sortedLabels(labels Labels) Labels {
	if labelsSorted(labels) {
		return labels
	}
	labels = append(Labels(nil), labels...)
	sort.Slice(labels, func(i, j int) bool {
		return labels[i].Name < labels[j].Name
	})
	return labels
}

func labelsSorted(labels Labels) bool {
	for i := 1; i < len(labels); i++ {
		if labels[i-1].Name > labels[i].Name {
			return false
		}
	}
	return true
}

type Series struct {
	Labels Labels
	chunks []sampleChunk
	count  int
	mu     sync.RWMutex
}

type sampleChunk struct {
	samples []Sample
}

func NewSeries(labels Labels) *Series {
	return &Series{
		Labels: labels,
	}
}

const samplesPerChunk = 1024

func (s *Series) Append(ts int64, v float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sample := Sample{Timestamp: ts, Value: v}
	if s.count == 0 || ts >= s.lastTimestampLocked() {
		s.appendInOrderLocked(sample)
		return
	}
	samples := s.samplesLocked()
	i := sort.Search(len(samples), func(i int) bool {
		return samples[i].Timestamp > ts
	})
	samples = append(samples, Sample{})
	copy(samples[i+1:], samples[i:])
	samples[i] = sample
	s.rebuildLocked(samples)
}

func (s *Series) Samples() []Sample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.samplesLocked()
}

func (s *Series) Range(mint, maxt int64) []Sample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.count == 0 || maxt < mint {
		return nil
	}
	startChunk := sort.Search(len(s.chunks), func(i int) bool {
		chunk := s.chunks[i].samples
		return chunk[len(chunk)-1].Timestamp >= mint
	})
	if startChunk >= len(s.chunks) {
		return nil
	}
	result := make([]Sample, 0, estimateRangeSize(s.count, len(s.chunks)))
	for i := startChunk; i < len(s.chunks); i++ {
		chunk := s.chunks[i].samples
		if len(chunk) == 0 {
			continue
		}
		if chunk[0].Timestamp > maxt {
			break
		}
		start := 0
		if chunk[0].Timestamp < mint {
			start = sort.Search(len(chunk), func(i int) bool {
				return chunk[i].Timestamp >= mint
			})
		}
		end := len(chunk)
		if chunk[len(chunk)-1].Timestamp > maxt {
			end = sort.Search(len(chunk), func(i int) bool {
				return chunk[i].Timestamp > maxt
			})
		}
		if start < end {
			result = append(result, chunk[start:end]...)
		}
	}
	return result
}

func (s *Series) Latest() (Sample, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.count == 0 {
		return Sample{}, false
	}
	last := s.chunks[len(s.chunks)-1].samples
	return last[len(last)-1], true
}

func (s *Series) TrimBefore(ts int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.count == 0 {
		return
	}
	firstKeptChunk := sort.Search(len(s.chunks), func(i int) bool {
		chunk := s.chunks[i].samples
		return chunk[len(chunk)-1].Timestamp >= ts
	})
	if firstKeptChunk >= len(s.chunks) {
		s.chunks = nil
		s.count = 0
		return
	}
	if firstKeptChunk > 0 {
		for i := 0; i < firstKeptChunk; i++ {
			s.count -= len(s.chunks[i].samples)
		}
		next := make([]sampleChunk, len(s.chunks)-firstKeptChunk)
		copy(next, s.chunks[firstKeptChunk:])
		s.chunks = next
	}
	first := s.chunks[0].samples
	trim := sort.Search(len(first), func(i int) bool {
		return first[i].Timestamp >= ts
	})
	if trim > 0 {
		s.count -= trim
		kept := make([]Sample, len(first)-trim, samplesPerChunk)
		copy(kept, first[trim:])
		s.chunks[0].samples = kept
	}
}

func (s *Series) Empty() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.count == 0
}

func (s *Series) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.count
}

func (s *Series) ChunkCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.chunks)
}

func (s *Series) TimeRange() (int64, int64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.count == 0 {
		return 0, 0, false
	}
	first := s.chunks[0].samples[0].Timestamp
	lastChunk := s.chunks[len(s.chunks)-1].samples
	return first, lastChunk[len(lastChunk)-1].Timestamp, true
}

func (s *Series) appendInOrderLocked(sample Sample) {
	if len(s.chunks) == 0 || len(s.chunks[len(s.chunks)-1].samples) >= samplesPerChunk {
		s.chunks = append(s.chunks, sampleChunk{samples: make([]Sample, 0, samplesPerChunk)})
	}
	last := &s.chunks[len(s.chunks)-1]
	last.samples = append(last.samples, sample)
	s.count++
}

func (s *Series) lastTimestampLocked() int64 {
	last := s.chunks[len(s.chunks)-1].samples
	return last[len(last)-1].Timestamp
}

func (s *Series) samplesLocked() []Sample {
	cp := make([]Sample, 0, s.count)
	for _, chunk := range s.chunks {
		cp = append(cp, chunk.samples...)
	}
	return cp
}

func (s *Series) rebuildLocked(samples []Sample) {
	s.chunks = nil
	s.count = 0
	for _, sample := range samples {
		s.appendInOrderLocked(sample)
	}
}

func estimateRangeSize(total, chunks int) int {
	if chunks <= 0 {
		return 0
	}
	n := total / chunks
	if n < 64 {
		return 64
	}
	return n
}

type DB struct {
	shards []seriesShard
	index  *labelIndex

	retention   time.Duration
	retentionMu sync.RWMutex
	dataDir     string
	wal         *WAL
	stopCh      chan struct{}
}

type seriesShard struct {
	mu           sync.RWMutex
	series       map[uint64]*Series
	labelStrings map[uint64]string
}

type labelIndex struct {
	mu     sync.RWMutex
	values map[string]map[string]map[uint64]*Series
}

const seriesShardCount = 64

type DBConfig struct {
	DataDir       string
	Retention     time.Duration
	FlushInterval time.Duration
}

type Stats struct {
	Series          int   `json:"series"`
	Samples         int   `json:"samples"`
	Chunks          int   `json:"chunks"`
	ShardCount      int   `json:"shard_count"`
	IndexedLabels   int   `json:"indexed_labels"`
	IndexedValues   int   `json:"indexed_values"`
	SamplesPerChunk int   `json:"samples_per_chunk"`
	MinTime         int64 `json:"min_time"`
	MaxTime         int64 `json:"max_time"`
}

func OpenDB(cfg DBConfig) (*DB, error) {
	wal, err := NewWAL(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	db := &DB{
		shards:    newSeriesShards(seriesShardCount),
		index:     newLabelIndex(),
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

func newSeriesShards(n int) []seriesShard {
	shards := make([]seriesShard, n)
	for i := range shards {
		shards[i].series = make(map[uint64]*Series)
		shards[i].labelStrings = make(map[uint64]string)
	}
	return shards
}

func newLabelIndex() *labelIndex {
	return &labelIndex{values: make(map[string]map[string]map[uint64]*Series)}
}

func (db *DB) replayWAL() error {
	snapshotEntries, err := db.wal.ReadSnapshot()
	if err != nil {
		return err
	}
	for _, e := range snapshotEntries {
		s := db.getOrCreateSeries(e.Labels)
		s.Append(e.Timestamp, e.Value)
	}
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
	shard := db.shardFor(h)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	return db.getOrCreateSeriesLocked(shard, h, labels)
}

func (db *DB) getOrCreateSeriesLocked(shard *seriesShard, h uint64, labels Labels) *Series {
	if s, ok := shard.series[h]; ok {
		return s
	}
	s := NewSeries(labels)
	shard.series[h] = s
	shard.labelStrings[h] = labelsToString(labels)
	db.index.add(h, s)
	return s
}

func (db *DB) Append(labels Labels, ts int64, v float64) error {
	h := labels.Hash()
	shard := db.shardFor(h)
	shard.mu.Lock()
	labelString := shard.labelStrings[h]
	if labelString == "" {
		labelString = labelsToString(labels)
	}
	if err := db.wal.WriteEncoded(labelString, ts, v); err != nil {
		shard.mu.Unlock()
		return err
	}
	s := db.getOrCreateSeriesLocked(shard, h, labels)
	s.Append(ts, v)
	shard.mu.Unlock()
	return nil
}

func (db *DB) Query(labels Labels, mint, maxt int64) []Sample {
	h := labels.Hash()
	shard := db.shardFor(h)
	shard.mu.RLock()
	s, ok := shard.series[h]
	shard.mu.RUnlock()
	if !ok {
		return nil
	}
	return s.Range(mint, maxt)
}

func (db *DB) SetRetention(retention time.Duration) error {
	if retention <= 0 {
		return nil
	}
	db.retentionMu.Lock()
	db.retention = retention
	db.retentionMu.Unlock()
	db.Compact()
	return nil
}

func (db *DB) Retention() time.Duration {
	db.retentionMu.RLock()
	defer db.retentionMu.RUnlock()
	return db.retention
}

func (db *DB) QueryByMatcher(matcher LabelMatcher, mint, maxt int64) map[uint64]*Series {
	if name, value, ok := matcherEqualHint(matcher); ok {
		return filterSeries(db.index.equal(name, value), matcher)
	}
	result := make(map[uint64]*Series)
	for i := range db.shards {
		shard := &db.shards[i]
		shard.mu.RLock()
		for hash, s := range shard.series {
			if matcher.Matches(s.Labels) {
				result[hash] = s
			}
		}
		shard.mu.RUnlock()
	}
	return result
}

func matcherEqualHint(matcher LabelMatcher) (string, string, bool) {
	switch m := matcher.(type) {
	case EqualMatcher:
		return m.Name, m.Value, true
	case MultiMatcher:
		for _, sub := range m.Matchers {
			if name, value, ok := matcherEqualHint(sub); ok {
				return name, value, true
			}
		}
	}
	return "", "", false
}

func (idx *labelIndex) add(hash uint64, s *Series) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	for _, label := range s.Labels {
		byValue, ok := idx.values[label.Name]
		if !ok {
			byValue = make(map[string]map[uint64]*Series)
			idx.values[label.Name] = byValue
		}
		series, ok := byValue[label.Value]
		if !ok {
			series = make(map[uint64]*Series)
			byValue[label.Value] = series
		}
		series[hash] = s
	}
}

func (idx *labelIndex) remove(hash uint64, s *Series) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	for _, label := range s.Labels {
		byValue := idx.values[label.Name]
		if byValue == nil {
			continue
		}
		series := byValue[label.Value]
		delete(series, hash)
		if len(series) == 0 {
			delete(byValue, label.Value)
		}
		if len(byValue) == 0 {
			delete(idx.values, label.Name)
		}
	}
}

func (idx *labelIndex) equal(name, value string) map[uint64]*Series {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	result := make(map[uint64]*Series)
	for hash, s := range idx.values[name][value] {
		result[hash] = s
	}
	return result
}

func (db *DB) shardFor(hash uint64) *seriesShard {
	return &db.shards[int(hash%uint64(len(db.shards)))]
}

func (db *DB) allSeriesSnapshot() map[uint64]*Series {
	result := make(map[uint64]*Series)
	for i := range db.shards {
		shard := &db.shards[i]
		shard.mu.RLock()
		for hash, s := range shard.series {
			result[hash] = s
		}
		shard.mu.RUnlock()
	}
	return result
}

func filterSeries(series map[uint64]*Series, matcher LabelMatcher) map[uint64]*Series {
	result := make(map[uint64]*Series)
	for hash, s := range series {
		if matcher.Matches(s.Labels) {
			result[hash] = s
		}
	}
	return result
}

func (db *DB) AllSeries() []*Series {
	series := db.allSeriesSnapshot()
	result := make([]*Series, 0, len(series))
	for _, s := range series {
		result = append(result, s)
	}
	return result
}

func (db *DB) SeriesCount() int {
	total := 0
	for i := range db.shards {
		shard := &db.shards[i]
		shard.mu.RLock()
		total += len(shard.series)
		shard.mu.RUnlock()
	}
	return total
}

func (db *DB) TotalSamples() int {
	total := 0
	for i := range db.shards {
		shard := &db.shards[i]
		shard.mu.RLock()
		for _, s := range shard.series {
			total += s.Count()
		}
		shard.mu.RUnlock()
	}
	return total
}

func (db *DB) Stats() Stats {
	stats := Stats{
		ShardCount:      len(db.shards),
		SamplesPerChunk: samplesPerChunk,
		MinTime:         0,
		MaxTime:         0,
	}
	for i := range db.shards {
		shard := &db.shards[i]
		shard.mu.RLock()
		stats.Series += len(shard.series)
		for _, s := range shard.series {
			stats.Samples += s.Count()
			stats.Chunks += s.ChunkCount()
			if minTime, maxTime, ok := s.TimeRange(); ok {
				if stats.MinTime == 0 || minTime < stats.MinTime {
					stats.MinTime = minTime
				}
				if maxTime > stats.MaxTime {
					stats.MaxTime = maxTime
				}
			}
		}
		shard.mu.RUnlock()
	}
	stats.IndexedLabels, stats.IndexedValues = db.index.stats()
	return stats
}

func (idx *labelIndex) stats() (int, int) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	labelCount := len(idx.values)
	valueCount := 0
	for _, values := range idx.values {
		valueCount += len(values)
	}
	return labelCount, valueCount
}

func (db *DB) runCompaction(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			db.Compact()
		case <-db.stopCh:
			return
		}
	}
}

func (db *DB) Compact() {
	retention := db.Retention()
	cutoff := time.Now().Add(-retention).UnixMilli()

	for i := range db.shards {
		shard := &db.shards[i]
		shard.mu.Lock()
		for hash, s := range shard.series {
			s.TrimBefore(cutoff)
			if s.Empty() {
				delete(shard.series, hash)
				delete(shard.labelStrings, hash)
				db.index.remove(hash, s)
			}
		}
		shard.mu.Unlock()
	}

	entries := db.snapshotEntries()
	if err := db.wal.WriteSnapshot(entries); err != nil {
		return
	}
	if err := db.wal.Truncate(); err != nil {
		_ = err
	}
}

func (db *DB) compact() {
	db.Compact()
}

func (db *DB) snapshotEntries() []WALEntry {
	series := db.AllSeries()
	total := 0
	for _, s := range series {
		total += s.Count()
	}
	entries := make([]WALEntry, 0, total)
	for _, s := range series {
		for _, sample := range s.Samples() {
			entries = append(entries, WALEntry{
				Labels:    s.Labels,
				Timestamp: sample.Timestamp,
				Value:     sample.Value,
			})
		}
	}
	return entries
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
	matched, err := regexp.MatchString(m.Regex.Pattern, v)
	return err == nil && matched
}

type NotRegexMatcher struct {
	Name  string
	Regex *Regexp
}

func (m NotRegexMatcher) Matches(labels Labels) bool {
	v := labels.Get(m.Name)
	matched, err := regexp.MatchString(m.Regex.Pattern, v)
	return err != nil || !matched
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
