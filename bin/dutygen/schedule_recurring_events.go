package main

import (
	"bytes"
	"crypto/sha256"
	"database/cassandra"
	"hash"
	"log"
	"net/url"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/starshipfactory/dutycal"
)

func genGeneratorID(start time.Time, duration time.Duration, title, description string) []byte {
	var h hash.Hash
	var genid GeneratorID
	var rv []byte

	h = sha256.New()
	h.Write([]byte(title))
	h.Write([]byte(description))

	genid.StartTimestamp = proto.Int64(start.Unix())
	genid.Duration = proto.Int64(int64(duration.Seconds()))
	genid.ContentHash = h.Sum([]byte{})

	rv, _ = proto.Marshal(&genid)
	return rv
}

func ScheduleWeekdayRecurringEvent(
	db *cassandra.RetryCassandraClient, conf *dutycal.DutyCalConfig,
	rev *dutycal.RecurringEvent) {
	var duration time.Duration
	var next_ev time.Time = time.Now()
	var end_time time.Time
	var u *url.URL

	if rev.Reference != nil {
		u, _ = url.Parse(rev.GetReference())
	}

	duration = time.Duration(rev.GetDurationHours())*time.Hour +
		time.Duration(rev.GetDurationMinutes())*time.Minute

	// Timezone specific Truncate()
	next_ev = next_ev.Add(-1 * time.Duration(next_ev.Hour()) * time.Hour).
		Add(-1 * time.Duration(next_ev.Minute()) * time.Minute).
		Add(-1 * time.Duration(next_ev.Second()) * time.Second).
		Add(-1 * time.Duration(next_ev.Nanosecond()) * time.Nanosecond)

	end_time = next_ev.AddDate(
		0, 0, int(conf.GetRecurringEventsScheduleAhead()))

	// Find the next fitting week day.
	next_ev = next_ev.AddDate(0, 0,
		7-int(next_ev.Weekday())+int(rev.GetRecurrenceSelector())).
		Add(time.Duration(rev.GetStartHour()) * time.Hour).
		Add(time.Duration(rev.GetStartMinute()) * time.Minute)

	for next_ev.Before(end_time) {
		// Now, let's determine if there is already a scheduled event during
		// that time.
		var genid []byte = genGeneratorID(next_ev, duration, rev.GetTitle(),
			rev.GetDescription())
		var evs []*dutycal.Event
		var ev *dutycal.Event
		var found bool = false
		var err error

		evs, err = dutycal.FetchEventRange(
			db, conf, next_ev, next_ev.Add(duration), true)
		if err != nil {
			log.Print("Error fetching events from ",
				next_ev, " to ", next_ev.Add(duration), ": ", err)
			return
		}

		for _, ev = range evs {
			if bytes.Compare(ev.GeneratorID, genid) != 0 {
				continue
			}

			// More checks may go here.

			log.Print("Found event matching generator ID: ",
				ev.Id, " from ", ev.Start.Format(time.RFC1123Z),
				" titled ", ev.Title)
			found = true
		}

		if !found {
			ev = dutycal.CreateEvent(db, conf, rev.GetTitle(),
				rev.GetDescription(), "", next_ev, duration, u,
				rev.GetRequired())
			ev.GeneratorID = genid
			err = ev.Sync()
			if err != nil {
				log.Print("Error creating event from ",
					next_ev.Format(time.RFC1123Z), " to ",
					next_ev.Add(duration).Format(time.RFC1123Z),
					": ", err)
			}
		}
		next_ev = next_ev.AddDate(0, 0, 7)
	}
}

func ScheduleRecurringEvent(
	db *cassandra.RetryCassandraClient, conf *dutycal.DutyCalConfig,
	rev *dutycal.RecurringEvent) {
	if rev.GetRecurrenceType() == dutycal.RecurringEvent_WEEKDAY {
		ScheduleWeekdayRecurringEvent(db, conf, rev)
	} else {
		log.Print("Don't know how to schedule a recurrence of type ",
			rev.GetRecurrenceType())
	}
}
