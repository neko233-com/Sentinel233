package promql

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/neko233-com/Sentinel233/internal/tsdb"
)

type ValueType int

const (
	ValueScalar ValueType = iota
	ValueInstantVector
	ValueRangeVector
)

type Result struct {
	Type   ValueType
	Vector Vector
	Scalar float64
}

type Vector []Sample

type Sample struct {
	Labels tsdb.Labels
	Point  tsdb.Sample
}

type Engine struct {
	db *tsdb.DB
}

func NewEngine(db *tsdb.DB) *Engine {
	return &Engine{db: db}
}

func (e *Engine) EvalInstant(expr string, ts time.Time) (Result, error) {
	p := newParser(expr)
	node, err := p.parseExpr()
	if err != nil {
		return Result{}, fmt.Errorf("parse error: %w", err)
	}
	return e.eval(node, ts, ts)
}

func (e *Engine) EvalRange(expr string, start, end time.Time, step time.Duration) ([]Result, error) {
	p := newParser(expr)
	node, err := p.parseExpr()
	if err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}
	var results []Result
	for t := start; !t.After(end); t = t.Add(step) {
		r, err := e.eval(node, t, t)
		if err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, nil
}

func (e *Engine) eval(node Node, ts, _ time.Time) (Result, error) {
	switch n := node.(type) {
	case *NumberLiteral:
		return Result{Type: ValueScalar, Scalar: n.Value}, nil

	case *VectorSelector:
		return e.evalVectorSelector(n, ts)

	case *MatrixSelector:
		return e.evalMatrixSelector(n, ts)

	case *BinaryExpr:
		return e.evalBinary(n, ts)

	case *AggExpr:
		return e.evalAgg(n, ts)

	case *Call:
		return e.evalCall(n, ts)

	case *ParenExpr:
		return e.eval(n.Expr, ts, ts)

	default:
		return Result{}, fmt.Errorf("unsupported node type: %T", node)
	}
}

func (e *Engine) evalVectorSelector(sel *VectorSelector, ts time.Time) (Result, error) {
	matcher := buildSelectorMatcher(sel.Name, sel.LabelMatchers)
	series := e.db.QueryByMatcher(matcher, 0, ts.UnixMilli())

	var vector Vector
	for _, s := range series {
		samples := s.Range(0, ts.UnixMilli())
		if len(samples) == 0 {
			continue
		}
		latest := samples[len(samples)-1]
		latest.Timestamp = ts.UnixMilli()
		vector = append(vector, Sample{
			Labels: s.Labels,
			Point:  latest,
		})
	}
	return Result{Type: ValueInstantVector, Vector: vector}, nil
}

func (e *Engine) evalMatrixSelector(sel *MatrixSelector, ts time.Time) (Result, error) {
	matcher := buildSelectorMatcher(sel.Name, sel.LabelMatchers)
	mint := ts.Add(-sel.Range).UnixMilli()
	maxt := ts.UnixMilli()
	series := e.db.QueryByMatcher(matcher, mint, maxt)

	var vector Vector
	for _, s := range series {
		samples := s.Range(mint, maxt)
		if len(samples) == 0 {
			continue
		}
		for _, sp := range samples {
			vector = append(vector, Sample{
				Labels: s.Labels,
				Point:  sp,
			})
		}
	}
	return Result{Type: ValueRangeVector, Vector: vector}, nil
}

