package api

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/neko233-com/Sentinel233/internal/promql"
	"github.com/neko233-com/Sentinel233/internal/tsdb"
)

func requestParam(r *http.Request, key string) string {
	if value := r.URL.Query().Get(key); value != "" {
		return value
	}
	if err := r.ParseForm(); err == nil {
		return r.Form.Get(key)
	}
	return ""
}

func requestParams(r *http.Request, key string) []string {
	values := append([]string{}, r.URL.Query()[key]...)
	if err := r.ParseForm(); err == nil {
		values = append(values, r.PostForm[key]...)
	}
	if key == "match[]" {
		values = append(values, r.URL.Query()["match"]...)
		if err := r.ParseForm(); err == nil {
			values = append(values, r.PostForm["match"]...)
		}
	}
	return values
}

func (s *Server) filteredSeries(r *http.Request) []*tsdb.Series {
	if s.db == nil {
		return nil
	}
	series := s.db.AllSeries()
	matchers := requestParams(r, "match[]")
	if len(matchers) == 0 {
		return series
	}
	filtered := make([]*tsdb.Series, 0, len(series))
	for _, item := range series {
		for _, expr := range matchers {
			if seriesMatchesSelector(item.Labels, expr) {
				filtered = append(filtered, item)
				break
			}
		}
	}
	return filtered
}

func (s *Server) handleLabels(w http.ResponseWriter, r *http.Request) {
	seen := map[string]bool{}
	for _, ser := range s.filteredSeries(r) {
		for _, label := range ser.Labels {
			seen[label.Name] = true
		}
	}
	labels := make([]string, 0, len(seen))
	for label := range seen {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": labels})
}

func (s *Server) handleMetadata(w http.ResponseWriter, r *http.Request) {
	metricFilter := requestParam(r, "metric")
	limit := intFromParam(requestParam(r, "limit"), 0)
	data := map[string][]map[string]interface{}{}
	for _, ser := range s.filteredSeries(r) {
		name := ser.Labels.Get("__name__")
		if name == "" || (metricFilter != "" && name != metricFilter) {
			continue
		}
		if _, ok := data[name]; ok {
			continue
		}
		data[name] = []map[string]interface{}{{
			"type": inferMetricType(name, ser.Labels),
			"help": "",
			"unit": inferMetricUnit(name, ser.Labels),
		}}
		if limit > 0 && len(data) >= limit {
			break
		}
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": data})
}

func (s *Server) handleTargetsMetadata(w http.ResponseWriter, r *http.Request) {
	metricFilter := requestParam(r, "metric")
	var data []map[string]interface{}
	for _, ser := range s.filteredSeries(r) {
		name := ser.Labels.Get("__name__")
		if name == "" || (metricFilter != "" && name != metricFilter) {
			continue
		}
		data = append(data, map[string]interface{}{
			"target": map[string]interface{}{
				"job":      ser.Labels.Get("job"),
				"instance": ser.Labels.Get("instance"),
			},
			"metric": name,
			"type":   inferMetricType(name, ser.Labels),
			"help":   "",
			"unit":   inferMetricUnit(name, ser.Labels),
		})
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": data})
}

func (s *Server) handleStatusTSDB(w http.ResponseWriter, r *http.Request) {
	series := s.filteredSeries(r)
	byMetric := map[string]int{}
	labelValues := map[string]map[string]bool{}
	for _, ser := range series {
		name := ser.Labels.Get("__name__")
		if name != "" {
			byMetric[name]++
		}
		for _, label := range ser.Labels {
			if labelValues[label.Name] == nil {
				labelValues[label.Name] = map[string]bool{}
			}
			labelValues[label.Name][label.Value] = true
		}
	}
	labelValueCounts := make([]map[string]interface{}, 0, len(labelValues))
	for name, values := range labelValues {
		labelValueCounts = append(labelValueCounts, map[string]interface{}{"name": name, "value": len(values)})
	}
	sort.Slice(labelValueCounts, func(i, j int) bool {
		return getString(labelValueCounts[i]["name"]) < getString(labelValueCounts[j]["name"])
	})
	metricCounts := make([]map[string]interface{}, 0, len(byMetric))
	for name, count := range byMetric {
		metricCounts = append(metricCounts, map[string]interface{}{"name": name, "value": count})
	}
	sort.Slice(metricCounts, func(i, j int) bool {
		return coerceInt(metricCounts[i]["value"], 0) > coerceInt(metricCounts[j]["value"], 0)
	})
	totalSamples := 0
	if s.db != nil {
		totalSamples = s.db.TotalSamples()
	}
	s.jsonOK(w, map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"headStats": map[string]interface{}{
				"numSeries":     len(series),
				"numLabelPairs": len(labelValueCounts),
				"chunkCount":    totalSamples,
				"minTime":       0,
				"maxTime":       time.Now().UnixMilli(),
			},
			"seriesCountByMetricName":    metricCounts,
			"labelValueCountByLabelName": labelValueCounts,
		},
	})
}

func (s *Server) handleAlertmanagers(w http.ResponseWriter, r *http.Request) {
	s.jsonOK(w, map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"activeAlertmanagers":  []interface{}{},
			"droppedAlertmanagers": []interface{}{},
		},
	})
}

