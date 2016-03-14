package dutycal

import (
	"database/cassandra"
	"html/template"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ancient-solutions.com/ancientauth"
)

// ViewEventHandler is a handler for viewing individual events from the
// calendar. Mostly used as an HTTP handler.
type ViewEventHandler struct {
	auth      *ancientauth.Authenticator
	am        *authManager
	db        *cassandra.RetryCassandraClient
	templates *template.Template
	config    *DutyCalConfig
	location  *time.Location
}

// ViewEventData holds all the data to be presented to the user from the
// template of the view event handler.
type ViewEventData struct {
	Auth AuthDetails

	Op   string
	Ev   *Event
	End  time.Time
	Week int64

	CanDisclaim bool
	CanDelete   bool
}

// NewViewEventHandler creates a new ViewEventHandler object using the specified
// database connection "db", the authentication client parameters "auth", the
// timestamp locale "loc", the HTML template "tmpl" and just in general the
// configuration protobuf "conf".
//
// This method cannot fail (except for running out of memory or something).
func NewViewEventHandler(
	db *cassandra.RetryCassandraClient,
	auth *ancientauth.Authenticator,
	loc *time.Location,
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
	if loc == nil {
		log.Panic("loc is nil")
	}
	return &ViewEventHandler{
		auth:      auth,
		am:        NewAuthManager(auth),
		db:        db,
		templates: tmpl,
		config:    conf,
		location:  loc,
	}
}

func (v *ViewEventHandler) ServeHTTP(
	rw http.ResponseWriter, req *http.Request) {
	var user string
	var ed *ViewEventData
	var canEdit bool
	var canDelete bool
	var canDisclaim bool
	var urlparts []string = strings.Split(req.URL.Path, "/")
	var op string
	var ev *Event
	var err error

	user = v.auth.GetAuthenticatedUser(req)
	if len(urlparts) < 3 {
		http.Redirect(rw, req, "/", http.StatusTemporaryRedirect)
		return
	}
	if len(urlparts) >= 4 {
		op = urlparts[3]
	} else {
		op = "view"
	}

	canEdit = v.auth.IsAuthenticatedScope(req, v.config.GetEditScope())

	ev, err = FetchEvent(v.db, v.config, urlparts[2], v.location, false)
	if err != nil {
		rw.WriteHeader(http.StatusInternalServerError)
		io.WriteString(rw, "Error fetching event "+urlparts[2]+": "+
			err.Error()+"\r\n")
		log.Print("Error fetching event ", urlparts[2], ": ", err)
		return
	}

	canDelete = !ev.Required && ev.Owner == user
	canDisclaim = ev.Owner == user

	if op == "take" {
		if len(user) == 0 {
			v.auth.RequestAuthorization(rw, req)
			return
		}

		if canEdit {
			ev.Owner = user
			err = ev.Sync()
			if err != nil {
				log.Print("Error syncing new owner ", user,
					" for event ", ev.ID, ": ", err)
				ev.Owner = err.Error()
			}
		}
	} else if op == "disclaim" {
		if len(user) == 0 {
			v.auth.RequestAuthorization(rw, req)
			return
		}

		if canDisclaim {
			ev.Owner = ""
			err = ev.Sync()
			if err == nil {
				rw.Header().Set("Location",
					"/event/"+ev.ID+"/view")
				rw.WriteHeader(http.StatusTemporaryRedirect)
				return
			}

			log.Print("Error syncing new owner ", user,
				" for event ", ev.ID, ": ", err)
			ev.Owner = err.Error()
		}
	} else if op == "delete" {
		if len(user) == 0 {
			v.auth.RequestAuthorization(rw, req)
			return
		}

		if canDelete {
			err = ev.Delete()
			if err == nil {
				rw.Header().Set("Location",
					"/?week="+strconv.FormatInt(
						getWeekFromTimestamp(ev.Start), 10))
				rw.WriteHeader(http.StatusTemporaryRedirect)
				return
			}
			log.Print("Error deleting event ", ev.ID, ": ", err)
		}
	}

	// Things may have changed above, let's recompute.
	canDelete = !ev.Required && ev.Owner == user
	canDisclaim = ev.Owner == user

	// Hide personal details unless the user is authenticated to a scope
	// which can see them.
	if !canEdit && len(ev.Owner) > 0 {
		ev.Owner = "Assigned"
	}

	ed = &ViewEventData{
		Ev:          ev,
		Op:          op,
		End:         ev.Start.Add(ev.Duration).In(v.location),
		Week:        getWeekFromTimestamp(ev.Start),
		CanDelete:   canDelete,
		CanDisclaim: canDisclaim,
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
