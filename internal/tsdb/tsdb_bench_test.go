package tsdb

import (
	"fmt"
	"os"
	"testing"
	"time"
)

func BenchmarkAppendSingleSeries(b *testing.B) {
	dir, _ := os.MkdirTemp("", "tsdb-bench-single-*")
	defer os.RemoveAll(dir)
	db, err := OpenDB(DBConfig{DataDir: dir, Retention: 24 * time.Hour, FlushInterval: time.Hour})
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()

	labels := Labels{{Name: "__name__", Value: "bench_metric"}, {Name: "job", Value: "api"}}
	now := time.Now().UnixMilli()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.Append(labels, now+int64(i), float64(i)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAppendManySeries(b *testing.B) {
	dir, _ := os.MkdirTemp("", "tsdb-bench-many-*")
	defer os.RemoveAll(dir)
	db, err := OpenDB(DBConfig{DataDir: dir, Retention: 24 * time.Hour, FlushInterval: time.Hour})
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()

	seriesCount := 10000
	labels := make([]Labels, seriesCount)
	for i := range labels {
		labels[i] = Labels{{Name: "__name__", Value: "bench_metric"}, {Name: "instance", Value: fmt.Sprintf("node-%05d", i)}}
	}
	now := time.Now().UnixMilli()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.Append(labels[i%seriesCount], now+int64(i), float64(i)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkQueryRangeChunkedSeries(b *testing.B) {
	dir, _ := os.MkdirTemp("", "tsdb-bench-query-*")
	defer os.RemoveAll(dir)
	db, err := OpenDB(DBConfig{DataDir: dir, Retention: 24 * time.Hour, FlushInterval: time.Hour})
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()

	labels := Labels{{Name: "__name__", Value: "bench_metric"}, {Name: "job", Value: "api"}}
	now := time.Now().UnixMilli()
	for i := 0; i < 1_000_000; i++ {
		if err := db.Append(labels, now+int64(i), float64(i)); err != nil {
			b.Fatal(err)
		}
	}
	mint := now + 500_000
	maxt := mint + 10_000
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if samples := db.Query(labels, mint, maxt); len(samples) == 0 {
			b.Fatal("expected samples")
		}
	}
}