func (e *Engine) evalBinary(expr *BinaryExpr, ts time.Time) (Result, error) {
	lr, err := e.eval(expr.LHS, ts, ts)
	if err != nil {
		return Result{}, err
	}
	rr, err := e.eval(expr.RHS, ts, ts)
	if err != nil {
		return Result{}, err
	}

	if lr.Type == ValueScalar && rr.Type == ValueScalar {
		return Result{
			Type:   ValueScalar,
			Scalar: applyOp(expr.Op, lr.Scalar, rr.Scalar),
		}, nil
	}

	if lr.Type == ValueScalar && rr.Type == ValueInstantVector {
		for i := range rr.Vector {
			rr.Vector[i].Point.Value = applyOp(expr.Op, lr.Scalar, rr.Vector[i].Point.Value)
		}
		return rr, nil
	}

	if lr.Type == ValueInstantVector && rr.Type == ValueScalar {
		for i := range lr.Vector {
			lr.Vector[i].Point.Value = applyOp(expr.Op, lr.Vector[i].Point.Value, rr.Scalar)
		}
		return lr, nil
	}

	if lr.Type == ValueInstantVector && rr.Type == ValueInstantVector {
		switch expr.Op {
		case "or":
			seen := make(map[string]bool)
			result := make(Vector, 0, len(lr.Vector)+len(rr.Vector))
			for _, sample := range lr.Vector {
				key := labelKey(sample.Labels)
				seen[key] = true
				result = append(result, sample)
			}
			for _, sample := range rr.Vector {
				key := labelKey(sample.Labels)
				if !seen[key] {
					result = append(result, sample)
				}
			}
			return Result{Type: ValueInstantVector, Vector: result}, nil
		case "and", "unless":
			rightMap := make(map[string]Sample)
			for _, s := range rr.Vector {
				rightMap[labelKey(s.Labels)] = s
			}
			var result Vector
			for _, ls := range lr.Vector {
				_, ok := rightMap[labelKey(ls.Labels)]
				if (expr.Op == "and" && ok) || (expr.Op == "unless" && !ok) {
					result = append(result, ls)
				}
			}
			return Result{Type: ValueInstantVector, Vector: result}, nil
		}
		rightMap := make(map[string]Sample)
		for _, s := range rr.Vector {
			rightMap[labelKey(s.Labels)] = s
		}
		var result Vector
		for _, ls := range lr.Vector {
			key := labelKey(ls.Labels)
			rs, ok := rightMap[key]
			if !ok {
				continue
			}
			result = append(result, Sample{
				Labels: ls.Labels,
				Point: tsdb.Sample{
					Timestamp: ls.Point.Timestamp,
					Value:     applyOp(expr.Op, ls.Point.Value, rs.Point.Value),
				},
			})
		}
		return Result{Type: ValueInstantVector, Vector: result}, nil
	}

	return Result{}, fmt.Errorf("unsupported binary op between %d and %d", lr.Type, rr.Type)
}

func (e *Engine) evalAgg(expr *AggExpr, ts time.Time) (Result, error) {
	inner, err := e.eval(expr.Expr, ts, ts)
	if err != nil {
		return Result{}, err
	}
	if inner.Type != ValueInstantVector {
		return Result{}, fmt.Errorf("aggregation requires instant vector")
	}

	groups := make(map[string][]Sample)
	for _, s := range inner.Vector {
		key := groupKey(s.Labels, expr.Grouping)
		groups[key] = append(groups[key], s)
	}

	var result Vector
	for _, group := range groups {
		val := aggFunc(expr.Op, group)
		var labels tsdb.Labels
		if len(expr.Grouping) > 0 {
			labels = filterLabels(group[0].Labels, expr.Grouping)
		}
		result = append(result, Sample{
			Labels: labels,
			Point:  tsdb.Sample{Timestamp: ts.UnixMilli(), Value: val},
		})
	}
	return Result{Type: ValueInstantVector, Vector: result}, nil
}

