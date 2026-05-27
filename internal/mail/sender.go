// Package mail отправляет письма через стандартный net/smtp с PLAIN auth.
// Настраивается через переменные окружения:
//   SMTP_HOST  — например smtp.gmail.com
//   SMTP_PORT  — например 587
//   SMTP_USER  — отправляющий email
//   SMTP_PASS  — App Password (для Gmail — 16 символов, не основной пароль)
//   SMTP_FROM  — что показать в From; если пусто — используется SMTP_USER
//
// Если SMTP_HOST не задан — Sender работает в режиме DryRun: коды печатаются
// в лог сервера. Это удобно для разработки и для демо на защите без настроенного SMTP.
package mail

import (
	"fmt"
	"log"
	"net/smtp"
	"os"
	"strings"
)

type Sender struct {
	host string
	port string
	user string
	pass string
	from string
}

// New читает env-переменные. Возвращает Sender даже если SMTP не настроен —
// в таком случае поле host будет пустым и SendCode напечатает код в лог.
func New() *Sender {
	s := &Sender{
		host: os.Getenv("SMTP_HOST"),
		port: os.Getenv("SMTP_PORT"),
		user: os.Getenv("SMTP_USER"),
		pass: os.Getenv("SMTP_PASS"),
		from: os.Getenv("SMTP_FROM"),
	}
	if s.from == "" {
		s.from = s.user
	}
	if s.port == "" {
		s.port = "587"
	}
	if s.host == "" {
		log.Println("[mail] SMTP_HOST is empty — sender in DryRun mode (codes will be logged)")
	} else {
		log.Printf("[mail] sender ready: host=%s port=%s from=%s", s.host, s.port, s.from)
	}
	return s
}

// SendCode шлёт 6-значный код восстановления на адрес to.
// Если SMTP не настроен — печатает в лог и возвращает nil (для удобства разработки).
func (s *Sender) SendCode(to, code string) error {
	if s.host == "" {
		log.Printf("[mail] DRY-RUN reset code for %s: %s", to, code)
		return nil
	}

	subject := "Код восстановления пароля — Smart Residency"
	body := fmt.Sprintf(
		"Здравствуйте!\r\n\r\n"+
			"Ваш код для восстановления пароля: %s\r\n\r\n"+
			"Код действителен 15 минут. Если вы не запрашивали восстановление, просто проигнорируйте это письмо.\r\n\r\n"+
			"— Smart Residency",
		code,
	)

	msg := buildMessage(s.from, to, subject, body)
	addr := s.host + ":" + s.port
	auth := smtp.PlainAuth("", s.user, s.pass, s.host)

	if err := smtp.SendMail(addr, auth, s.from, []string{to}, []byte(msg)); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	log.Printf("[mail] reset code sent to %s", to)
	return nil
}

func buildMessage(from, to, subject, body string) string {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	return b.String()
}
