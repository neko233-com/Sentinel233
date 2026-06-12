package promql

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Node interface{}

type NumberLiteral struct {
	Value float64
}

type VectorSelector struct {
	Name          string
	LabelMatchers []LabelMatcher
	Offset        int64
}

type MatrixSelector struct {
	Name          string
	LabelMatchers []LabelMatcher
	Range         time.Duration
	Offset        int64
}

type BinaryExpr struct {
	Op  string
	LHS Node
	RHS Node
}

type AggExpr struct {
	Op       string
	Expr     Node
	Grouping []string
	Without  bool
}

type Call struct {
	Func string
	Args []Node
}

type ParenExpr struct {
	Expr Node
}

type LabelMatcherType int

const (
	MatchEqual LabelMatcherType = iota
	MatchNotEqual
	MatchRegex
	MatchNotRegex
)

type LabelMatcher struct {
	Type  LabelMatcherType
	Name  string
	Value string
}

type parser struct {
	input string
	pos   int
}

func newParser(input string) *parser {
	return &parser{input: strings.TrimSpace(input), pos: 0}
}

func (p *parser) parseExpr() (Node, error) {
	node, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	node, err = p.parseBinaryRHS(node, 0)
	if err != nil {
		return nil, err
	}
	return node, nil
}

func (p *parser) parseUnary() (Node, error) {
	p.skipSpace()
	if p.pos >= len(p.input) {
		return nil, fmt.Errorf("unexpected end of expression")
	}

	// unary minus
	if p.input[p.pos] == '-' {
		p.pos++
		inner, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		if nl, ok := inner.(*NumberLiteral); ok {
			return &NumberLiteral{Value: -nl.Value}, nil
		}
		return &BinaryExpr{Op: "*", LHS: &NumberLiteral{Value: -1}, RHS: inner}, nil
	}

	return p.parsePrimary()
}

func (p *parser) parsePrimary() (Node, error) {
	p.skipSpace()
	if p.pos >= len(p.input) {
		return nil, fmt.Errorf("unexpected end of expression")
	}

	ch := p.input[p.pos]

	// parenthesized expression
	if ch == '(' {
		p.pos++
		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if p.pos < len(p.input) && p.input[p.pos] == ')' {
			p.pos++
		}
		return &ParenExpr{Expr: inner}, nil
	}

	// number literal
	if (ch >= '0' && ch <= '9') || ch == '.' {
		return p.parseNumber()
	}

	// keyword or metric name
	word := p.readWord()
	switch strings.ToLower(word) {
	case "sum", "avg", "min", "max", "count", "stddev", "stdvar", "topk", "bottomk", "group":
		return p.parseAgg(word)
	case "by", "without":
		return nil, fmt.Errorf("unexpected keyword %q", word)
	default:
		if word == "" {
			return nil, fmt.Errorf("unexpected character %q at position %d", string(ch), p.pos)
		}
		// Check if this is a function call
		if isFunction(word) {
			return p.parseCall(word)
		}
		return p.parseMetricSelector(word)
	}
}

func (p *parser) parseNumber() (Node, error) {
	start := p.pos
	for p.pos < len(p.input) {
		ch := p.input[p.pos]
		if (ch >= '0' && ch <= '9') || ch == '.' || ch == 'e' || ch == 'E' || ch == '+' || ch == '-' {
			p.pos++
			// handle scientific notation
			if (ch == 'e' || ch == 'E') && p.pos < len(p.input) && (p.input[p.pos] == '+' || p.input[p.pos] == '-') {
				p.pos++
			}
		} else {
			break
		}
	}
	val, err := strconv.ParseFloat(p.input[start:p.pos], 64)
	if err != nil {
		return nil, fmt.Errorf("invalid number: %s", p.input[start:p.pos])
	}
	// handle unit suffix
	if p.pos < len(p.input) {
		suffix := p.input[p.pos:]
		switch {
		case strings.HasPrefix(suffix, "ms"):
			val *= 0.001
			p.pos += 2
		case strings.HasPrefix(suffix, "s"):
			val *= 1
			p.pos += 1
		case strings.HasPrefix(suffix, "m"):
			val *= 60
			p.pos += 1
		case strings.HasPrefix(suffix, "h"):
			val *= 3600
			p.pos += 1
		case strings.HasPrefix(suffix, "d"):
			val *= 86400
			p.pos += 1
		case strings.HasPrefix(suffix, "w"):
			val *= 604800
			p.pos += 1
		case strings.HasPrefix(suffix, "y"):
			val *= 31536000
			p.pos += 1
		}
	}
	return &NumberLiteral{Value: val}, nil
}