func (e *Engine) evalCall(call *Call, ts time.Time) (Result, error) {
	switch strings.ToLower(call.Func) {
	case "rate":
		return e.callRate(call, ts, false)
	case "irate":
		return e.callRate(call, ts, true)
	case "increase":
		return e.callIncrease(call, ts)
	case "avg_over_time":
		return e.callAggOverTime(call, ts, avgFunc)
	case "min_over_time":
		return e.callAggOverTime(call, ts, minFunc)
	case "max_over_time":
		return e.callAggOverTime(call, ts, maxFunc)
	case "sum_over_time":
		return e.callAggOverTime(call, ts, sumFunc)
	case "count_over_time":
		return e.callAggOverTime(call, ts, func(vals []float64) float64 {
			return float64(len(vals))
		})
	case "last_over_time":
		return e.callAggOverTime(call, ts, func(vals []float64) float64 {
			if len(vals) == 0 {
				return 0
			}
			return vals[len(vals)-1]
		})
	case "abs":
		return e.applyUnary(call, ts, math.Abs)
	case "ceil":
		return e.applyUnary(call, ts, math.Ceil)
	case "floor":
		return e.applyUnary(call, ts, math.Floor)
	case "round":
		return e.applyUnary(call, ts, math.Round)
	case "sqrt":
		return e.applyUnary(call, ts, math.Sqrt)
	case "ln":
		return e.applyUnary(call, ts, math.Log)
	case "log2":
		return e.applyUnary(call, ts, math.Log2)
	case "log10":
		return e.applyUnary(call, ts, math.Log10)
	case "exp":
		return e.applyUnary(call, ts, math.Exp)
	case "clamp_min":
		return e.applyBinaryScalar(call, ts, func(v, min float64) float64 {
			if v < min {
				return min
			}
			return v
		})
	case "clamp_max":
		return e.applyBinaryScalar(call, ts, func(v, max float64) float64 {
			if v > max {
				return max
			}
			return v
		})
	case "delta":
		return e.callDelta(call, ts)
	case "deriv":
		return e.callDeriv(call, ts)
	case "resets":
		return e.callResets(call, ts)
	case "changes":
		return e.callChanges(call, ts)
	case "absent":
		return e.callAbsent(call, ts)
	case "vector":
		if len(call.Args) == 0 {
			return Result{}, fmt.Errorf("vector requires scalar argument")
		}
		arg, err := e.eval(call.Args[0], ts, ts)
		if err != nil {
			return Result{}, err
		}
		value := arg.Scalar
		if arg.Type == ValueInstantVector && len(arg.Vector) > 0 {
			value = arg.Vector[0].Point.Value
		}
		return Result{
			Type: ValueInstantVector,
			Vector: Vector{{
				Labels: nil,
				Point:  tsdb.Sample{Timestamp: ts.UnixMilli(), Value: value},
			}},
		}, nil
	case "scalar":
		if len(call.Args) == 0 {
			return Result{}, fmt.Errorf("scalar requires argument")
		}
		arg, err := e.eval(call.Args[0], ts, ts)
		if err != nil {
			return Result{}, err
		}
		if arg.Type == ValueScalar {
			return arg, nil
		}
		if len(arg.Vector) == 1 {
			return Result{Type: ValueScalar, Scalar: arg.Vector[0].Point.Value}, nil
		}
		return Result{Type: ValueScalar, Scalar: math.NaN()}, nil
	case "time":
		return Result{Type: ValueScalar, Scalar: float64(ts.Unix())}, nil
	case "timestamp":
		if len(call.Args) == 0 {
			return Result{}, fmt.Errorf("timestamp requires argument")
		}
		inner, err := e.eval(call.Args[0], ts, ts)
		if err != nil {
			return Result{}, err
		}
		for i := range inner.Vector {
			inner.Vector[i].Point.Value = float64(inner.Vector[i].Point.Timestamp) / 1000
		}
		return inner, nil
	case "sort", "sort_desc":
		if len(call.Args) == 0 {
			return Result{}, fmt.Errorf("sort requires argument")
		}
		inner, err := e.eval(call.Args[0], ts, ts)
		if err != nil {
			return Result{}, err
		}
		sort.Slice(inner.Vector, func(i, j int) bool {
			if strings.EqualFold(call.Func, "sort_desc") {
				return inner.Vector[i].Point.Value > inner.Vector[j].Point.Value
			}
			return inner.Vector[i].Point.Value < inner.Vector[j].Point.Value
		})
		return inner, nil
	case "histogram_quantile":
		return e.callHistogramQuantile(call, ts)
	default:
		return Result{}, fmt.Errorf("unknown function: %s", call.Func)
	}
}

