package tsdb

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type WALEntry struct {
	Labels    Labels
	Timestamp int64
	Value     float64
}

type WAL struct {
	dir    string
	mu     sync.Mutex
	file   *os.File
	writer *bufio.Writer
}

func NewWAL(dir string) (*WAL, error) {
	walDir := filepath.Join(dir, "wal")
	if err := os.MkdirAll(walDir, 0755); err != nil {
		return nil, fmt.Errorf("wal: mkdir: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(walDir, "wal.dat"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("wal: open: %w", err)
	}
	return &WAL{
		dir:    walDir,
		file:   f,
		writer: bufio.NewWriter(f),
	}, nil
}

func (w *WAL) Write(entry WALEntry) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	labelsStr := labelsToString(entry.Labels)

	var buf [binary.MaxVarintLen64]byte
	// write label string length
	n := binary.PutVarint(buf[:], int64(len(labelsStr)))
	if _, err := w.writer.Write(buf[:n]); err != nil {
		return err
	}
	// write labels
	if _, err := w.writer.WriteString(labelsStr); err != nil {
		return err
	}
	// write timestamp
	n = binary.PutVarint(buf[:], entry.Timestamp)
	if _, err := w.writer.Write(buf[:n]); err != nil {
		return err
	}
	// write value as uint64 bits
	bits := math.Float64bits(entry.Value)
	binary.LittleEndian.PutUint16(buf[:2], uint16(bits>>48))
	binary.LittleEndian.PutUint16(buf[2:4], uint16(bits>>32))
	binary.LittleEndian.PutUint16(buf[4:6], uint16(bits>>16))
	binary.LittleEndian.PutUint16(buf[6:8], uint16(bits))
	if _, err := w.writer.Write(buf[:8]); err != nil {
		return err
	}
	return nil
}

func (w *WAL) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Flush()
}

func (w *WAL) ReadAll() ([]WALEntry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.writer.Flush(); err != nil {
		return nil, err
	}

	return readEntriesFile(filepath.Join(w.dir, "wal.dat"))
}

func (w *WAL) ReadSnapshot() ([]WALEntry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return readEntriesFile(filepath.Join(w.dir, "snapshot.dat"))
}

func (w *WAL) WriteSnapshot(entries []WALEntry) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	tmpFile := filepath.Join(w.dir, "snapshot.dat.tmp")
	snapshotFile := filepath.Join(w.dir, "snapshot.dat")
	f, err := os.OpenFile(tmpFile, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	writer := bufio.NewWriter(f)
	for _, entry := range entries {
		if err := writeEntry(writer, entry); err != nil {
			f.Close()
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmpFile, snapshotFile)
}

func readEntriesFile(path string) ([]WALEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var entries []WALEntry
	reader := bufio.NewReader(f)
	for {
		labelLen, err := binary.ReadVarint(reader)
		if err != nil {
			if err == io.EOF {
				break
			}
			return entries, nil // partial read is ok
		}
		labelBuf := make([]byte, labelLen)
		if _, err := io.ReadFull(reader, labelBuf); err != nil {
			return entries, nil
		}
		ts, err := binary.ReadVarint(reader)
		if err != nil {
			return entries, nil
		}
		var valBuf [8]byte
		if _, err := io.ReadFull(reader, valBuf[:]); err != nil {
			return entries, nil
		}
		bits := uint64(binary.LittleEndian.Uint16(valBuf[:2]))<<48 |
			uint64(binary.LittleEndian.Uint16(valBuf[2:4]))<<32 |
			uint64(binary.LittleEndian.Uint16(valBuf[4:6]))<<16 |
			uint64(binary.LittleEndian.Uint16(valBuf[6:8]))
		value := math.Float64frombits(bits)

		entries = append(entries, WALEntry{
			Labels:    stringToLabels(string(labelBuf)),
			Timestamp: ts,
			Value:     value,
		})
	}
	return entries, nil
}

func writeEntry(writer *bufio.Writer, entry WALEntry) error {
	labelsStr := labelsToString(entry.Labels)

	var buf [binary.MaxVarintLen64]byte
	n := binary.PutVarint(buf[:], int64(len(labelsStr)))
	if _, err := writer.Write(buf[:n]); err != nil {
		return err
	}
	if _, err := writer.WriteString(labelsStr); err != nil {
		return err
	}
	n = binary.PutVarint(buf[:], entry.Timestamp)
	if _, err := writer.Write(buf[:n]); err != nil {
		return err
	}
	bits := math.Float64bits(entry.Value)
	binary.LittleEndian.PutUint16(buf[:2], uint16(bits>>48))
	binary.LittleEndian.PutUint16(buf[2:4], uint16(bits>>32))
	binary.LittleEndian.PutUint16(buf[4:6], uint16(bits>>16))
	binary.LittleEndian.PutUint16(buf[6:8], uint16(bits))
	_, err := writer.Write(buf[:8])
	return err
}

func (w *WAL) Truncate() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.writer.Flush(); err != nil {
		return err
	}
	if err := w.file.Close(); err != nil {
		return err
	}

	walFile := filepath.Join(w.dir, "wal.dat")
	if err := os.Truncate(walFile, 0); err != nil {
		return err
	}
	f, err := os.OpenFile(walFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	w.file = f
	w.writer = bufio.NewWriter(f)
	return nil
}

func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.writer.Flush(); err != nil {
		return err
	}
	return w.file.Close()
}

func labelsToString(labels Labels) string {
	sort.Slice(labels, func(i, j int) bool {
		return labels[i].Name < labels[j].Name
	})
	parts := make([]string, len(labels))
	for i, l := range labels {
		parts[i] = l.Name + "=" + l.Value
	}
	return strings.Join(parts, ",")
}

func stringToLabels(s string) Labels {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	labels := make(Labels, len(parts))
	for i, p := range parts {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) == 2 {
			labels[i] = Label{Name: kv[0], Value: kv[1]}
		}
	}
	return labels
}
