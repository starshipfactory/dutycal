package dutycal

import (
	"database/cassandra"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"ancient-solutions.com/ancientauth"
)

type NewEventHandler struct {
	auth      *ancientauth.Authenticator
	am        *authManager
	db        *cassandra.RetryCassandraClient
	templates *template.Template
	config    *DutyCalConfig
	location  *time.Location
}

type NewEventHandlerData struct {
	Auth        AuthDetails
	Ev          *Event
	StartHour   int
	StartMinute int
	EndHour     int
	EndMinute   int

	DateFormatted string
	Error         string

	Data     url.Values
	PostData url.Values
}

func NewNewEventHandler(
	db *cassandra.RetryCassandraClient,
	auth *ancientauth.Authenticator,
	loc *time.Location,
	tmpl *template.Template,
	conf *DutyCalConfig) *NewEventHandler {
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
	return &NewEventHandler{
		auth:      auth,
		am:        NewAuthManager(auth),
		db:        db,
		templates: tmpl,
		config:    conf,
		location:  loc,
	}
}

func (h *NewEventHandler) ServeHTTP(
	rw http.ResponseWriter, req *http.Request) {
	var user string
	var ed NewEventHandlerData
	var on_date time.Time
	var start, end time.Time
	var title, description string
	var offset_hour, offset_minute int
	var reference *url.URL
	var err error

	user = h.auth.GetAuthenticatedUser(req)
	if len(user) == 0 {
		h.auth.RequestAuthorization(rw, req)
		return
	}

	if !h.auth.IsAuthenticatedScope(req, h.config.GetEditScope()) {
		rw.WriteHeader(http.StatusForbidden)
		io.WriteString(rw, "No permission to create events: "+
			err.Error()+"\r\n")
	}

	err = req.ParseForm()
	if err != nil {
		rw.WriteHeader(http.StatusInternalServerError)
		io.WriteString(rw, "Error parsing newevent form: "+
			err.Error()+"\r\n")
		log.Print("Error parsing newevent form: ", err)
	}

	title = req.PostFormValue("title")
	description = req.PostFormValue("description")

	if len(req.PostFormValue("date")) == 0 {
		on_date = time.Now().Truncate(24 * time.Hour)
	} else {
		on_date, err = time.ParseInLocation(
			"02.01.2006", req.PostFormValue("date"),
			h.location)
		if err != nil {
			ed.Error = err.Error()
		}
	}
	ed.DateFormatted = on_date.Format("02.01.2006")

	if len(req.PostFormValue("start-hour")) > 0 {
		offset_hour, err = strconv.Atoi(req.PostFormValue("start-hour"))
		if err != nil {
			ed.Error += " " + err.Error()
		}
	}
	if len(req.PostFormValue("start-hour")) > 0 {
		offset_minute, err = strconv.Atoi(req.PostFormValue("start-minute"))
		if err != nil {
			ed.Error += " " + err.Error()
		}
	}

	start = on_date.Add(
		time.Duration(offset_hour) * time.Hour).Add(
		time.Duration(offset_minute) * time.Minute)

	if len(req.PostFormValue("end-hour")) > 0 {
		offset_hour, err = strconv.Atoi(req.PostFormValue("end-hour"))
		if err != nil {
			ed.Error += " " + err.Error()
		}
	}
	if len(req.PostFormValue("end-minute")) > 0 {
		offset_minute, err = strconv.Atoi(req.PostFormValue("end-minute"))
		if err != nil {
			ed.Error += " " + err.Error()
		}
	}

	if len(req.PostFormValue("reference")) > 0 {
		reference, err = url.Parse(req.PostFormValue("reference"))
		if err == nil {
			if !reference.IsAbs() {
				ed.Error += " URL reference is not absolute."
			}
		} else {
			ed.Error += " " + err.Error()
		}
	}

	end = on_date.Add(
		time.Duration(offset_hour) * time.Hour).Add(
		time.Duration(offset_minute) * time.Minute)

	ed.StartHour = start.Hour()
	ed.StartMinute = start.Minute()
	ed.EndHour = end.Hour()
	ed.EndMinute = end.Minute()

	if ed.StartHour > ed.EndHour || (ed.StartHour == ed.EndHour &&
		ed.StartMinute > ed.EndMinute) {
		ed.Error += " Event starts after it ends."
	}

	ed.Ev = CreateEvent(h.db, h.config, title, description, user, start,
		end.Sub(start), h.location, reference, false)

	if len(ed.Error) == 0 && ed.StartHour >= 0 && ed.StartHour < 24 &&
		ed.EndHour >= 0 && ed.EndHour < 24 && ed.StartMinute >= 0 &&
		ed.StartMinute < 60 && ed.EndHour >= 0 && ed.EndHour < 24 &&
		ed.EndMinute >= 0 && ed.EndMinute < 60 && ed.Ev.Duration > 0 &&
		len(title) > 0 && len(description) > 0 {
		err = ed.Ev.Sync()

		if err == nil {
			rw.Header().Set("Location",
				"/?week="+strconv.FormatInt(
					getWeekFromTimestamp(ed.Ev.Start), 10))
			rw.WriteHeader(http.StatusTemporaryRedirect)
			return
		}

		ed.Error = err.Error()
		log.Print("Error writing out new event: ", err)
	}

	err = h.am.GenAuthDetails(req, &ed.Auth)
	if err != nil {
		rw.WriteHeader(http.StatusInternalServerError)
		io.WriteString(rw, "Error generating authentication details: "+
			err.Error()+"\r\n")
		log.Print("Error generating authentication details: ", err)
	}

	err = h.templates.ExecuteTemplate(rw, "newevent.html", &ed)
	if err != nil {
		rw.WriteHeader(http.StatusInternalServerError)
		io.WriteString(rw, "Error executing new event template: "+
			err.Error()+"\r\n")
		log.Print("Error executing new event template: ", err)
	}
}
