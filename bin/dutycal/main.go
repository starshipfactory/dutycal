package main

import (
	"database/cassandra"
	"flag"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	"ancient-solutions.com/ancientauth"
	"github.com/golang/protobuf/proto"
	"github.com/starshipfactory/dutycal"
)

func main() {
	var auth *ancientauth.Authenticator
	var viewTemplates *template.Template
	var viewhandler *dutycal.ViewCalHandler
	var vieweventhandler *dutycal.ViewEventHandler
	var neweventhandler *dutycal.NewEventHandler
	var db *cassandra.RetryCassandraClient
	var loc *time.Location
	var config dutycal.DutyCalConfig
	var configPath, listenAddr string
	var configData []byte
	var err error

	flag.StringVar(&configPath, "config", "",
		"Path to the configuration file")
	flag.StringVar(&listenAddr, "listen", ":8080",
		"host:port pair the server should listen on")
	flag.Parse()

	if len(configPath) == 0 {
		flag.Usage()
		log.Fatal("No config file has been specified")
	}

	configData, err = ioutil.ReadFile(configPath)
	if err != nil {
		log.Fatal("Error reading config file ", configPath, ": ", err)
	}

	err = proto.UnmarshalText(string(configData), &config)
	if err != nil {
		log.Fatal("Error reading config file: ", err)
	}

	viewTemplates, err = template.ParseGlob(
		config.GetTemplatePath() + "/*")
	if err != nil {
		log.Fatal("Error reading HTML templates: ", err)
	}

	auth, err = ancientauth.NewAuthenticator(
		config.GetAuth().GetAppName(), config.GetAuth().GetCert(),
		config.GetAuth().GetKey(), config.GetAuth().GetCaCertificate(),
		config.GetAuth().GetAuthenticationServer(),
		config.GetAuth().GetX509Keyserver(),
		int(config.GetAuth().GetX509CacheSize()))
	if err != nil {
		log.Fatal("Error creating AncientAuth client: ", err)
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
		log.Fatal("Unable to load timezone ", config.GetDefaultTimeZone(),
			": ", err)
	}

	viewhandler = dutycal.NewViewCalHandler(
		db, auth, loc, viewTemplates, &config)
	vieweventhandler = dutycal.NewViewEventHandler(
		db, auth, loc, viewTemplates, &config)
	neweventhandler = dutycal.NewNewEventHandler(
		db, auth, loc, viewTemplates, &config)

	http.Handle("/", viewhandler)
	http.Handle("/favicon.ico", http.NotFoundHandler())
	http.Handle("/event/", vieweventhandler)
	http.Handle("/newevent", neweventhandler)
	http.Handle("/bootstrap/",
		http.StripPrefix("/bootstrap/",
			http.FileServer(http.Dir(config.GetBootstrapPath()))))
	http.Handle("/moment/",
		http.StripPrefix("/moment/",
			http.FileServer(http.Dir(config.GetMomentPath()))))
	http.Handle("/fontawesome/",
		http.StripPrefix("/fontawesome/",
			http.FileServer(http.Dir(config.GetFontawesomePath()))))

	err = http.ListenAndServeTLS(listenAddr, config.GetTlsCertFile(),
		config.GetTlsKeyFile(), nil)
	if err != nil {
		log.Fatal("Error listening on ", listenAddr, ": ", err)
	}
}