func (p *parser) parseMetricSelector(name string) (Node, error) {
	p.skipSpace()

	if p.pos >= len(p.input) || p.input[p.pos] != '{' {
		// bare metric name with no labels
		// check for [range] for matrix selector
		p.skipSpace()
		if p.pos < len(p.input) && p.input[p.pos] == '[' {
			return p.parseMatrixSelector(name, nil)
		}
		return &VectorSelector{Name: name}, nil
	}

	matchers, err := p.parseLabelMatchers()
	if err != nil {
		return nil, err
	}

	// check for [range]
	p.skipSpace()
	if p.pos < len(p.input) && p.input[p.pos] == '[' {
		return p.parseMatrixSelector(name, matchers)
	}

	return &VectorSelector{Name: name, LabelMatchers: matchers}, nil
}

func (p *parser) parseLabelMatchers() ([]LabelMatcher, error) {
	if p.input[p.pos] != '{' {
		return nil, fmt.Errorf("expected '{'")
	}
	p.pos++

	var matchers []LabelMatcher
	for {
		p.skipSpace()
		if p.pos < len(p.input) && p.input[p.pos] == '}' {
			p.pos++
			break
		}
		if len(matchers) > 0 {
			if p.pos < len(p.input) && p.input[p.pos] == ',' {
				p.pos++
			}
		}
		m, err := p.parseLabelMatcher()
		if err != nil {
			return nil, err
		}
		matchers = append(matchers, *m)
	}
	return matchers, nil
}

func (p *parser) parseLabelMatcher() (*LabelMatcher, error) {
	p.skipSpace()
	name := p.readIdent()
	if name == "" {
		return nil, fmt.Errorf("expected label name")
	}
	p.skipSpace()

	m := &LabelMatcher{Name: name}

	if p.pos+1 < len(p.input) {
		two := p.input[p.pos : p.pos+2]
		if two == "=~" {
			m.Type = MatchRegex
			p.pos += 2
		} else if two == "!~" {
			m.Type = MatchNotRegex
			p.pos += 2
		} else if p.input[p.pos] == '=' && (p.pos+1 >= len(p.input) || p.input[p.pos+1] != '=') {
			m.Type = MatchEqual
			p.pos++
		} else if p.input[p.pos] == '!' && p.pos+1 < len(p.input) && p.input[p.pos+1] == '=' {
			m.Type = MatchNotEqual
			p.pos += 2
		} else {
			return nil, fmt.Errorf("expected matcher operator")
		}
	} else if p.pos < len(p.input) && p.input[p.pos] == '=' {
		m.Type = MatchEqual
		p.pos++
	} else {
		return nil, fmt.Errorf("expected matcher operator")
	}

	p.skipSpace()
	val, err := p.parseString()
	if err != nil {
		return nil, err
	}
	m.Value = val
	return m, nil
}