func (e *Engine) callRate(call *Call, ts time.Time, isIrate bool) (Result, error) {
	if len(call.Args) == 0 {
		return Result{}, fmt.Errorf("rate requires a range vector argument")
	}
	matrix, err := e.eval(call.Args[0], ts, ts)
	if err != nil {
		return Result{}, err
	}

	grouped := groupByHash(matrix.Vector)
	var result Vector
	for _, samples := range grouped {
		sort.Slice(samples, func(i, j int) bool {
			return samples[i].Point.Timestamp < samples[j].Point.Timestamp
		})
		if len(samples) < 2 {
			continue
		}
		var val float64
		if isIrate {
			last := samples[len(samples)-1]
			prev := samples[len(samples)-2]
			dt := float64(last.Point.Timestamp-prev.Point.Timestamp) / 1000.0
			if dt == 0 {
				continue
			}
			val = (last.Point.Value - prev.Point.Value) / dt
		} else {
			first := samples[0]
			last := samples[len(samples)-1]
			dt := float64(last.Point.Timestamp-first.Point.Timestamp) / 1000.0
			if dt == 0 {
				continue
			}
			val = (last.Point.Value - first.Point.Value) / dt
		}
		if val < 0 {
			val = 0
		}
		result = append(result, Sample{
			Labels: samples[0].Labels,
			Point:  tsdb.Sample{Timestamp: ts.UnixMilli(), Value: val},
		})
	}
	return Result{Type: ValueInstantVector, Vector: result}, nil
}

func (e *Engine) callIncrease(call *Call, ts time.Time) (Result, error) {
	if len(call.Args) == 0 {
		return Result{}, fmt.Errorf("increase requires a range vector argument")
	}
	matrix, err := e.eval(call.Args[0], ts, ts)
	if err != nil {
		return Result{}, err
	}
	grouped := groupByHash(matrix.Vector)
	var result Vector
	for _, samples := range grouped {
		sort.Slice(samples, func(i, j int) bool {
			return samples[i].Point.Timestamp < samples[j].Point.Timestamp
		})
		if len(samples) < 2 {
			continue
		}
		first := samples[0]
		last := samples[len(samples)-1]
		val := last.Point.Value - first.Point.Value
		if val < 0 {
			val = last.Point.Value
		}
		result = append(result, Sample{
			Labels: samples[0].Labels,
			Point:  tsdb.Sample{Timestamp: ts.UnixMilli(), Value: val},
		})
	}
	return Result{Type: ValueInstantVector, Vector: result}, nil
}

func (e *Engine) callAggOverTime(call *Call, ts time.Time, fn func([]float64) float64) (Result, error) {
	if len(call.Args) == 0 {
		return Result{}, fmt.Errorf("agg_over_time requires a range vector argument")
	}
	matrix, err := e.eval(call.Args[0], ts, ts)
	if err != nil {
		return Result{}, err
	}
	grouped := groupByHash(matrix.Vector)
	var result Vector
	for _, samples := range grouped {
		vals := make([]float64, len(samples))
		for i, s := range samples {
			vals[i] = s.Point.Value
		}
		result = append(result, Sample{
			Labels: samples[0].Labels,
			Point:  tsdb.Sample{Timestamp: ts.UnixMilli(), Value: fn(vals)},
		})
	}
	return Result{Type: ValueInstantVector, Vector: result}, nil
}

func (e *Engine) applyUnary(call *Call, ts time.Time, fn func(float64) float64) (Result, error) {
	if len(call.Args) == 0 {
		return Result{}, fmt.Errorf("unary function requires argument")
	}
	inner, err := e.eval(call.Args[0], ts, ts)
	if err != nil {
		return Result{}, err
	}
	if inner.Type == ValueScalar {
		return Result{Type: ValueScalar, Scalar: fn(inner.Scalar)}, nil
	}
	for i := range inner.Vector {
		inner.Vector[i].Point.Value = fn(inner.Vector[i].Point.Value)
	}
	return inner, nil
}

func (e *Engine) applyBinaryScalar(call *Call, ts time.Time, fn func(float64, float64) float64) (Result, error) {
	if len(call.Args) < 2 {
		return Result{}, fmt.Errorf("function requires 2 arguments")
	}
	inner, err := e.eval(call.Args[0], ts, ts)
	if err != nil {
		return Result{}, err
	}
	arg2, err := e.eval(call.Args[1], ts, ts)
	if err != nil {
		return Result{}, err
	}
	scalarVal := arg2.Scalar
	if inner.Type == ValueScalar {
		return Result{Type: ValueScalar, Scalar: fn(inner.Scalar, scalarVal)}, nil
	}
	for i := range inner.Vector {
		inner.Vector[i].Point.Value = fn(inner.Vector[i].Point.Value, scalarVal)
	}
	return inner, nil
}