func (s *Server) handleQueryExemplars(w http.ResponseWriter, r *http.Request) {
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": []interface{}{}})
}

func formatRangeResults(results []promql.Result) []interface{} {
	type rangeSeries struct {
		metric map[string]string
		values []interface{}
	}
	groups := map[string]*rangeSeries{}
	for _, result := range results {
		if result.Type == promql.ValueScalar {
			key := "__scalar__"
			if groups[key] == nil {
				groups[key] = &rangeSeries{metric: map[string]string{}}
			}
			groups[key].values = append(groups[key].values, []interface{}{float64(time.Now().Unix()), strconv.FormatFloat(result.Scalar, 'f', -1, 64)})
			continue
		}
		for _, sample := range result.Vector {
			metric := make(map[string]string)
			for _, label := range sample.Labels {
				metric[label.Name] = label.Value
			}
			key := labelMapKey(metric)
			if groups[key] == nil {
				groups[key] = &rangeSeries{metric: metric}
			}
			groups[key].values = append(groups[key].values, []interface{}{float64(sample.Point.Timestamp) / 1000, strconv.FormatFloat(sample.Point.Value, 'f', -1, 64)})
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]interface{}, 0, len(keys))
	for _, key := range keys {
		group := groups[key]
		out = append(out, map[string]interface{}{"metric": group.metric, "values": group.values})
	}
	return out
}

func (s *Server) prometheusRuleGroups(tenantID int64) []map[string]interface{} {
	var rules []map[string]interface{}
	appendRule := func(name, expr, duration, severity, notifyURL string, enabled bool) {
		state := "inactive"
		if enabled {
			state = "unknown"
		}
		rules = append(rules, map[string]interface{}{
			"name":           name,
			"query":          expr,
			"duration":       durationSeconds(duration),
			"labels":         map[string]string{"severity": severity},
			"annotations":    map[string]string{"notify_url": notifyURL},
			"alerts":         []interface{}{},
			"health":         "ok",
			"type":           "alerting",
			"state":          state,
			"lastEvaluation": time.Now().Format(time.RFC3339Nano),
			"evaluationTime": 0,
		})
	}
	if s.store != nil {
		if stored, err := s.store.ListAlertRules(tenantID); err == nil {
			for _, rule := range stored {
				appendRule(rule.Name, rule.Expr, rule.Duration, rule.Severity, rule.NotifyURL, rule.Enabled)
			}
		}
	}
	if s.config != nil {
		for _, rule := range s.config.Alert.Rules {
			appendRule(rule.Name, rule.Expr, rule.Duration, rule.Severity, rule.NotifyURL, true)
		}
	}
	if len(rules) == 0 {
		return []map[string]interface{}{}
	}
	return []map[string]interface{}{{
		"name":           "sentinel233",
		"file":           "sentinel233",
		"interval":       15,
		"lastEvaluation": time.Now().Format(time.RFC3339Nano),
		"evaluationTime": 0,
		"rules":          rules,
	}}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func prometheusHealth(healthy bool) string {
	if healthy {
		return "up"
	}
	return "down"
}

func intFromParam(value string, fallback int) int {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return parsed
}

func parsePrometheusTime(value string, fallback time.Time) (time.Time, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		return fallback, nil
	}
	if parsed, err := strconv.ParseFloat(text, 64); err == nil {
		if parsed > 1e11 {
			return time.UnixMilli(int64(parsed)), nil
		}
		seconds := int64(parsed)
		nanos := int64((parsed - float64(seconds)) * 1e9)
		return time.Unix(seconds, nanos), nil
	}
	if ts, err := time.Parse(time.RFC3339Nano, text); err == nil {
		return ts, nil
	}
	return time.Time{}, fmt.Errorf("invalid timestamp %q", value)
}

func parsePrometheusDuration(value string, fallback time.Duration) (time.Duration, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		return fallback, nil
	}
	if parsed, err := strconv.ParseFloat(text, 64); err == nil {
		return time.Duration(parsed * float64(time.Second)), nil
	}
	return parseCompatDuration(text)
}

func durationSeconds(value string) float64 {
	d, err := parseCompatDuration(value)
	if err != nil {
		return 0
	}
	return d.Seconds()
}

