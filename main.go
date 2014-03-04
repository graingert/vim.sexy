package main

import (
	"bytes"
	"code.google.com/p/gcfg"
	"code.google.com/p/go-uuid/uuid"
	"encoding/json"
	"github.com/dpapathanasiou/go-recaptcha"
	"github.com/justinas/nosurf"
	"github.com/worr/chrooter"
	"github.com/worr/secstring"
	"html/template"
	"log"
	"math/rand"
	"net/http"
	"net/mail"
	"net/smtp"
	txttemplate "text/template"
)

type Config struct {
	Mail struct {
		Email    string
		Username string
		Password string
		Hostname string
		password *secstring.SecString
	}

	Recaptcha struct {
		Private string
	}
}

type PostData struct {
	Email string
	Csrf_token string
	Recaptcha_challenge_field string
	Recaptcha_response_field string
}

var t = template.Must(template.ParseFiles("template/index.html"))
var emailTemplate = txttemplate.Must(txttemplate.New("email").Parse("Here is your exclusive Vim download link: http://www.vim.org/download.php?code={{.Code}}"))
var c = make(chan string)
var conf Config

// Default handler
func dispatch(w http.ResponseWriter, r *http.Request) {
	context := map[string]string{}

	if r.Method == "POST" {
		body := make([]byte, 256)

		if _, err := r.Body.Read(body); err != nil {
			http.Error(w, "Error reading body", http.StatusBadRequest)
			return
		}

		var data PostData
		if err := json.Unmarshal(body, &data); err != nil {
			http.Error(w, "Can't parse JSON", http.StatusBadRequest)
			return
		}

		context["email"] = data.Email
		context["token"] = data.Csrf_token
		if !recaptcha.Confirm(r.RemoteAddr, data.Recaptcha_challenge_field, data.Recaptcha_response_field) {
			http.Error(w, "Failed captcha", http.StatusBadRequest)
			return
		}

		if context["email"] == "" {
			http.Error(w, "Empty email address", http.StatusBadRequest)
			return
		}

		c <- context["email"]
	}

	if err := t.Execute(w, context); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// Handler for all bad CSRF requests
func failedCSRF(w http.ResponseWriter, r *http.Request) {
	http.Error(w, nosurf.Reason(r).Error(), http.StatusBadRequest)
}

// Pulls email off of the channel and possibly sends download codes
func email() {
	auth := smtp.PlainAuth("", conf.Mail.Username, string(conf.Mail.password.String), conf.Mail.Hostname)

	for addr := range c {
		// Exclusivity
		if r := rand.Intn(3); r != 0 {
			continue
		}

		buf := bytes.NewBuffer(make([]byte, 100))
		if err := emailTemplate.Execute(buf, struct{ Code string }{uuid.NewUUID().String()}); err != nil {
			log.Printf("Can't execute email template: %v", err)
			continue
		}

		var emailAddr *mail.Address
		var err error
		if emailAddr, err = mail.ParseAddress(addr); err != nil {
			log.Printf("Failed to send email to %v: %v", emailAddr.String(), err)
			continue
		}

		conf.Mail.password.Decrypt()
		smtp.SendMail(conf.Mail.Hostname, auth, conf.Mail.Email, []string{emailAddr.String()}, buf.Bytes())
		conf.Mail.password.Encrypt()
	}
}

func main() {
	if err := gcfg.ReadFileInto(&conf, "vim.sexy.ini"); err != nil {
		log.Fatalf("Can't read config file: %v", err)
	}

	if err := chrooter.Chroot("www", "/var/chroot/vim.sexy"); err != nil {
		log.Fatalf("Can't chroot: %v", err)
	}


	var err error
	if conf.Mail.password, err = secstring.FromString(&conf.Mail.Password); err != nil {
		log.Fatal(err)
	}

	recaptcha.Init(conf.Recaptcha.Private)

	go email()

	http.HandleFunc("/", dispatch)
	csrf := nosurf.New(http.DefaultServeMux)
	csrf.SetFailureHandler(http.HandlerFunc(failedCSRF))
	if err = http.ListenAndServe("127.0.0.1:8000", csrf); err != nil {
		log.Fatalf("Cannot listen: %v", err)
	}
}
