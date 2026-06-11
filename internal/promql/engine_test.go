package promql

import (
	"os"
	"testing"
	"time"

	"github.com/neko233-com/Sentinel233/internal/tsdb"
)

func setupTestDB(t *testing.T) (*tsdb.DB, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "promql-test-*")
	if err != nil {
		t.Fatal(err)
	}
	db, err := tsdb.OpenDB(tsdb.DBConfig{
		DataDir:       dir,
		Retention:     24 * time.Hour,
		FlushInterval: 1 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	cleanup := func() {
		db.Close()
		os.RemoveAll(dir)
	}
	return db, cleanup
}

func TestParserVectorSelector(t *testing.T) {
	p := newParser(`http_requests_total{method="GET",status="200"}`)
	node, err := p.parseExpr()
	if err != nil {
		t.Fatal(err)
	}
	sel, ok := node.(*VectorSelector)
	if !ok {
		t.Fatalf("expected VectorSelector, got %T", node)
	}
	if sel.Name != "http_requests_total" {
		t.Fatalf("expected name http_requests_total, got %s", sel.Name)
	}
	if len(sel.LabelMatchers) != 2 {
		t.Fatalf("expected 2 matchers, got %d", len(sel.LabelMatchers))
	}
}

func TestParserNumber(t *testing.T) {
	p := newParser("42")
	node, err := p.parseExpr()
	if err != nil {
		t.Fatal(err)
	}
	nl, ok := node.(*NumberLiteral)
	if !ok {
		t.Fatalf("expected NumberLiteral, got %T", node)
	}
	if nl.Value != 42 {
		t.Fatalf("expected 42, got %f", nl.Value)
	}
}

func TestParserBinaryExpr(t *testing.T) {
	p := newParser("1 + 2")
	node, err := p.parseExpr()
	if err != nil {
		t.Fatal(err)
	}
	be, ok := node.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr, got %T", node)
	}
	if be.Op != "+" {
		t.Fatalf("expected op +, got %s", be.Op)
	}
}

func TestParserAggregation(t *testing.T) {
	p := newParser(`sum(http_requests_total{job="api"})`)
	node, err := p.parseExpr()
	if err != nil {
		t.Fatal(err)
	}
	agg, ok := node.(*AggExpr)
	if !ok {
		t.Fatalf("expected AggExpr, got %T", node)
	}
	if agg.Op != "sum" {
		t.Fatalf("expected sum, got %s", agg.Op)
	}
}

func TestParserMatrixSelector(t *testing.T) {
	p := newParser(`http_requests_total{job="api"}[5m]`)
	node, err := p.parseExpr()
	if err != nil {
		t.Fatal(err)
	}
	ms, ok := node.(*MatrixSelector)
	if !ok {
		t.Fatalf("expected MatrixSelector, got %T", node)
	}
	if ms.Range != 5*time.Minute {
		t.Fatalf("expected 5m range, got %v", ms.Range)
	}
}

func TestEngineEvalInstant(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	engine := NewEngine(db)
	now := time.Now()
	ts := now.UnixMilli()

	labels := tsdb.Labels{
		{Name: "__name__", Value: "up"},
		{Name: "job", Value: "test"},
	}
	db.Append(labels, ts, 1)

	result, err := engine.EvalInstant("up", now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Type != ValueInstantVector {
		t.Fatalf("expected instant vector, got %d", result.Type)
	}
	if len(result.Vector) != 1 {
		t.Fatalf("expected 1 sample, got %d", len(result.Vector))
	}
	if result.Vector[0].Point.Value != 1 {
		t.Fatalf("expected value 1, got %f", result.Vector[0].Point.Value)
	}
}

func TestEngineEvalScalar(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	engine := NewEngine(db)

	result, err := engine.EvalInstant("1 + 2 * 3", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.Type != ValueScalar {
		t.Fatalf("expected scalar, got %d", result.Type)
	}
	if result.Scalar != 7 {
		t.Fatalf("expected 7, got %f", result.Scalar)
	}
}

func TestEngineEvalBinaryVectorScalar(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	engine := NewEngine(db)
	now := time.Now()
	ts := now.UnixMilli()

	db.Append(tsdb.Labels{{Name: "__name__", Value: "cpu"}, {Name: "host", Value: "a"}}, ts, 50)
	db.Append(tsdb.Labels{{Name: "__name__", Value: "cpu"}, {Name: "host", Value: "b"}}, ts, 80)

	result, err := engine.EvalInstant("cpu * 2", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Vector) != 2 {
		t.Fatalf("expected 2 samples, got %d", len(result.Vector))
	}
}

func TestEngineEvalAgg(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	engine := NewEngine(db)
	now := time.Now()
	ts := now.UnixMilli()

	db.Append(tsdb.Labels{{Name: "__name__", Value: "req"}, {Name: "host", Value: "a"}}, ts, 10)
	db.Append(tsdb.Labels{{Name: "__name__", Value: "req"}, {Name: "host", Value: "b"}}, ts, 20)
	db.Append(tsdb.Labels{{Name: "__name__", Value: "req"}, {Name: "host", Value: "c"}}, ts, 30)

	result, err := engine.EvalInstant("sum(req)", now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Type != ValueInstantVector {
		t.Fatalf("expected vector, got %d", result.Type)
	}
	if len(result.Vector) != 1 {
		t.Fatalf("expected 1 aggregated result, got %d", len(result.Vector))
	}
	if result.Vector[0].Point.Value != 60 {
		t.Fatalf("expected sum=60, got %f", result.Vector[0].Point.Value)
	}
}

func TestEngineFunctionAbs(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	engine := NewEngine(db)

	result, err := engine.EvalInstant("abs(-42)", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.Scalar != 42 {
		t.Fatalf("expected 42, got %f", result.Scalar)
	}
}

func TestEngineFunctionRound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	engine := NewEngine(db)

	result, err := engine.EvalInstant("round(3.7)", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.Scalar != 4 {
		t.Fatalf("expected 4, got %f", result.Scalar)
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
	}{
		{"5m", 5 * time.Minute},
		{"1h", 1 * time.Hour},
		{"30s", 30 * time.Second},
		{"1d", 24 * time.Hour},
		{"100ms", 100 * time.Millisecond},
		{"1h30m", 90 * time.Minute},
	}
	for _, tt := range tests {
		d, err := parsePromDuration(tt.input)
		if err != nil {
			t.Errorf("parsePromDuration(%q) error: %v", tt.input, err)
			continue
		}
		if d != tt.expected {
			t.Errorf("parsePromDuration(%q) = %v, want %v", tt.input, d, tt.expected)
		}
	}
}

func TestApplyOp(t *testing.T) {
	tests := []struct {
		op       string
		a, b     float64
		expected float64
	}{
		{"+", 1, 2, 3},
		{"-", 5, 3, 2},
		{"*", 4, 5, 20},
		{"/", 10, 2, 5},
		{"^", 2, 3, 8},
		{">", 5, 3, 1},
		{"<", 3, 5, 1},
		{"==", 5, 5, 1},
		{"!=", 5, 3, 1},
	}
	for _, tt := range tests {
		result := applyOp(tt.op, tt.a, tt.b)
		if result != tt.expected {
			t.Errorf("applyOp(%s, %f, %f) = %f, want %f", tt.op, tt.a, tt.b, result, tt.expected)
		}
	}
}