func (e *Engine) callDelta(call *Call, ts time.Time) (Result, error) {
	if len(call.Args) == 0 {
		return Result{}, fmt.Errorf("delta requires argument")
	}
	matrix, err := e.eval(call.Args[0], ts, ts)
	if err != nil {
		return Result{}, err
	}
	grouped := groupByHash(matrix.Vector)
	var result Vector
	for _, samples := range grouped {
		sort.Slice(samples, func(i, j int) bool {
			return samples[i].Point.Timestamp < samples[j].Point.Timestamp
		})
		if len(samples) < 2 {
			continue
		}
		val := samples[len(samples)-1].Point.Value - samples[0].Point.Value
		result = append(result, Sample{
			Labels: samples[0].Labels,
			Point:  tsdb.Sample{Timestamp: ts.UnixMilli(), Value: val},
		})
	}
	return Result{Type: ValueInstantVector, Vector: result}, nil
}

func (e *Engine) callDeriv(call *Call, ts time.Time) (Result, error) {
	return e.callRate(call, ts, false)
}

func (e *Engine) callResets(call *Call, ts time.Time) (Result, error) {
	if len(call.Args) == 0 {
		return Result{}, fmt.Errorf("resets requires argument")
	}
	matrix, err := e.eval(call.Args[0], ts, ts)
	if err != nil {
		return Result{}, err
	}
	grouped := groupByHash(matrix.Vector)
	var result Vector
	for _, samples := range grouped {
		sort.Slice(samples, func(i, j int) bool {
			return samples[i].Point.Timestamp < samples[j].Point.Timestamp
		})
		count := 0.0
		for i := 1; i < len(samples); i++ {
			if samples[i].Point.Value < samples[i-1].Point.Value {
				count++
			}
		}
		result = append(result, Sample{
			Labels: samples[0].Labels,
			Point:  tsdb.Sample{Timestamp: ts.UnixMilli(), Value: count},
		})
	}
	return Result{Type: ValueInstantVector, Vector: result}, nil
}

func (e *Engine) callChanges(call *Call, ts time.Time) (Result, error) {
	if len(call.Args) == 0 {
		return Result{}, fmt.Errorf("changes requires argument")
	}
	matrix, err := e.eval(call.Args[0], ts, ts)
	if err != nil {
		return Result{}, err
	}
	grouped := groupByHash(matrix.Vector)
	var result Vector
	for _, samples := range grouped {
		sort.Slice(samples, func(i, j int) bool {
			return samples[i].Point.Timestamp < samples[j].Point.Timestamp
		})
		count := 0.0
		for i := 1; i < len(samples); i++ {
			if samples[i].Point.Value != samples[i-1].Point.Value {
				count++
			}
		}
		result = append(result, Sample{
			Labels: samples[0].Labels,
			Point:  tsdb.Sample{Timestamp: ts.UnixMilli(), Value: count},
		})
	}
	return Result{Type: ValueInstantVector, Vector: result}, nil
}

func (e *Engine) callAbsent(call *Call, ts time.Time) (Result, error) {
	if len(call.Args) == 0 {
		return Result{}, fmt.Errorf("absent requires argument")
	}
	inner, err := e.eval(call.Args[0], ts, ts)
	if err != nil {
		return Result{}, err
	}
	if inner.Type == ValueInstantVector && len(inner.Vector) == 0 {
		return Result{
			Type: ValueInstantVector,
			Vector: Vector{{
				Labels: nil,
				Point:  tsdb.Sample{Timestamp: ts.UnixMilli(), Value: 1},
			}},
		}, nil
	}
	return Result{Type: ValueInstantVector, Vector: nil}, nil
}

