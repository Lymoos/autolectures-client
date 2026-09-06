package ical

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Lesson struct {
	UID         string
	Title       string
	Start, End  time.Time
	Location    string
	Description string
}

var moscow = func() *time.Location {
	if loc, err := time.LoadLocation("Europe/Moscow"); err == nil {
		return loc
	}
	return time.FixedZone("MSK", 3*3600)
}()

var tzidRe = regexp.MustCompile(`TZID=([^;:]+)`)


func parseDateTime(value, params string) (time.Time, bool) {
	v := strings.TrimSpace(value)
	loc := moscow
	if m := tzidRe.FindStringSubmatch(params); m != nil {
		if l, err := time.LoadLocation(m[1]); err == nil {
			loc = l
		}
	}
	if strings.HasSuffix(v, "Z") {
		if t, err := time.Parse("20060102T150405Z", v); err == nil {
			return t.Local(), true
		}
	}
	if len(v) >= 15 {
		if t, err := time.ParseInLocation("20060102T150405", v[:15], loc); err == nil {
			return t.Local(), true
		}
	}
	if len(v) == 8 {
		if t, err := time.ParseInLocation("20060102", v, loc); err == nil {
			return t.Local(), true
		}
	}
	return time.Time{}, false
}

func unescape(v string) string {
	r := strings.NewReplacer(`\n`, "\n", `\,`, ",", `\;`, ";", `\\`, `\`)
	return r.Replace(v)
}

type prop struct{ params, value string }

type rawEvent struct {
	props   map[string]prop
	exdates []prop
}


func Parse(ics string, from, to time.Time) []Lesson {

	var lines []string
	for _, raw := range strings.Split(strings.ReplaceAll(ics, "\r\n", "\n"), "\n") {
		if (strings.HasPrefix(raw, " ") || strings.HasPrefix(raw, "\t")) && len(lines) > 0 {
			lines[len(lines)-1] += raw[1:]
		} else {
			lines = append(lines, raw)
		}
	}


	var events []*rawEvent
	var cur *rawEvent
	for _, line := range lines {
		switch {
		case line == "BEGIN:VEVENT":
			cur = &rawEvent{props: map[string]prop{}}
			events = append(events, cur)
			continue
		case line == "END:VEVENT":
			cur = nil
			continue
		case cur == nil:
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			continue
		}
		nameParams, value := line[:colon], line[colon+1:]
		name, params := nameParams, ""
		if semi := strings.IndexByte(nameParams, ';'); semi >= 0 {
			name, params = nameParams[:semi], nameParams[semi+1:]
		}
		name = strings.ToUpper(name)
		if name == "EXDATE" {
			for _, d := range strings.Split(value, ",") {
				cur.exdates = append(cur.exdates, prop{params, strings.TrimSpace(d)})
			}
		} else {
			cur.props[name] = prop{params, value}
		}
	}


	var result []Lesson
	for _, ev := range events {
		ds := ev.props["DTSTART"]
		de := ev.props["DTEND"]
		start, ok := parseDateTime(ds.value, ds.params)
		if !ok {
			continue
		}
		end, ok := parseDateTime(de.value, de.params)
		if !ok {
			end = start.Add(90 * time.Minute)
		}
		duration := end.Sub(start)

		base := Lesson{
			UID:         ev.props["UID"].value,
			Title:       strings.TrimSpace(unescape(ev.props["SUMMARY"].value)),
			Location:    strings.TrimSpace(unescape(ev.props["LOCATION"].value)),
			Description: strings.TrimSpace(unescape(ev.props["DESCRIPTION"].value)),
		}
		excluded := map[int64]bool{}
		for _, ex := range ev.exdates {
			if t, ok := parseDateTime(ex.value, ex.params); ok {
				excluded[t.Unix()] = true
			}
		}

		rrule := ev.props["RRULE"].value
		if rrule == "" {
			if !start.After(to) && !end.Before(from) && !excluded[start.Unix()] {
				l := base
				l.Start, l.End = start, end
				result = append(result, l)
			}
			continue
		}


		intervalDays, count := 7, -1
		var until time.Time
		for _, kv := range strings.Split(rrule, ";") {
			key, val, _ := strings.Cut(kv, "=")
			switch strings.ToUpper(key) {
			case "FREQ":
				if strings.EqualFold(val, "DAILY") {
					intervalDays = 1
				}
			case "INTERVAL":
				if n, err := strconv.Atoi(val); err == nil && n > 0 {
					intervalDays *= n
				}
			case "UNTIL":
				until, _ = parseDateTime(val, "")
			case "COUNT":
				count, _ = strconv.Atoi(val)
			}
		}
		occ := start
		for produced, guard := 0, 0; guard < 1000; guard++ {
			if !until.IsZero() && occ.After(until) {
				break
			}
			if count > 0 && produced >= count {
				break
			}
			if occ.After(to) {
				break
			}
			occEnd := occ.Add(duration)
			if !occEnd.Before(from) && !excluded[occ.Unix()] {
				l := base
				l.Start, l.End = occ, occEnd
				result = append(result, l)
			}
			produced++
			occ = occ.AddDate(0, 0, intervalDays)
		}
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Start.Before(result[j].Start) })
	return result
}

