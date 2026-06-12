package api

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"

	"github.com/golang/snappy"

	"github.com/neko233-com/Sentinel233/internal/tsdb"
)

const maxRemoteWriteBodyBytes = 64 << 20

type remoteWriteRequest struct {
	TimeSeries []remoteWriteSeries
}

type remoteWriteSeries struct {
	Labels  tsdb.Labels
	Samples []remoteWriteSample
}

type remoteWriteSample struct {
	Value     float64
	Timestamp int64
}

func (s *Server) handleRemoteWrite(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRemoteWriteBodyBytes))
	if err != nil {
		s.jsonError(w, "remote write body is too large", http.StatusRequestEntityTooLarge)
		return
	}
	if len(body) == 0 {
		s.jsonError(w, "remote write body is empty", http.StatusBadRequest)
		return
	}

	payload, err := decodeRemoteWritePayload(body)
	if err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	req, err := decodeRemoteWriteRequest(payload)
	if err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	written := 0
	for _, series := range req.TimeSeries {
		if len(series.Labels) == 0 || len(series.Samples) == 0 {
			continue
		}
		sort.Slice(series.Labels, func(i, j int) bool {
			return series.Labels[i].Name < series.Labels[j].Name
		})
		for _, sample := range series.Samples {
			if err := s.db.Append(series.Labels, sample.Timestamp, sample.Value); err != nil {
				s.jsonError(w, err.Error(), http.StatusInternalServerError)
				return
			}
			written++
		}
	}
	if written == 0 {
		s.jsonError(w, "remote write request contained no samples", http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func decodeRemoteWritePayload(body []byte) ([]byte, error) {
	decoded, err := snappy.Decode(nil, body)
	if err == nil {
		return decoded, nil
	}
	return body, nil
}

func decodeRemoteWriteRequest(data []byte) (remoteWriteRequest, error) {
	var req remoteWriteRequest
	if err := walkProto(data, func(field int, wire int, value []byte, _ uint64) error {
		if field == 1 && wire == protoWireBytes {
			series, err := decodeRemoteWriteSeries(value)
			if err != nil {
				return err
			}
			req.TimeSeries = append(req.TimeSeries, series)
		}
		return nil
	}); err != nil {
		return req, fmt.Errorf("invalid remote write protobuf: %w", err)
	}
	return req, nil
}

func decodeRemoteWriteSeries(data []byte) (remoteWriteSeries, error) {
	var series remoteWriteSeries
	if err := walkProto(data, func(field int, wire int, value []byte, _ uint64) error {
		switch {
		case field == 1 && wire == protoWireBytes:
			label, err := decodeRemoteWriteLabel(value)
			if err != nil {
				return err
			}
			if label.Name != "" {
				series.Labels = append(series.Labels, label)
			}
		case field == 2 && wire == protoWireBytes:
			sample, err := decodeRemoteWriteSample(value)
			if err != nil {
				return err
			}
			series.Samples = append(series.Samples, sample)
		}
		return nil
	}); err != nil {
		return series, err
	}
	return series, nil
}

func decodeRemoteWriteLabel(data []byte) (tsdb.Label, error) {
	var label tsdb.Label
	if err := walkProto(data, func(field int, wire int, value []byte, _ uint64) error {
		if wire != protoWireBytes {
			return nil
		}
		switch field {
		case 1:
			label.Name = string(value)
		case 2:
			label.Value = string(value)
		}
		return nil
	}); err != nil {
		return label, err
	}
	return label, nil
}

func decodeRemoteWriteSample(data []byte) (remoteWriteSample, error) {
	var sample remoteWriteSample
	if err := walkProto(data, func(field int, wire int, value []byte, varint uint64) error {
		switch {
		case field == 1 && wire == protoWireFixed64:
			sample.Value = math.Float64frombits(binary.LittleEndian.Uint64(value))
		case field == 2 && wire == protoWireVarint:
			sample.Timestamp = int64(varint)
		}
		return nil
	}); err != nil {
		return sample, err
	}
	return sample, nil
}

const (
	protoWireVarint  = 0
	protoWireFixed64 = 1
	protoWireBytes   = 2
	protoWireFixed32 = 5
)

func walkProto(data []byte, visit func(field int, wire int, value []byte, varint uint64) error) error {
	for offset := 0; offset < len(data); {
		key, next, err := readProtoVarint(data, offset)
		if err != nil {
			return err
		}
		offset = next
		field := int(key >> 3)
		wire := int(key & 0x7)
		if field <= 0 {
			return fmt.Errorf("invalid field number %d", field)
		}

		switch wire {
		case protoWireVarint:
			v, next, err := readProtoVarint(data, offset)
			if err != nil {
				return err
			}
			offset = next
			if err := visit(field, wire, nil, v); err != nil {
				return err
			}
		case protoWireFixed64:
			if offset+8 > len(data) {
				return io.ErrUnexpectedEOF
			}
			value := data[offset : offset+8]
			offset += 8
			if err := visit(field, wire, value, 0); err != nil {
				return err
			}
		case protoWireBytes:
			size, next, err := readProtoVarint(data, offset)
			if err != nil {
				return err
			}
			offset = next
			end := offset + int(size)
			if end < offset || end > len(data) {
				return io.ErrUnexpectedEOF
			}
			value := data[offset:end]
			offset = end
			if err := visit(field, wire, value, 0); err != nil {
				return err
			}
		case protoWireFixed32:
			if offset+4 > len(data) {
				return io.ErrUnexpectedEOF
			}
			value := data[offset : offset+4]
			offset += 4
			if err := visit(field, wire, value, 0); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported protobuf wire type %d", wire)
		}
	}
	return nil
}

func readProtoVarint(data []byte, offset int) (uint64, int, error) {
	var value uint64
	for shift := uint(0); shift < 64; shift += 7 {
		if offset >= len(data) {
			return 0, offset, io.ErrUnexpectedEOF
		}
		b := data[offset]
		offset++
		value |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return value, offset, nil
		}
	}
	return 0, offset, fmt.Errorf("protobuf varint overflow")
}
