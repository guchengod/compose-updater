package cronexpr

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Schedule struct {
	location    *time.Location
	minutes     valueSet
	hours       valueSet
	days        valueSet
	months      valueSet
	weekdays    valueSet
	dayWildcard bool
	dowWildcard bool
	spec        string
}

type valueSet map[int]struct{}

type fieldDef struct {
	name    string
	min     int
	max     int
	aliases map[string]int
	normal  func(int) int
}

var monthAliases = map[string]int{
	"JAN": 1, "FEB": 2, "MAR": 3, "APR": 4, "MAY": 5, "JUN": 6,
	"JUL": 7, "AUG": 8, "SEP": 9, "OCT": 10, "NOV": 11, "DEC": 12,
}

var weekdayAliases = map[string]int{
	"SUN": 0, "MON": 1, "TUE": 2, "WED": 3, "THU": 4, "FRI": 5, "SAT": 6,
}

func Parse(spec string, location *time.Location) (*Schedule, error) {
	if location == nil {
		location = time.Local
	}
	spec = strings.TrimSpace(spec)
	spec = expandMacro(spec)
	parts := strings.Fields(spec)
	if len(parts) != 5 {
		return nil, fmt.Errorf("Cron 表达式必须包含 5 个字段，当前为 %d: %q", len(parts), spec)
	}

	defs := []fieldDef{
		{name: "minute", min: 0, max: 59},
		{name: "hour", min: 0, max: 23},
		{name: "day-of-month", min: 1, max: 31},
		{name: "month", min: 1, max: 12, aliases: monthAliases},
		{name: "day-of-week", min: 0, max: 7, aliases: weekdayAliases, normal: func(v int) int {
			if v == 7 {
				return 0
			}
			return v
		}},
	}

	sets := make([]valueSet, 5)
	for i := range parts {
		set, err := parseField(parts[i], defs[i])
		if err != nil {
			return nil, err
		}
		sets[i] = set
	}

	return &Schedule{
		location:    location,
		minutes:     sets[0],
		hours:       sets[1],
		days:        sets[2],
		months:      sets[3],
		weekdays:    sets[4],
		dayWildcard: parts[2] == "*",
		dowWildcard: parts[4] == "*",
		spec:        spec,
	}, nil
}

func expandMacro(spec string) string {
	switch strings.ToLower(strings.TrimSpace(spec)) {
	case "@hourly":
		return "0 * * * *"
	case "@daily", "@midnight":
		return "0 0 * * *"
	case "@weekly":
		return "0 0 * * 0"
	case "@monthly":
		return "0 0 1 * *"
	case "@yearly", "@annually":
		return "0 0 1 1 *"
	default:
		return spec
	}
}

func parseField(expr string, def fieldDef) (valueSet, error) {
	result := make(valueSet)
	for _, item := range strings.Split(strings.ToUpper(expr), ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			return nil, fmt.Errorf("Cron 字段 %s 包含空项", def.name)
		}
		base, stepText, hasStep := strings.Cut(item, "/")
		step := 1
		if hasStep {
			if strings.Contains(stepText, "/") || stepText == "" {
				return nil, fmt.Errorf("Cron 字段 %s 的步长语法无效: %q", def.name, item)
			}
			v, err := strconv.Atoi(stepText)
			if err != nil || v <= 0 {
				return nil, fmt.Errorf("Cron 字段 %s 的步长必须是正整数: %q", def.name, item)
			}
			step = v
		}

		start, end := def.min, def.max
		switch {
		case base == "*":
		case strings.Contains(base, "-"):
			left, right, ok := strings.Cut(base, "-")
			if !ok || strings.Contains(right, "-") {
				return nil, fmt.Errorf("Cron 字段 %s 的范围无效: %q", def.name, item)
			}
			var err error
			start, err = parseValue(left, def)
			if err != nil {
				return nil, err
			}
			end, err = parseValue(right, def)
			if err != nil {
				return nil, err
			}
			if start > end {
				return nil, fmt.Errorf("Cron 字段 %s 暂不支持跨界范围: %q", def.name, item)
			}
		default:
			v, err := parseValue(base, def)
			if err != nil {
				return nil, err
			}
			start = v
			if hasStep {
				end = def.max
			} else {
				end = v
			}
		}

		for v := start; v <= end; v += step {
			if def.normal != nil {
				v = def.normal(v)
			}
			result[v] = struct{}{}
			if def.normal != nil && v == 0 && end == 7 {
				break
			}
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("Cron 字段 %s 没有有效值", def.name)
	}
	return result, nil
}

func parseValue(text string, def fieldDef) (int, error) {
	text = strings.TrimSpace(strings.ToUpper(text))
	if v, ok := def.aliases[text]; ok {
		return v, nil
	}
	v, err := strconv.Atoi(text)
	if err != nil || v < def.min || v > def.max {
		return 0, fmt.Errorf("Cron 字段 %s 的值 %q 超出范围 %d-%d", def.name, text, def.min, def.max)
	}
	return v, nil
}

func (s *Schedule) Matches(t time.Time) bool {
	t = t.In(s.location)
	if !has(s.minutes, t.Minute()) || !has(s.hours, t.Hour()) || !has(s.months, int(t.Month())) {
		return false
	}
	dayMatch := has(s.days, t.Day())
	dowMatch := has(s.weekdays, int(t.Weekday()))
	switch {
	case s.dayWildcard && s.dowWildcard:
		return true
	case s.dayWildcard:
		return dowMatch
	case s.dowWildcard:
		return dayMatch
	default:
		return dayMatch || dowMatch
	}
}

func (s *Schedule) Next(after time.Time) (time.Time, error) {
	candidate := after.In(s.location).Truncate(time.Minute).Add(time.Minute)
	limit := candidate.AddDate(6, 0, 0)
	for !candidate.After(limit) {
		if s.Matches(candidate) {
			return candidate, nil
		}
		candidate = candidate.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("未来 6 年内找不到匹配 Cron %q 的时间", s.spec)
}

func has(set valueSet, value int) bool {
	_, ok := set[value]
	return ok
}