func (p *parser) parseString() (string, error) {
	if p.pos >= len(p.input) {
		return "", fmt.Errorf("unexpected end of expression")
	}
	quote := p.input[p.pos]
	if quote != '"' && quote != '\'' {
		return "", fmt.Errorf("expected string, got %q", string(quote))
	}
	p.pos++
	var builder strings.Builder
	escaped := false
	for p.pos < len(p.input) {
		ch := p.input[p.pos]
		p.pos++
		if escaped {
			builder.WriteByte(ch)
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == quote {
			return builder.String(), nil
		}
		builder.WriteByte(ch)
	}
	return builder.String(), fmt.Errorf("unterminated string")
}

func (p *parser) parseMatrixSelector(name string, matchers []LabelMatcher) (Node, error) {
	if p.input[p.pos] != '[' {
		return nil, fmt.Errorf("expected '['")
	}
	p.pos++
	start := p.pos
	for p.pos < len(p.input) && p.input[p.pos] != ']' {
		p.pos++
	}
	if p.pos >= len(p.input) {
		return nil, fmt.Errorf("unterminated range")
	}
	rangeStr := p.input[start:p.pos]
	p.pos++

	dur, err := parsePromDuration(rangeStr)
	if err != nil {
		return nil, fmt.Errorf("invalid range: %w", err)
	}
	return &MatrixSelector{
		Name:          name,
		LabelMatchers: matchers,
		Range:         dur,
	}, nil
}

func (p *parser) parseAgg(op string) (Node, error) {
	p.skipSpace()

	// optional by()/without() grouping
	var grouping []string
	without := false
	if p.pos < len(p.input) {
		nextWord := p.peekWord()
		switch nextWord {
		case "by":
			p.pos += 2
			p.skipSpace()
			grouping, _ = p.parseGrouping()
		case "without":
			p.pos += 7
			p.skipSpace()
			without = true
			grouping, _ = p.parseGrouping()
		}
	}

	p.skipSpace()
	if p.pos >= len(p.input) || p.input[p.pos] != '(' {
		return nil, fmt.Errorf("expected '(' after aggregation")
	}
	p.pos++

	inner, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.pos < len(p.input) && p.input[p.pos] == ')' {
		p.pos++
	}

	// handle topk/bottomk with extra arg
	_ = without

	return &AggExpr{
		Op:       op,
		Expr:     inner,
		Grouping: grouping,
	}, nil
}

func (p *parser) parseGrouping() ([]string, error) {
	if p.pos >= len(p.input) || p.input[p.pos] != '(' {
		return nil, fmt.Errorf("expected '('")
	}
	p.pos++
	var groups []string
	for {
		p.skipSpace()
		if p.pos < len(p.input) && p.input[p.pos] == ')' {
			p.pos++
			break
		}
		if len(groups) > 0 {
			if p.pos < len(p.input) && p.input[p.pos] == ',' {
				p.pos++
			}
		}
		p.skipSpace()
		name := p.readIdent()
		if name == "" {
			break
		}
		groups = append(groups, name)
	}
	return groups, nil
}

func (p *parser) parseBinaryRHS(lhs Node, minPrec int) (Node, error) {
	for {
		p.skipSpace()
		if p.pos >= len(p.input) {
			return lhs, nil
		}
		op := p.peekOp()
		if op == "" {
			return lhs, nil
		}
		prec := opPrecedence(op)
		if prec < minPrec {
			return lhs, nil
		}
		p.pos += len(op)
		// skip optional "bool"
		p.skipSpace()
		if p.pos+4 <= len(p.input) && p.input[p.pos:p.pos+4] == "bool" {
			p.pos += 4
		}

		rhs, err := p.parseUnary()
		if err != nil {
			return nil, err
		}

		// right-associative for ^
		nextPrec := prec
		if op == "^" {
			nextPrec = prec - 1
		}

		rhs, err = p.parseBinaryRHS(rhs, nextPrec+1)
		if err != nil {
			return nil, err
		}

		lhs = &BinaryExpr{Op: op, LHS: lhs, RHS: rhs}
	}
}

func (p *parser) peekOp() string {
	if p.pos >= len(p.input) {
		return ""
	}
	rest := p.input[p.pos:]
	for _, op := range []string{"^", "*", "/", "%", "+", "-", "==", "!=", ">=", "<=", ">", "<", "and", "or", "unless"} {
		if strings.HasPrefix(rest, op) {
			// word operators need space boundaries
			if op == "and" || op == "or" || op == "unless" {
				end := p.pos + len(op)
				if end < len(p.input) && isIdentChar(p.input[end]) {
					return ""
				}
			}
			return op
		}
	}
	return ""
}

func opPrecedence(op string) int {
	switch op {
	case "^":
		return 4
	case "*", "/", "%":
		return 3
	case "+", "-":
		return 2
	case "==", "!=", ">", "<", ">=", "<=":
		return 1
	case "and":
		return 0
	case "or":
		return -1
	case "unless":
		return -1
	default:
		return 0
	}
}

func (p *parser) skipSpace() {
	for p.pos < len(p.input) && (p.input[p.pos] == ' ' || p.input[p.pos] == '\t' || p.input[p.pos] == '\n') {
		p.pos++
	}
}

func (p *parser) readWord() string {
	start := p.pos
	for p.pos < len(p.input) && isIdentChar(p.input[p.pos]) {
		p.pos++
	}
	return p.input[start:p.pos]
}

func (p *parser) readIdent() string {
	start := p.pos
	for p.pos < len(p.input) && isIdentChar(p.input[p.pos]) {
		p.pos++
	}
	return p.input[start:p.pos]
}

func (p *parser) peekWord() string {
	saved := p.pos
	word := p.readWord()
	p.pos = saved
	return word
}

func isIdentChar(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == ':'
}

var knownFunctions = map[string]bool{
	"rate": true, "irate": true, "increase": true,
	"avg_over_time": true, "min_over_time": true, "max_over_time": true,
	"sum_over_time": true, "count_over_time": true, "last_over_time": true,
	"abs": true, "ceil": true, "floor": true, "round": true,
	"sqrt": true, "ln": true, "log2": true, "log10": true, "exp": true,
	"clamp_min": true, "clamp_max": true,
	"delta": true, "deriv": true, "resets": true, "changes": true,
	"absent": true, "vector": true, "scalar": true,
	"sort": true, "sort_desc": true,
	"label_replace": true, "label_join": true,
	"timestamp": true, "time": true,
}

func isFunction(name string) bool {
	return knownFunctions[strings.ToLower(name)]
}

func (p *parser) parseCall(funcName string) (Node, error) {
	p.skipSpace()
	if p.pos >= len(p.input) || p.input[p.pos] != '(' {
		return nil, fmt.Errorf("expected '(' after function name %q", funcName)
	}
	p.pos++

	var args []Node
	for {
		p.skipSpace()
		if p.pos < len(p.input) && p.input[p.pos] == ')' {
			p.pos++
			break
		}
		if len(args) > 0 {
			if p.pos < len(p.input) && p.input[p.pos] == ',' {
				p.pos++
			}
		}
		arg, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
	}
	return &Call{Func: funcName, Args: args}, nil
}
