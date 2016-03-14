package main

import (
	"database/cassandra"
	"flag"
	"io/ioutil"
	"log"
	"text/template"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/starshipfactory/dutycal"
)

func main() {
	var db *cassandra.RetryCassandraClient
	var notificationSection string
	var configPath string
	var configdata []byte
	var loc *time.Location
	var config dutycal.DutyCalConfig
	var notification, n *dutycal.UpcomingEventNotificationConfig
	var tmpl *template.Template
	var err error

	flag.StringVar(&configPath, "config", "",
		"Path to the configuration file")
	flag.StringVar(&notificationSection, "section", "",
		"Name of the notification section to check against")
	flag.Parse()

	if len(configPath) == 0 {
		flag.Usage()
		log.Fatal("No config file has been specified")
	}

	configdata, err = ioutil.ReadFile(configPath)
	if err != nil {
		log.Fatal("Error reading config file ", configPath, ": ", err)
	}

	err = proto.UnmarshalText(string(configdata), &config)
	if err != nil {
		log.Fatal("Error reading config file: ", err)
	}

	db, err = cassandra.NewRetryCassandraClient(config.GetDbServer())
	if err != nil {
		log.Fatal("Error connecting to Cassandra at ",
			config.GetDbServer(), ": ", err)
	}

	err = db.SetKeyspace(config.GetKeyspace())
	if err != nil {
		log.Fatal("Error switching keyspace to ", config.GetKeyspace(),
			": ", err)
	}

	loc, err = time.LoadLocation(config.GetDefaultTimeZone())
	if err != nil {
		log.Fatal("Unable to load time zone ", config.GetDefaultTimeZone(),
			": ", err)
	}

	for _, n = range config.GetUpcomingNotifications() {
		if notificationSection == n.GetName() {
			notification = n
		}
	}

	if notification == nil {
		log.Fatal("No notification named ", notificationSection,
			" in configuration ", configPath)
	}

	tmpl, err = template.ParseFiles(notification.GetTemplatePath())
	if err != nil {
		log.Fatal("Error loading template ", notification.GetTemplatePath(),
			": ", err)
	}

	SendNotifications(notification, db, tmpl, loc, &config)
}
