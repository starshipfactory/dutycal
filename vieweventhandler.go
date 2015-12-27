package dutycal

import (
	"database/cassandra"
	"html/template"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

type ViewEventHandler struct {
	db        *cassandra.RetryCassandraClient
	templates *template.Template
	config    *DutyCalConfig
}

type ViewEventData struct {
	Op  string
	Ev  *Event
	End time.Time
}

func NewViewEventHandler(db *cassandra.RetryCassandraClient, conf *DutyCalConfig, tmpl *template.Template) *ViewEventHandler {
	if db == nil {
		log.Panic("db is nil")
	}
	if conf == nil {
		log.Panic("conf is nil")
	}
	if tmpl == nil {
		log.Panic("tmpl is nil")
	}
	return &ViewEventHandler{
		db:        db,
		templates: tmpl,
		config:    conf,
	}
}

func (v *ViewEventHandler) ServeHTTP(
	rw http.ResponseWriter, req *http.Request) {
	var urlparts []string = strings.Split(req.URL.Path, "/")
	var op string
	var ev *Event
	var err error

	if len(urlparts) < 3 {
		http.Redirect(rw, req, "/", http.StatusTemporaryRedirect)
		return
	}
	if len(urlparts) >= 4 {
		op = urlparts[3]
	} else {
		op = "view"
	}

	ev, err = FetchEvent(v.db, v.config, urlparts[2], false)
	if err != nil {
		rw.WriteHeader(http.StatusInternalServerError)
		io.WriteString(rw, "Error fetching event "+urlparts[2]+": "+
			err.Error()+"\r\n")
		log.Print("Error fetching event ", urlparts[2], ": ", err)
		return
	}

	err = v.templates.ExecuteTemplate(rw, "viewevent.html", &ViewEventData{
		Ev:  ev,
		Op:  op,
		End: ev.Start.Add(ev.Duration),
	})
	if err != nil {
		rw.WriteHeader(http.StatusInternalServerError)
		io.WriteString(rw, "Error executing template for "+urlparts[2]+": "+
			err.Error()+"\r\n")
		log.Print("Error executing template for ", urlparts[2], ": ", err)
	}
}
