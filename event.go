package dutycal

import (
	"crypto/sha256"
	"database/cassandra"
	"encoding/hex"
	"fmt"
	"time"
)

var kEventAllColumns [][]byte = [][]byte{
	[]byte("title"), []byte("description"), []byte("owner"),
	[]byte("start"), []byte("end"),
}

type Event struct {
	db   *cassandra.RetryCassandraClient
	conf *DutyCalConfig

	id          string
	Title       string
	Description string
	Start       time.Time
	Duration    time.Duration
	Owner       string
}

// Create a new event with the speicfied details.
func CreateEvent(db *cassandra.RetryCassandraClient, conf *DutyCalConfig,
	title, description, owner string,
	start time.Time, duration time.Duration) *Event {
	return &Event{
		db:   db,
		conf: conf,

		Title:       title,
		Description: description,
		Start:       start,
		Duration:    duration,
		Owner:       owner,
	}
}

// Recreate in-memory event object from database.
func FetchEvent(db *cassandra.RetryCassandraClient, conf *DutyCalConfig,
	id string, quorum bool) (*Event, error) {
	var cp *cassandra.ColumnParent = cassandra.NewColumnParent()
	var pred *cassandra.SlicePredicate = cassandra.NewSlicePredicate()
	var cos *cassandra.ColumnOrSuperColumn
	var cl cassandra.ConsistencyLevel

	var title, description, owner string
	var start, end time.Time
	var duration time.Duration

	var r []*cassandra.ColumnOrSuperColumn
	var ire *cassandra.InvalidRequestException
	var ue *cassandra.UnavailableException
	var te *cassandra.TimedOutException
	var err error

	cp.ColumnFamily = conf.GetEventsColumnFamily()
	pred.ColumnNames = kEventAllColumns

	if quorum {
		cl = cassandra.ConsistencyLevel_QUORUM
	} else {
		cl = cassandra.ConsistencyLevel_ONE
	}

	r, ire, ue, te, err = db.GetSlice([]byte(id), cp, pred, cl)
	if ire != nil {
		return nil, fmt.Errorf("Invalid request error fetching event %s: %s",
			id, ire.Why)
	}
	if ue != nil {
		return nil, fmt.Errorf("Cassandra unavailable fetching event %s", id)
	}
	if te != nil {
		return nil, fmt.Errorf("Request for %s timed out: %s",
			id, te.String())
	}
	if err != nil {
		return nil, err
	}

	for _, cos = range r {
		var col *cassandra.Column = cos.Column
		var cname string

		if col == nil {
			continue
		}

		cname = string(col.Name)

		if cname == "title" {
			title = string(col.Value)
		} else if cname == "description" {
			description = string(col.Value)
		} else if cname == "owner" {
			owner = string(col.Value)
		} else if cname == "start" {
			err = start.UnmarshalBinary(col.Value)
			if err != nil {
				return nil, err
			}
		} else if cname == "end" {
			err = end.UnmarshalBinary(col.Value)
			if err != nil {
				return nil, err
			}
		}
	}

	duration = end.Sub(start)

	return &Event{
		id:          id,
		Title:       title,
		Description: description,
		Owner:       owner,
		Start:       start,
		Duration:    duration,
	}, nil
}

// Generate an event ID (but don't overwrite it).
func (e *Event) genEventID() string {
	var etitle [sha256.Size224]byte
	if len(e.Title) == 0 {
		return ""
	}
	etitle = sha256.Sum224([]byte(e.Title))
	return fmt.Sprintf("%08X-%s.%s", e.Start.Unix(), e.Duration.String(),
		hex.EncodeToString(etitle[:]))
}

// Write the modified event object to the database.
func (e *Event) Sync() error {
	var ire *cassandra.InvalidRequestException
	var ue *cassandra.UnavailableException
	var te *cassandra.TimedOutException
	var mmap map[string]map[string][]*cassandra.Mutation
	var mutations []*cassandra.Mutation
	var mutation *cassandra.Mutation
	var col *cassandra.Column
	var ts int64
	var err error

	if len(e.id) == 0 {
		e.id = e.genEventID()
	}

	ts = time.Now().UnixNano()

	col = cassandra.NewColumn()
	col.Name = []byte("title")
	col.Value = []byte(e.Title)
	col.Timestamp = ts

	mutation = cassandra.NewMutation()
	mutation.ColumnOrSupercolumn = cassandra.NewColumnOrSuperColumn()
	mutation.ColumnOrSupercolumn.Column = col
	mutations = append(mutations, mutation)

	col = cassandra.NewColumn()
	col.Name = []byte("description")
	col.Value = []byte(e.Description)
	col.Timestamp = ts

	mutation = cassandra.NewMutation()
	mutation.ColumnOrSupercolumn = cassandra.NewColumnOrSuperColumn()
	mutation.ColumnOrSupercolumn.Column = col
	mutations = append(mutations, mutation)

	col = cassandra.NewColumn()
	col.Name = []byte("owner")
	col.Value = []byte(e.Owner)
	col.Timestamp = ts

	mutation = cassandra.NewMutation()
	mutation.ColumnOrSupercolumn = cassandra.NewColumnOrSuperColumn()
	mutation.ColumnOrSupercolumn.Column = col
	mutations = append(mutations, mutation)

	col = cassandra.NewColumn()
	col.Name = []byte("start")
	col.Value, err = e.Start.MarshalBinary()
	if err != nil {
		return err
	}
	col.Timestamp = ts

	mutation = cassandra.NewMutation()
	mutation.ColumnOrSupercolumn = cassandra.NewColumnOrSuperColumn()
	mutation.ColumnOrSupercolumn.Column = col
	mutations = append(mutations, mutation)

	col = cassandra.NewColumn()
	col.Name = []byte("end")
	col.Value, err = e.Start.Add(e.Duration).MarshalBinary()
	if err != nil {
		return err
	}
	col.Timestamp = ts

	mutation = cassandra.NewMutation()
	mutation.ColumnOrSupercolumn = cassandra.NewColumnOrSuperColumn()
	mutation.ColumnOrSupercolumn.Column = col
	mutations = append(mutations, mutation)

	mmap = make(map[string]map[string][]*cassandra.Mutation)
	mmap[e.conf.GetKeyspace()] = make(map[string][]*cassandra.Mutation)
	mmap[e.conf.GetKeyspace()][e.conf.GetEventsColumnFamily()] = mutations

	ire, ue, te, err = e.db.AtomicBatchMutate(mmap,
		cassandra.ConsistencyLevel_QUORUM)
	if err != nil {
		return err
	}
	if ire != nil {
		err = fmt.Errorf("Invalid request error in batch mutation: %s",
			ire.Why)
	}
	if ue != nil {
		err = fmt.Errorf("Cassandra unavailable when updating %s",
			e.id)
	}
	if te != nil {
		err = fmt.Errorf("Cassandra timed out when updating %s: %s",
			e.id, te.String())
	}

	return nil
}
