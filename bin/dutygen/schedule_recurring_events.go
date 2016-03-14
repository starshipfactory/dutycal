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

// ScheduleWeekdayRecurringEvent schedules a recurring event which is based
// on weekday recurrence, i.e. weekly on the same week day.
func ScheduleWeekdayRecurringEvent(
	start time.Time, db *cassandra.RetryCassandraClient,
	conf *dutycal.DutyCalConfig, loc *time.Location,
	rev *dutycal.RecurringEvent) {
	var duration time.Duration
	var nextEv time.Time = start
	var endTime time.Time
	var u *url.URL

	if rev.Reference != nil {
		u, _ = url.Parse(rev.GetReference())
	}

	duration = time.Duration(rev.GetDurationHours())*time.Hour +
		time.Duration(rev.GetDurationMinutes())*time.Minute

	// Timezone specific Truncate()
	nextEv = nextEv.Add(-1 * time.Duration(nextEv.Hour()) * time.Hour).
		Add(-1 * time.Duration(nextEv.Minute()) * time.Minute).
		Add(-1 * time.Duration(nextEv.Second()) * time.Second).
		Add(-1 * time.Duration(nextEv.Nanosecond()) * time.Nanosecond)

	endTime = nextEv.AddDate(
		0, 0, int(conf.GetRecurringEventsScheduleAhead()))

	// Find the next fitting week day.
	nextEv = nextEv.AddDate(0, 0,
		7-int(nextEv.Weekday())+int(rev.GetRecurrenceSelector())).
		Add(time.Duration(rev.GetStartHour()) * time.Hour).
		Add(time.Duration(rev.GetStartMinute()) * time.Minute)

	for nextEv.Before(endTime) {
		// Now, let's determine if there is already a scheduled event during
		// that time.
		var genid []byte = genGeneratorID(nextEv, duration, rev.GetTitle(),
			rev.GetDescription())
		var evs []*dutycal.Event
		var ev *dutycal.Event
		var found bool = false
		var err error

		evs, err = dutycal.FetchEventRange(
			db, conf, nextEv, nextEv.Add(duration), -1, loc, nil, true)
		if err != nil {
			log.Print("Error fetching events from ",
				nextEv, " to ", nextEv.Add(duration), ": ", err)
			return
		}

		for _, ev = range evs {
			if bytes.Compare(ev.GeneratorID, genid) != 0 {
				continue
			}

			// More checks may go here.

			found = true
		}

		if !found {
			ev = dutycal.CreateEvent(db, conf, rev.GetTitle(),
				rev.GetDescription(), "", nextEv, duration, loc, u,
				rev.GetRequired())
			ev.GeneratorID = genid
			err = ev.Sync()
			if err != nil {
				log.Print("Error creating event from ",
					nextEv.Format(time.RFC1123Z), " to ",
					nextEv.Add(duration).Format(time.RFC1123Z),
					": ", err)
			}
		}
		nextEv = nextEv.AddDate(0, 0, 7)
	}
}

// ScheduleRecurringEvent schedules a recurring event based on what recurrence
// type was defined in the configuration file.
func ScheduleRecurringEvent(
	start time.Time, db *cassandra.RetryCassandraClient,
	conf *dutycal.DutyCalConfig, loc *time.Location,
	rev *dutycal.RecurringEvent) {
	if rev.GetRecurrenceType() == dutycal.RecurringEvent_WEEKDAY {
		ScheduleWeekdayRecurringEvent(start, db, conf, loc, rev)
	} else {
		log.Print("Don't know how to schedule a recurrence of type ",
			rev.GetRecurrenceType())
	}
}
