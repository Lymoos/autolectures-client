package mailmon

import (
	"errors"
	"testing"

	"github.com/Lymoos/autolectures/client/internal/config"
)

func TestBrokenProtocol(t *testing.T) {
	broken := []error{
		errors.New(`in response-tagged: received tagged response with unknown tag "T3"`),
		errors.New("не удалось подключиться к imap.mirea.ru: tls: first record does not look like a TLS handshake"),
		errors.New("unexpected EOF"),
	}
	for _, err := range broken {
		if !brokenProtocol(err) {
			t.Errorf("ожидался протокольный сбой: %v", err)
		}
	}
	fine := []error{
		errors.New("неверный логин или пароль приложения (Authentication failed)"),
		errors.New("поиск писем завершился ошибкой: BAD syntax"),
	}
	for _, err := range fine {
		if brokenProtocol(err) {
			t.Errorf("это не протокольный сбой: %v", err)
		}
	}
}

func TestAltSettings(t *testing.T) {
	starttls := config.Mail{Host: "imap.mirea.ru", Port: 143}
	alt := altSettings(starttls)
	if alt.Port != 993 || alt.Mode() != config.MailSSL {
		t.Fatalf("143 → ожидался 993/SSL, получено %d/%s", alt.Port, alt.Mode())
	}
	if back := altSettings(alt); back.Port != 143 || back.Mode() != config.MailStartTLS {
		t.Fatalf("993 → ожидался 143/STARTTLS, получено %d/%s", back.Port, back.Mode())
	}
	if alt.Host != starttls.Host || alt.User != starttls.User {
		t.Fatal("подмена настроек не должна трогать хост и логин")
	}
}
