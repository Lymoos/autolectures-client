package mailmon

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"
)

const reminderHTML = `<html><body>
<h1>Напоминаем, что вы приглашены на вебинар</h1>
<img src="https:
<h2>Лекция №1</h2>
<p><span style="color:#8e44ad">2 сентября</span> 16:10 (UTC +03)</p>
<table><tr><td align="center">
  <a href="https:
     style="background:#7f00ff;color:#fff">ПЕРЕЙТИ К ВЕБИНАРУ</a>
</td></tr></table>
<p>Максим Игоревич Шестаков,<br>напоминаем, что Вы зарегистрированы на вебинар.</p>
<p><a href="https:
</body></html>`

func buildMessage(from, subject, html string) []byte {
	enc := base64.StdEncoding.EncodeToString([]byte(html))
	var b strings.Builder
	fmt.Fprintf(&b, "From: MTS Link <%s>\r\n", from)
	fmt.Fprintf(&b, "To: shestakov@edu.mirea.ru\r\n")
	fmt.Fprintf(&b, "Subject: =?UTF-8?B?%s?=\r\n", base64.StdEncoding.EncodeToString([]byte(subject)))
	b.WriteString("Date: Mon, 01 Sep 2025 09:00:00 +0300\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	for i := 0; i < len(enc); i += 76 {
		end := i + 76
		if end > len(enc) {
			end = len(enc)
		}
		b.WriteString(enc[i:end] + "\r\n")
	}
	return []byte(b.String())
}


func TestReminderFromEduMirea(t *testing.T) {
	raw := buildMessage("noreply@mts-link.ru", "Напоминаем о вебинаре: Лекция №1", reminderHTML)
	parsed := ParseMessage(raw)
	if parsed.HTML == "" {
		t.Fatalf("HTML-часть письма не разобрана")
	}
	inv := ExtractInvitation(parsed)
	if !inv.Valid() {
		t.Fatalf("ссылка на вебинар не найдена")
	}
	const want = "https:
	if inv.URL != want {
		t.Errorf("ссылка: получено %q, ожидалось %q", inv.URL, want)
	}
	if inv.When.Day() != 2 || inv.When.Month() != time.September || inv.When.Hour() != 16 || inv.When.Minute() != 10 {
		t.Errorf("дата: получено %s, ожидалось 2 сентября 16:10", inv.When.Format("02.01.2006 15:04"))
	}
	if inv.Title == "" {
		t.Errorf("название лекции пустое")
	}
	t.Logf("ссылка=%s время=%s название=%q", inv.URL, inv.When.Format("02.01.2006 15:04"), inv.Title)
}


func TestClassicInvitation(t *testing.T) {
	html := `<html><body><p>Вы приглашены на мероприятие «Лекция по физике»</p>
	<p>Дата: 15.09.2025 в 10:40 (UTC +03)</p>
	<a href="https:
	parsed := ParseMessage(buildMessage("invitation@mts-link.ru", "Приглашение на мероприятие: Лекция по физике", html))
	inv := ExtractInvitation(parsed)
	if !inv.Valid() {
		t.Fatalf("ссылка не найдена")
	}
	if inv.When.Day() != 15 || inv.When.Month() != time.September || inv.When.Hour() != 10 || inv.When.Minute() != 40 {
		t.Errorf("дата: получено %s, ожидалось 15.09 10:40", inv.When.Format("02.01.2006 15:04"))
	}
	t.Logf("ссылка=%s время=%s название=%q", inv.URL, inv.When.Format("02.01.2006 15:04"), inv.Title)
}


func TestUnsubscribeIgnored(t *testing.T) {
	html := `<html><body><a href="https:
	inv := ExtractInvitation(ParseMessage(buildMessage("noreply@mts-link.ru", "Рассылка", html)))
	if inv.Valid() {
		t.Errorf("принята ссылка отписки: %s", inv.URL)
	}
}