func parseCompatDuration(value string) (time.Duration, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		return 0, nil
	}
	if d, err := time.ParseDuration(text); err == nil {
		return d, nil
	}
	var total time.Duration
	for i := 0; i < len(text); {
		start := i
		for i < len(text) && ((text[i] >= '0' && text[i] <= '9') || text[i] == '.') {
			i++
		}
		if start == i {
			return 0, fmt.Errorf("invalid duration %q", value)
		}
		num, err := strconv.ParseFloat(text[start:i], 64)
		if err != nil {
			return 0, err
		}
		unitStart := i
		for i < len(text) && (text[i] < '0' || text[i] > '9') && text[i] != '.' {
			i++
		}
		switch text[unitStart:i] {
		case "ms":
			total += time.Duration(num * float64(time.Millisecond))
		case "s":
			total += time.Duration(num * float64(time.Second))
		case "m":
			total += time.Duration(num * float64(time.Minute))
		case "h":
			total += time.Duration(num * float64(time.Hour))
		case "d":
			total += time.Duration(num * float64(24*time.Hour))
		case "w":
			total += time.Duration(num * float64(7*24*time.Hour))
		case "y":
			total += time.Duration(num * float64(365*24*time.Hour))
		default:
			return 0, fmt.Errorf("unknown duration unit in %q", value)
		}
	}
	return total, nil
}

func inferMetricType(name string, labels tsdb.Labels) string {
	if value := labels.Get("metric_type"); value != "" {
		return value
	}
	switch {
	case strings.HasSuffix(name, "_total"), strings.HasSuffix(name, "_count"), strings.HasSuffix(name, "_sum"):
		return "counter"
	case strings.HasSuffix(name, "_bucket"):
		return "histogram"
	default:
		return "gauge"
	}
}

func inferMetricUnit(name string, labels tsdb.Labels) string {
	if value := labels.Get("unit"); value != "" {
		return value
	}
	switch {
	case strings.HasSuffix(name, "_bytes"):
		return "bytes"
	case strings.HasSuffix(name, "_seconds"):
		return "seconds"
	default:
		return ""
	}
}

func seriesMatchesSelector(labels tsdb.Labels, selector string) bool {
	metricName, matchers := parseSeriesSelector(selector)
	if metricName != "" && labels.Get("__name__") != metricName {
		return false
	}
	for _, matcher := range matchers {
		actual := labels.Get(matcher.Name)
		switch matcher.Op {
		case "=":
			if actual != matcher.Value {
				return false
			}
		case "!=":
			if actual == matcher.Value {
				return false
			}
		case "=~":
			if !regexMatches(matcher.Value, actual) {
				return false
			}
		case "!~":
			if regexMatches(matcher.Value, actual) {
				return false
			}
		}
	}
	return true
}

type compatLabelMatcher struct {
	Name  string
	Op    string
	Value string
}

func parseSeriesSelector(selector string) (string, []compatLabelMatcher) {
	text := strings.TrimSpace(selector)
	if text == "" {
		return "", nil
	}
	open := strings.IndexByte(text, '{')
	if open < 0 {
		return strings.TrimSpace(text), nil
	}
	metric := strings.TrimSpace(text[:open])
	close := strings.LastIndexByte(text, '}')
	if close < open {
		return metric, nil
	}
	return metric, parseCompatLabelMatchers(text[open+1 : close])
}

func parseCompatLabelMatchers(text string) []compatLabelMatcher {
	var result []compatLabelMatcher
	for _, part := range splitCompatCSV(text) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		for _, op := range []string{"!~", "=~", "!=", "="} {
			if idx := strings.Index(part, op); idx > 0 {
				result = append(result, compatLabelMatcher{
					Name:  strings.TrimSpace(part[:idx]),
					Op:    op,
					Value: unquoteCompat(strings.TrimSpace(part[idx+len(op):])),
				})
				break
			}
		}
	}
	return result
}

func splitCompatCSV(text string) []string {
	var parts []string
	var current strings.Builder
	quote := byte(0)
	escaped := false
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if escaped {
			current.WriteByte(ch)
			escaped = false
			continue
		}
		if ch == '\\' {
			current.WriteByte(ch)
			escaped = true
			continue
		}
		if quote != 0 {
			current.WriteByte(ch)
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '"' || ch == '\'' || ch == '`' {
			quote = ch
			current.WriteByte(ch)
			continue
		}
		if ch == ',' {
			parts = append(parts, current.String())
			current.Reset()
			continue
		}
		current.WriteByte(ch)
	}
	parts = append(parts, current.String())
	return parts
}

func unquoteCompat(value string) string {
	if len(value) >= 2 {
		if unquoted, err := strconv.Unquote(value); err == nil {
			return unquoted
		}
		return strings.Trim(value, `"'`)
	}
	return value
}

func regexMatches(pattern, value string) bool {
	matched, err := regexp.MatchString(pattern, value)
	return err == nil && matched
}

func labelMapKey(labels map[string]string) string {
	parts := make([]string, 0, len(labels))
	for name, value := range labels {
		parts = append(parts, name+"="+value)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}
