package main

import (
	"bytes"
	"database/cassandra"
	"io"
	"log"
	"net"
	"net/smtp"
	"strconv"
	"text/template"
	"time"

	"github.com/starshipfactory/dutycal"
)

func SendNotifications(
	notification *dutycal.UpcomingEventNotificationConfig,
	db *cassandra.RetryCassandraClient,
	tmpl *template.Template,
	loc *time.Location,
	config *dutycal.DutyCalConfig) {
	var now time.Time = time.Now().In(loc).Truncate(
		24 * time.Hour)
	var end time.Time = now.AddDate(0, 0,
		int(notification.GetWarningLookahead()))
	var sb bytes.Buffer
	var events []*dutycal.Event
	var notify []*dutycal.Event
	var ev *dutycal.Event
	var weekend time.Time
	var auth smtp.Auth
	var smtp_host string
	var offset int
	var err error

	for now.Before(end) {
		weekend = now.AddDate(0, 0, 8).Truncate(7 * 24 * time.Hour)
		_, offset = weekend.Zone()
		weekend = weekend.Add(time.Duration(-offset) * time.Second)

		if end.Before(weekend) {
			weekend = end
		}

		events, err = dutycal.FetchEventRange(
			db, config, now, weekend, loc, false)
		if err != nil {
			log.Fatal("Error fetching events from ", now, " to ",
				weekend, ": ", err)
		}

		for _, ev = range events {
			if ev.Required && ev.Owner == "" {
				notify = append(notify, ev)
			}
		}

		now = weekend
	}

	if len(notify) == 0 {
		return
	}

	// Write the mail header first.
	io.WriteString(&sb, "Message-Id: notify-"+
		time.Now().In(loc).Format("2006-01-02T15-04-05")+"-"+
		strconv.FormatInt(int64(len(notify)), 10)+"msg-"+
		notification.GetSender()+"\r\n")
	io.WriteString(&sb, "From: "+notification.GetSender()+"\r\n")
	io.WriteString(&sb, "To: "+notification.GetRecipient()+"\r\n")
	io.WriteString(&sb, "Subject: "+notification.GetSubject()+"\r\n")
	io.WriteString(&sb, "Date: "+time.Now().In(loc).Format(time.RFC1123Z)+
		"\r\n\r\n")

	err = tmpl.Execute(&sb, notify)
	if err != nil {
		log.Fatal("Error executing template ",
			notification.GetTemplatePath(), ": ", err)
	}

	smtp_host, _, err = net.SplitHostPort(
		config.GetMailConfig().GetSmtpServerAddress())
	if err != nil {
		log.Fatal("Error splitting host:port in SMTP server address: ",
			err)
	}

	auth = smtp.PlainAuth(
		config.GetMailConfig().GetIdentity(),
		config.GetMailConfig().GetUsername(),
		config.GetMailConfig().GetPassword(), smtp_host)

	err = smtp.SendMail(config.GetMailConfig().GetSmtpServerAddress(),
		auth, notification.GetSender(),
		[]string{notification.GetRecipient()}, sb.Bytes())
	if err != nil {
		log.Fatal("Error sending mail to ", notification.GetRecipient(),
			": ", err)
	}
}
