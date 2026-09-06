package config

import "testing"

func TestMailMode(t *testing.T) {
	cases := []struct {
		name string
		mail Mail
		want string
		auto bool
	}{
		{"993 без выбора — сразу TLS", Mail{Port: 993}, MailSSL, true},
		{"143 без выбора — STARTTLS", Mail{Port: 143}, MailStartTLS, true},
		{"выбор важнее порта", Mail{Port: 993, Security: "starttls"}, MailStartTLS, false},
		{"регистр и пробелы не мешают", Mail{Port: 143, Security: " SSL "}, MailSSL, false},
		{"неизвестный порт — сразу TLS", Mail{Port: 1993}, MailSSL, true},
	}
	for _, c := range cases {
		if got := c.mail.Mode(); got != c.want {
			t.Errorf("%s: Mode() = %q, ожидалось %q", c.name, got, c.want)
		}
		if got := c.mail.AutoSecurity(); got != c.auto {
			t.Errorf("%s: AutoSecurity() = %v, ожидалось %v", c.name, got, c.auto)
		}
	}
}
