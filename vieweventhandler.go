package dutycal

import (
	"database/cassandra"
	"html/template"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"ancient-solutions.com/ancientauth"
)

type ViewEventHandler struct {
	auth      *ancientauth.Authenticator
	am        *authManager
	db        *cassandra.RetryCassandraClient
	templates *template.Template
	config    *DutyCalConfig
}

type ViewEventData struct {
	Auth AuthDetails

	Op   string
	Ev   *Event
	End  time.Time
	Week int64
}

func NewViewEventHandler(
	db *cassandra.RetryCassandraClient,
	auth *ancientauth.Authenticator,
	tmpl *template.Template,
	conf *DutyCalConfig) *ViewEventHandler {
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
		auth:      auth,
		am:        NewAuthManager(auth),
		db:        db,
		templates: tmpl,
		config:    conf,
	}
}

func (v *ViewEventHandler) ServeHTTP(
	rw http.ResponseWriter, req *http.Request) {
	var ed *ViewEventData
	var can_edit bool
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

	can_edit = v.auth.IsAuthenticatedScope(req, v.config.GetEditScope())

	ev, err = FetchEvent(v.db, v.config, urlparts[2], false)
	if err != nil {
		rw.WriteHeader(http.StatusInternalServerError)
		io.WriteString(rw, "Error fetching event "+urlparts[2]+": "+
			err.Error()+"\r\n")
		log.Print("Error fetching event ", urlparts[2], ": ", err)
		return
	}

	if op == "take" {
		var user string = v.auth.GetAuthenticatedUser(req)

		if len(user) == 0 {
			v.auth.RequestAuthorization(rw, req)
			return
		}

		if can_edit {
			ev.Owner = user
			err = ev.Sync()
			if err != nil {
				log.Print("Error syncing new owner ", user,
					" for event ", ev.Id, ": ", err)
				ev.Owner = err.Error()
			}
		}
	}

	// Hide personal details unless the user is authenticated to a scope
	// which can see them.
	if !can_edit && len(ev.Owner) > 0 {
		ev.Owner = "Assigned"
	}

	ed = &ViewEventData{
		Ev:   ev,
		Op:   op,
		End:  ev.Start.Add(ev.Duration),
		Week: getWeekFromTimestamp(ev.Start),
	}
	v.am.GenAuthDetails(req, &ed.Auth)
	err = v.templates.ExecuteTemplate(rw, "viewevent.html", ed)
	if err != nil {
		rw.WriteHeader(http.StatusInternalServerError)
		io.WriteString(rw, "Error executing template for "+urlparts[2]+": "+
			err.Error()+"\r\n")
		log.Print("Error executing template for ", urlparts[2], ": ", err)
	}
}
