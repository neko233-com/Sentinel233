package tsdb

import (
	"os"
	"testing"
	"time"
)

func TestDBAppendAndQuery(t *testing.T) {
	dir, err := os.MkdirTemp("", "tsdb-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	db, err := OpenDB(DBConfig{
		DataDir:       dir,
		Retention:     24 * time.Hour,
		FlushInterval: 1 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	labels := Labels{
		{Name: "__name__", Value: "http_requests_total"},
		{Name: "method", Value: "GET"},
		{Name: "status", Value: "200"},
	}

	now := time.Now().UnixMilli()
	for i := 0; i < 100; i++ {
		if err := db.Append(labels, now+int64(i*1000), float64(i)); err != nil {
			t.Fatalf("append failed: %v", err)
		}
	}

	if db.SeriesCount() != 1 {
		t.Fatalf("expected 1 series, got %d", db.SeriesCount())
	}
	if db.TotalSamples() != 100 {
		t.Fatalf("expected 100 samples, got %d", db.TotalSamples())
	}

	samples := db.Query(labels, now, now+99*1000)
	if len(samples) != 100 {
		t.Fatalf("expected 100 samples in range, got %d", len(samples))
	}
	if samples[0].Value != 0 || samples[99].Value != 99 {
		t.Fatalf("unexpected sample values: first=%f last=%f", samples[0].Value, samples[99].Value)
	}
}

func TestSeriesLabelsHash(t *testing.T) {
	labels1 := Labels{
		{Name: "__name__", Value: "up"},
		{Name: "job", Value: "test"},
	}
	labels2 := Labels{
		{Name: "__name__", Value: "up"},
		{Name: "job", Value: "test"},
	}
	labels3 := Labels{
		{Name: "__name__", Value: "down"},
		{Name: "job", Value: "test"},
	}

	if labels1.Hash() != labels2.Hash() {
		t.Fatal("identical labels should have same hash")
	}
	if labels1.Hash() == labels3.Hash() {
		t.Fatal("different labels should have different hash")
	}
	labels4 := Labels{
		{Name: "job", Value: "test"},
		{Name: "__name__", Value: "up"},
	}
	if labels1.Hash() != labels4.Hash() {
		t.Fatal("label hash should not depend on label order")
	}
}

func TestLabelGet(t *testing.T) {
	labels := Labels{
		{Name: "__name__", Value: "up"},
		{Name: "instance", Value: "localhost:9090"},
	}
	if labels.Get("__name__") != "up" {
		t.Fatal("expected 'up'")
	}
	if labels.Get("instance") != "localhost:9090" {
		t.Fatal("expected 'localhost:9090'")
	}
	if labels.Get("nonexistent") != "" {
		t.Fatal("expected empty string for nonexistent label")
	}
}

func TestWALReplay(t *testing.T) {
	dir, err := os.MkdirTemp("", "tsdb-wal-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	labels := Labels{{Name: "__name__", Value: "test_metric"}}

	// Write data and close
	db1, err := OpenDB(DBConfig{DataDir: dir, Retention: 24 * time.Hour, FlushInterval: 1 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		db1.Append(labels, int64(i*1000), float64(i))
	}
	db1.Close()

	// Reopen and verify data is replayed
	db2, err := OpenDB(DBConfig{DataDir: dir, Retention: 24 * time.Hour, FlushInterval: 1 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()

	if db2.TotalSamples() != 50 {
		t.Fatalf("expected 50 samples after replay, got %d", db2.TotalSamples())
	}
}

func TestSnapshotSurvivesCompactionAndRestart(t *testing.T) {
	dir, err := os.MkdirTemp("", "tsdb-snapshot-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	labels := Labels{{Name: "__name__", Value: "agent_up"}, {Name: "agent_id", Value: "node-1"}}
	now := time.Now().UnixMilli()
	db1, err := OpenDB(DBConfig{DataDir: dir, Retention: 24 * time.Hour, FlushInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if err := db1.Append(labels, now+int64(i*1000), float64(i)); err != nil {
			t.Fatal(err)
		}
	}
	db1.compact()
	if err := db1.Close(); err != nil {
		t.Fatal(err)
	}

	db2, err := OpenDB(DBConfig{DataDir: dir, Retention: 24 * time.Hour, FlushInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	samples := db2.Query(labels, now, now+9000)
	if len(samples) != 10 {
		t.Fatalf("snapshot samples = %d, want 10", len(samples))
	}
	if samples[9].Value != 9 {
		t.Fatalf("last sample = %#v", samples[9])
	}
}

func TestEqualMatcher(t *testing.T) {
	labels := Labels{{Name: "env", Value: "prod"}}
	m := EqualMatcher{Name: "env", Value: "prod"}
	if !m.Matches(labels) {
		t.Fatal("should match")
	}
	m2 := EqualMatcher{Name: "env", Value: "dev"}
	if m2.Matches(labels) {
		t.Fatal("should not match")
	}
}

func TestNotEqualMatcher(t *testing.T) {
	labels := Labels{{Name: "env", Value: "prod"}}
	m := NotEqualMatcher{Name: "env", Value: "dev"}
	if !m.Matches(labels) {
		t.Fatal("should match")
	}
	m2 := NotEqualMatcher{Name: "env", Value: "prod"}
	if m2.Matches(labels) {
		t.Fatal("should not match")
	}
}

func TestMultiMatcher(t *testing.T) {
	labels := Labels{
		{Name: "env", Value: "prod"},
		{Name: "job", Value: "api"},
	}
	m := MultiMatcher{
		Matchers: []LabelMatcher{
			EqualMatcher{Name: "env", Value: "prod"},
			EqualMatcher{Name: "job", Value: "api"},
		},
	}
	if !m.Matches(labels) {
		t.Fatal("should match all")
	}
	m.Matchers = append(m.Matchers, EqualMatcher{Name: "region", Value: "us"})
	if m.Matches(labels) {
		t.Fatal("should not match missing label")
	}
}

func TestSeriesTrimBefore(t *testing.T) {
	s := NewSeries(Labels{{Name: "__name__", Value: "test"}})
	for i := 0; i < 100; i++ {
		s.Append(int64(i*1000), float64(i))
	}
	s.TrimBefore(50000)
	samples := s.Samples()
	if len(samples) != 50 {
		t.Fatalf("expected 50 samples after trim, got %d", len(samples))
	}
	if samples[0].Timestamp != 50000 {
		t.Fatalf("expected first sample at 50000, got %d", samples[0].Timestamp)
	}
}

func TestQueryByMatcher(t *testing.T) {
	dir, _ := os.MkdirTemp("", "tsdb-matcher-*")
	defer os.RemoveAll(dir)
	db, _ := OpenDB(DBConfig{DataDir: dir, Retention: 24 * time.Hour, FlushInterval: 1 * time.Hour})
	defer db.Close()

	now := time.Now().UnixMilli()
	db.Append(Labels{{Name: "__name__", Value: "up"}, {Name: "job", Value: "api"}}, now, 1)
	db.Append(Labels{{Name: "__name__", Value: "up"}, {Name: "job", Value: "web"}}, now, 1)
	db.Append(Labels{{Name: "__name__", Value: "down"}, {Name: "job", Value: "api"}}, now, 0)

	m := EqualMatcher{Name: "__name__", Value: "up"}
	series := db.QueryByMatcher(m, 0, now)
	if len(series) != 2 {
		t.Fatalf("expected 2 series with __name__=up, got %d", len(series))
	}
}
