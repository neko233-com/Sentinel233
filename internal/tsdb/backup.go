package tsdb

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

const backupFormatVersion = 1

type backupManifest struct {
	Format          string    `json:"format"`
	Version         int       `json:"version"`
	CreatedAt       time.Time `json:"created_at"`
	Series          int       `json:"series"`
	Samples         int       `json:"samples"`
	Chunks          int       `json:"chunks"`
	Shards          int       `json:"shards"`
	SamplesPerChunk int       `json:"samples_per_chunk"`
	MinTime         int64     `json:"min_time"`
	MaxTime         int64     `json:"max_time"`
}

type backupSample struct {
	Labels    Labels  `json:"labels"`
	Timestamp int64   `json:"timestamp"`
	Value     float64 `json:"value"`
}

func (db *DB) Export(w io.Writer) error {
	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	stats := db.Stats()
	manifest := backupManifest{
		Format:          "sentinel233-tsdb-backup",
		Version:         backupFormatVersion,
		CreatedAt:       time.Now().UTC(),
		Series:          stats.Series,
		Samples:         stats.Samples,
		Chunks:          stats.Chunks,
		Shards:          stats.ShardCount,
		SamplesPerChunk: stats.SamplesPerChunk,
		MinTime:         stats.MinTime,
		MaxTime:         stats.MaxTime,
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := writeTarFile(tw, "manifest.json", manifestData); err != nil {
		return err
	}

	var samples bytes.Buffer
	enc := json.NewEncoder(&samples)
	for _, series := range db.AllSeries() {
		for _, sample := range series.Samples() {
			if err := enc.Encode(backupSample{Labels: series.Labels, Timestamp: sample.Timestamp, Value: sample.Value}); err != nil {
				return err
			}
		}
	}
	if err := writeTarFile(tw, "samples.jsonl", samples.Bytes()); err != nil {
		return err
	}
	return nil
}

func (db *DB) Import(r io.Reader) (int, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return 0, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	imported := 0
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return imported, err
		}
		if header.Name != "samples.jsonl" {
			continue
		}
		dec := json.NewDecoder(tr)
		for {
			var sample backupSample
			if err := dec.Decode(&sample); err != nil {
				if err == io.EOF {
					break
				}
				return imported, err
			}
			if len(sample.Labels) == 0 {
				return imported, fmt.Errorf("backup sample missing labels")
			}
			if err := db.Append(sample.Labels, sample.Timestamp, sample.Value); err != nil {
				return imported, err
			}
			imported++
		}
	}
	return imported, nil
}

func writeTarFile(tw *tar.Writer, name string, data []byte) error {
	if err := tw.WriteHeader(&tar.Header{
		Name:    name,
		Mode:    0644,
		Size:    int64(len(data)),
		ModTime: time.Now(),
	}); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}