func (e *Engine) callHistogramQuantile(call *Call, ts time.Time) (Result, error) {
	if len(call.Args) < 2 {
		return Result{}, fmt.Errorf("histogram_quantile requires quantile and bucket vector arguments")
	}
	quantileResult, err := e.eval(call.Args[0], ts, ts)
	if err != nil {
		return Result{}, err
	}
	q := quantileResult.Scalar
	if quantileResult.Type == ValueInstantVector && len(quantileResult.Vector) > 0 {
		q = quantileResult.Vector[0].Point.Value
	}
	if math.IsNaN(q) || q < 0 || q > 1 {
		return Result{Type: ValueInstantVector, Vector: nil}, nil
	}
	buckets, err := e.eval(call.Args[1], ts, ts)
	if err != nil {
		return Result{}, err
	}
	type bucket struct {
		upper float64
		value float64
	}
	grouped := make(map[string][]bucket)
	groupLabels := make(map[string]tsdb.Labels)
	for _, sample := range buckets.Vector {
		le := sample.Labels.Get("le")
		if le == "" {
			continue
		}
		upper, err := strconv.ParseFloat(le, 64)
		if err != nil {
			if strings.EqualFold(le, "+Inf") || strings.EqualFold(le, "Inf") {
				upper = math.Inf(1)
			} else {
				continue
			}
		}
		labels := labelsWithout(sample.Labels, "le")
		key := labelKey(labels)
		grouped[key] = append(grouped[key], bucket{upper: upper, value: sample.Point.Value})
		groupLabels[key] = labels
	}
	var result Vector
	for key, group := range grouped {
		sort.Slice(group, func(i, j int) bool { return group[i].upper < group[j].upper })
		if len(group) == 0 {
			continue
		}
		total := group[len(group)-1].value
		if total <= 0 {
			continue
		}
		rank := q * total
		prevUpper := 0.0
		prevValue := 0.0
		value := group[len(group)-1].upper
		for _, item := range group {
			if item.value >= rank {
				if math.IsInf(item.upper, 1) {
					value = prevUpper
				} else if item.value <= prevValue {
					value = item.upper
				} else {
					fraction := (rank - prevValue) / (item.value - prevValue)
					value = prevUpper + (item.upper-prevUpper)*fraction
				}
				break
			}
			prevUpper = item.upper
			prevValue = item.value
		}
		result = append(result, Sample{
			Labels: groupLabels[key],
			Point:  tsdb.Sample{Timestamp: ts.UnixMilli(), Value: value},
		})
	}
	return Result{Type: ValueInstantVector, Vector: result}, nil
}

func buildSelectorMatcher(name string, matchers []LabelMatcher) tsdb.MultiMatcher {
	if strings.TrimSpace(name) == "" {
		return buildMatcher(matchers)
	}
	next := make([]LabelMatcher, 0, len(matchers)+1)
	next = append(next, LabelMatcher{Type: MatchEqual, Name: "__name__", Value: name})
	next = append(next, matchers...)
	return buildMatcher(next)
}

func buildMatcher(matchers []LabelMatcher) tsdb.MultiMatcher {
	var mm tsdb.MultiMatcher
	for _, m := range matchers {
		switch m.Type {
		case MatchEqual:
			mm.Matchers = append(mm.Matchers, tsdb.EqualMatcher{Name: m.Name, Value: m.Value})
		case MatchNotEqual:
			mm.Matchers = append(mm.Matchers, tsdb.NotEqualMatcher{Name: m.Name, Value: m.Value})
		case MatchRegex:
			mm.Matchers = append(mm.Matchers, tsdb.RegexMatcher{
				Name:  m.Name,
				Regex: &tsdb.Regexp{Pattern: m.Value},
			})
		case MatchNotRegex:
			mm.Matchers = append(mm.Matchers, tsdb.NotRegexMatcher{
				Name:  m.Name,
				Regex: &tsdb.Regexp{Pattern: m.Value},
			})
		}
	}
	return mm
}

func groupByHash(vector Vector) map[uint64][]Sample {
	groups := make(map[uint64][]Sample)
	for _, s := range vector {
		h := s.Labels.Hash()
		groups[h] = append(groups[h], s)
	}
	return groups
}

