package cronjoborgclient

import (
	"fmt"
	"strconv"
	"strings"
)

// JobSchedule is the cron-job.org schedule format.
// -1 in any list means "every" (wildcard).
type JobSchedule struct {
	Timezone string `json:"timezone"`
	Hours    []int  `json:"hours"`
	Minutes  []int  `json:"minutes"`
	Mdays    []int  `json:"mdays"`
	Months   []int  `json:"months"`
	Wdays    []int  `json:"wdays"`
}

// ParseCronExpression converts a standard 5-field cron expression
// (minute hour mday month wday) to a cron-job.org JobSchedule.
//
// Examples:
//
//	"0 * * * *"   → hours:[-1],  minutes:[0]
//	"*/5 * * * *" → hours:[-1],  minutes:[0,5,10,...,55]
//	"0 0 * * *"   → hours:[0],   minutes:[0]
//	"0 */6 * * *" → hours:[0,6,12,18], minutes:[0]
func ParseCronExpression(expr string) (JobSchedule, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return JobSchedule{}, fmt.Errorf("expected 5-field cron expression, got %d fields: %q", len(fields), expr)
	}

	minuteField, hourField, mdayField, monthField, wdayField := fields[0], fields[1], fields[2], fields[3], fields[4]

	minutes, err := parseField(minuteField, 0, 59)
	if err != nil {
		return JobSchedule{}, fmt.Errorf("parse minute field %q: %w", minuteField, err)
	}
	hours, err := parseField(hourField, 0, 23)
	if err != nil {
		return JobSchedule{}, fmt.Errorf("parse hour field %q: %w", hourField, err)
	}
	mdays, err := parseField(mdayField, 1, 31)
	if err != nil {
		return JobSchedule{}, fmt.Errorf("parse mday field %q: %w", mdayField, err)
	}
	months, err := parseField(monthField, 1, 12)
	if err != nil {
		return JobSchedule{}, fmt.Errorf("parse month field %q: %w", monthField, err)
	}
	wdays, err := parseField(wdayField, 0, 7)
	if err != nil {
		return JobSchedule{}, fmt.Errorf("parse wday field %q: %w", wdayField, err)
	}

	return JobSchedule{
		Timezone: "UTC",
		Minutes:  minutes,
		Hours:    hours,
		Mdays:    mdays,
		Months:   months,
		Wdays:    wdays,
	}, nil
}

// parseField converts a single cron field to a cron-job.org list.
//   - "*"   → [-1]  (every)
//   - "*/n" → expanded step values within [min, max]
//   - "n"   → [n]
//   - "n,m" → [n, m]
func parseField(field string, min, max int) ([]int, error) {
	if field == "*" {
		return []int{-1}, nil
	}

	// Step expression: */n
	if strings.HasPrefix(field, "*/") {
		stepStr := strings.TrimPrefix(field, "*/")
		step, err := strconv.Atoi(stepStr)
		if err != nil || step <= 0 {
			return nil, fmt.Errorf("invalid step %q", stepStr)
		}
		var vals []int
		for v := min; v <= max; v += step {
			vals = append(vals, v)
		}
		return vals, nil
	}

	// Range expression: n-m (basic support)
	if strings.Contains(field, "-") && !strings.Contains(field, ",") {
		parts := strings.SplitN(field, "-", 2)
		from, err1 := strconv.Atoi(parts[0])
		to, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("invalid range %q", field)
		}
		var vals []int
		for v := from; v <= to; v++ {
			vals = append(vals, v)
		}
		return vals, nil
	}

	// List expression: n,m,... (may also include ranges)
	parts := strings.Split(field, ",")
	var vals []int
	for _, p := range parts {
		p = strings.TrimSpace(p)
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("invalid value %q in field %q", p, field)
		}
		vals = append(vals, n)
	}
	return vals, nil
}
