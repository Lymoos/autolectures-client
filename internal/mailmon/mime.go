package mailmon

import (
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset"
	"github.com/emersion/go-message/mail"
)

type Parsed struct {
	From    string
	Subject string
	Date    time.Time
	Text    string
	HTML    string
}

type Invitation struct {
	URL   string
	When  time.Time
	Title string
}

func (i Invitation) Valid() bool { return i.URL != "" }

func ParseMessage(raw []byte) Parsed {
	var p Parsed
	mr, err := mail.CreateReader(strings.NewReader(string(raw)))
	if err != nil {
		if entity, e2 := message.Read(strings.NewReader(string(raw))); e2 == nil {
			body, _ := io.ReadAll(entity.Body)
			p.Text = string(body)
		}
		return p
	}
	p.Subject, _ = mr.Header.Subject()
	p.Date, _ = mr.Header.Date()
	if addrs, err := mr.Header.AddressList("From"); err == nil && len(addrs) > 0 {
		p.From = addrs[0].Address
	}
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}
		ih, ok := part.Header.(*mail.InlineHeader)
		if !ok {
			continue
		}
		ctype, _, _ := ih.ContentType()
		body, _ := io.ReadAll(part.Body)
		switch strings.ToLower(ctype) {
		case "text/plain":
			p.Text += string(body) + "\n"
		case "text/html":
			p.HTML += string(body) + "\n"
		}
	}
	return p
}

var (
	styleRe   = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	scriptRe  = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	breakRe   = regexp.MustCompile(`(?i)<br\s*/?>|</p>|</div>|</tr>|</li>|</h[1-6]>`)
	tagRe     = regexp.MustCompile(`<[^>]+>`)
	spacesRe  = regexp.MustCompile(`[ \t]+`)
	hrefRe    = regexp.MustCompile(`(?i)href\s*=\s*["']?(https?://[^\s"'<>]*mts-link\.ru/[^\s"'<>]+)`)
	urlRe     = regexp.MustCompile(`(?i)(https?://[\w.-]*mts-link\.ru/[^\s"'<>)\]]+)`)
	ruDateRe  = regexp.MustCompile(`(\d{1,2})\s+([А-Яа-яЁё]+)(?:\s+(\d{4}))?[^\d\n]{0,25}?(\d{1,2}):(\d{2})`)
	dotDateRe = regexp.MustCompile(`(\d{1,2})\.(\d{2})\.(\d{4})[^\d\n]{0,15}?(\d{1,2}):(\d{2})`)
	isoDateRe = regexp.MustCompile(`(\d{4})-(\d{2})-(\d{2})[T\s](\d{1,2}):(\d{2})`)
	titleRe   = regexp.MustCompile(`[:«"]\s*(.+?)\s*[»"]?\s*$`)
)

var months = []string{"январ", "феврал", "март", "апрел", "ма", "июн", "июл", "август", "сентябр", "октябр", "ноябр", "декабр"}

func monthFromRussian(word string) int {
	w := strings.ToLower(word)
	for i, m := range months {
		if strings.HasPrefix(w, m) {
			return i + 1
		}
	}
	return 0
}

func HTMLToText(html string) string {
	t := styleRe.ReplaceAllString(html, "")
	t = scriptRe.ReplaceAllString(t, "")
	t = breakRe.ReplaceAllString(t, "\n")
	t = tagRe.ReplaceAllString(t, " ")
	r := strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&quot;", `"`, "&#39;", "'", "&lt;", "<", "&gt;", ">")
	t = r.Replace(t)
	return spacesRe.ReplaceAllString(t, " ")
}

func ExtractInvitation(m Parsed) Invitation {
	inv := Invitation{Title: m.Subject}

	pick := func(re *regexp.Regexp, src string) string {
		fallback := ""
		for _, sub := range re.FindAllStringSubmatch(src, -1) {
			u := strings.ReplaceAll(sub[1], "&amp;", "&")
			lower := strings.ToLower(u)
			if strings.Contains(lower, "unsubscribe") || strings.Contains(lower, "otpis") {
				continue
			}
			for _, marker := range []string{"/j/", "/webinar/", "/meeting", "/event/", "/room/", "/stream/"} {
				if strings.Contains(lower, marker) {
					return u
				}
			}
			if fallback == "" {
				fallback = u
			}
		}
		return fallback
	}
	inv.URL = pick(hrefRe, m.HTML)
	if inv.URL == "" {
		inv.URL = pick(urlRe, m.Text+"\n"+HTMLToText(m.HTML))
	}

	text := m.Text + "\n" + HTMLToText(m.HTML)
	nowYear := time.Now().Year()
	for _, sub := range ruDateRe.FindAllStringSubmatch(text, -1) {
		month := monthFromRussian(sub[2])
		if month == 0 {
			continue
		}
		year := nowYear
		if sub[3] != "" {
			year, _ = strconv.Atoi(sub[3])
		}
		day, _ := strconv.Atoi(sub[1])
		hour, _ := strconv.Atoi(sub[4])
		minute, _ := strconv.Atoi(sub[5])
		if t := time.Date(year, time.Month(month), day, hour, minute, 0, 0, time.Local); t.Day() == day {
			if sub[3] == "" && t.Before(time.Now().AddDate(0, 0, -30)) {
				t = t.AddDate(1, 0, 0)
			}
			inv.When = t
			break
		}
	}
	if inv.When.IsZero() {
		if sub := dotDateRe.FindStringSubmatch(text); sub != nil {
			d, _ := strconv.Atoi(sub[1])
			mo, _ := strconv.Atoi(sub[2])
			y, _ := strconv.Atoi(sub[3])
			h, _ := strconv.Atoi(sub[4])
			mi, _ := strconv.Atoi(sub[5])
			inv.When = time.Date(y, time.Month(mo), d, h, mi, 0, 0, time.Local)
		}
	}
	if inv.When.IsZero() {
		if sub := isoDateRe.FindStringSubmatch(text); sub != nil {
			y, _ := strconv.Atoi(sub[1])
			mo, _ := strconv.Atoi(sub[2])
			d, _ := strconv.Atoi(sub[3])
			h, _ := strconv.Atoi(sub[4])
			mi, _ := strconv.Atoi(sub[5])
			inv.When = time.Date(y, time.Month(mo), d, h, mi, 0, 0, time.Local)
		}
	}
	if inv.When.IsZero() && !m.Date.IsZero() {
		inv.When = m.Date.Local()
	}
	if sub := titleRe.FindStringSubmatch(m.Subject); sub != nil && len(sub[1]) > 3 {
		inv.Title = sub[1]
	}
	return inv
}
