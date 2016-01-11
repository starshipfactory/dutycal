package dutycal

import (
	"database/cassandra"
	"html/template"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"ancient-solutions.com/ancientauth"
)

type ViewCalHandler struct {
	am        *authManager
	db        *cassandra.RetryCassandraClient
	templates *template.Template
	config    *DutyCalConfig
}

type calendarViewData struct {
	Auth AuthDetails

	Weekstart     time.Time
	WeekstartText string
	WeekNumber    int64
	PreviousWeek  int64
	NextWeek      int64

	Days   []string
	Events [][]*Event
}

func NewViewCalHandler(
	db *cassandra.RetryCassandraClient, auth *ancientauth.Authenticator,
	tmpl *template.Template, conf *DutyCalConfig) *ViewCalHandler {
	if db == nil {
		log.Panic("db is nil")
	}
	if conf == nil {
		log.Panic("conf is nil")
	}
	if tmpl == nil {
		log.Panic("tmpl is nil")
	}
	return &ViewCalHandler{
		am:        NewAuthManager(auth),
		db:        db,
		templates: tmpl,
		config:    conf,
	}
}

func (v *ViewCalHandler) ServeHTTP(
	rw http.ResponseWriter, req *http.Request) {
	var md calendarViewData
	var ts time.Time
	var week int64
	var err error
	var offset int

	req.ParseForm()

	if len(req.FormValue("week")) > 0 {
		// The user entered a specific week number, so we need to build
		// from that.
		week, err = strconv.ParseInt(req.FormValue("week"), 10, 64)
		if err != nil {
			rw.WriteHeader(http.StatusBadRequest)
			io.WriteString(rw, "Error parsing week input: "+
				err.Error()+"\r\n")
			log.Print("Error parsing week input: ", err)
			return
		}
	} else {
		// No week specified — we'll have to find out.
		week = getWeekFromTimestamp(time.Now())
	}

	// Get the timestamp of the start of the week.
	ts = time.Unix(0, 0)
	ts = ts.Add(time.Duration(week)*24*7*time.Hour +
		12*7*time.Hour).Truncate(24 * 7 * time.Hour)
	_, offset = ts.Zone()
	ts = ts.Add(time.Duration(-offset) * time.Second)
	md.Weekstart = ts
	md.WeekstartText = ts.Format("Mon 2 Jan 2006")
	md.WeekNumber = week
	md.PreviousWeek = week - 1
	md.NextWeek = week + 1
	md.Days = make([]string, 0)
	md.Events = make([][]*Event, 0)

	if md.PreviousWeek < 0 {
		md.PreviousWeek = 0
	}

	for weekday := 0; weekday < 7; weekday++ {
		var dayend time.Time = ts.Add(36 * time.Hour).Truncate(24 * time.Hour)
		var events []*Event = make([]*Event, 0)

		// We have to determine the offset again, since it may have changed
		// due to DST or similar.
		_, offset = ts.Zone()
		dayend = dayend.Add(time.Duration(-offset) * time.Second)

		events, err = FetchEventRange(v.db, v.config,
			ts, dayend, false)
		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			io.WriteString(rw, "Error fetching events for "+ts.String()+
				": "+err.Error()+"\r\n")
			log.Print("Error fetching events for ", ts, ": ", err)
			return
		}

		md.Events = append(md.Events, events)
		md.Days = append(md.Days, ts.Format("Mon 2 Jan"))
		ts = dayend
	}

	v.am.GenAuthDetails(req, &md.Auth)

	err = v.templates.ExecuteTemplate(rw, "viewcalendar.html", &md)
	if err != nil {
		rw.WriteHeader(http.StatusInternalServerError)
		io.WriteString(rw, "Error displaying calendar: "+err.Error()+"\r\n")
		log.Print("Error displaying calendar: ", err)
	}
}