func groupKey(labels tsdb.Labels, grouping []string) string {
	if len(grouping) == 0 {
		return "__all__"
	}
	parts := make([]string, 0, len(grouping))
	for _, g := range grouping {
		v := labels.Get(g)
		parts = append(parts, g+"="+v)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func filterLabels(labels tsdb.Labels, keep []string) tsdb.Labels {
	var result tsdb.Labels
	for _, l := range labels {
		for _, k := range keep {
			if l.Name == k {
				result = append(result, l)
				break
			}
		}
	}
	return result
}

func labelsWithout(labels tsdb.Labels, drop ...string) tsdb.Labels {
	dropped := make(map[string]bool, len(drop))
	for _, name := range drop {
		dropped[name] = true
	}
	result := make(tsdb.Labels, 0, len(labels))
	for _, label := range labels {
		if !dropped[label.Name] {
			result = append(result, label)
		}
	}
	return result
}

func labelKey(labels tsdb.Labels) string {
	parts := make([]string, len(labels))
	for i, l := range labels {
		parts[i] = l.Name + "=" + l.Value
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func applyOp(op string, a, b float64) float64 {
	switch op {
	case "+":
		return a + b
	case "-":
		return a - b
	case "*":
		return a * b
	case "/":
		if b == 0 {
			return math.NaN()
		}
		return a / b
	case "%":
		if b == 0 {
			return math.NaN()
		}
		return math.Mod(a, b)
	case "^":
		return math.Pow(a, b)
	case "==":
		if a == b {
			return 1
		}
		return 0
	case "!=":
		if a != b {
			return 1
		}
		return 0
	case ">":
		if a > b {
			return 1
		}
		return 0
	case "<":
		if a < b {
			return 1
		}
		return 0
	case ">=":
		if a >= b {
			return 1
		}
		return 0
	case "<=":
		if a <= b {
			return 1
		}
		return 0
	default:
		return math.NaN()
	}
}

func aggFunc(op string, samples []Sample) float64 {
	vals := make([]float64, len(samples))
	for i, s := range samples {
		vals[i] = s.Point.Value
	}
	switch strings.ToLower(op) {
	case "sum":
		return sumFunc(vals)
	case "avg", "average":
		return avgFunc(vals)
	case "min":
		return minFunc(vals)
	case "max":
		return maxFunc(vals)
	case "count":
		return float64(len(vals))
	case "stddev":
		return stddevFunc(vals)
	case "stdvar":
		return stdvarFunc(vals)
	case "topk":
		if len(vals) > 0 {
			sort.Float64s(vals)
			return vals[len(vals)-1]
		}
		return 0
	case "bottomk":
		if len(vals) > 0 {
			sort.Float64s(vals)
			return vals[0]
		}
		return 0
	case "group":
		if len(vals) > 0 {
			return 1
		}
		return 0
	default:
		return sumFunc(vals)
	}
}

func sumFunc(vals []float64) float64 {
	s := 0.0
	for _, v := range vals {
		s += v
	}
	return s
}

func avgFunc(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	return sumFunc(vals) / float64(len(vals))
}

func minFunc(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	m := math.Inf(1)
	for _, v := range vals {
		if v < m {
			m = v
		}
	}
	return m
}

func maxFunc(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	m := math.Inf(-1)
	for _, v := range vals {
		if v > m {
			m = v
		}
	}
	return m
}

func stddevFunc(vals []float64) float64 {
	return math.Sqrt(stdvarFunc(vals))
}

func stdvarFunc(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	avg := avgFunc(vals)
	sum := 0.0
	for _, v := range vals {
		d := v - avg
		sum += d * d
	}
	return sum / float64(len(vals))
}

func parsePromDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}

	var total time.Duration
	i := 0
	for i < len(s) {
		start := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if start == i {
			return 0, fmt.Errorf("invalid duration: %s", s)
		}
		val, err := strconv.ParseFloat(s[start:i], 64)
		if err != nil {
			return 0, err
		}
		unitStart := i
		for i < len(s) && (s[i] < '0' || s[i] > '9') {
			i++
		}
		unit := s[unitStart:i]
		switch unit {
		case "ms":
			total += time.Duration(val * float64(time.Millisecond))
		case "s":
			total += time.Duration(val * float64(time.Second))
		case "m":
			total += time.Duration(val * float64(time.Minute))
		case "h":
			total += time.Duration(val * float64(time.Hour))
		case "d":
			total += time.Duration(val * float64(24*time.Hour))
		case "w":
			total += time.Duration(val * float64(7*24*time.Hour))
		case "y":
			total += time.Duration(val * float64(365*24*time.Hour))
		default:
			return 0, fmt.Errorf("unknown duration unit: %s", unit)
		}
	}
	return total, nil
}
