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

// Recreate in-memory event object from database. The record "id" is read
// from the database designated as "db" as specified in the configuration
// "conf". If "quorum" is specified, a quorum read from the database will
// be performed rather than just reading from a single replica.
func FetchEvent(db *cassandra.RetryCassandraClient, conf *DutyCalConfig,
	id string, quorum bool) (rv *Event, err error) {
	var cp *cassandra.ColumnParent = cassandra.NewColumnParent()
	var pred *cassandra.SlicePredicate = cassandra.NewSlicePredicate()
	var cl cassandra.ConsistencyLevel

	var r []*cassandra.ColumnOrSuperColumn
	var ire *cassandra.InvalidRequestException
	var ue *cassandra.UnavailableException
	var te *cassandra.TimedOutException

	cp.ColumnFamily = conf.GetEventsColumnFamily()
	pred.ColumnNames = kEventAllColumns

	if quorum {
		cl = cassandra.ConsistencyLevel_QUORUM
	} else {
		cl = cassandra.ConsistencyLevel_ONE
	}

	r, ire, ue, te, err = db.GetSlice([]byte(id), cp, pred, cl)
	if ire != nil {
		err = fmt.Errorf("Invalid request error fetching event %s: %s",
			id, ire.Why)
		return
	}
	if ue != nil {
		err = fmt.Errorf("Cassandra unavailable fetching event %s", id)
		return
	}
	if te != nil {
		err = fmt.Errorf("Request for %s timed out: %s",
			id, te.String())
		return
	}
	if err != nil {
		return
	}

	rv = &Event{id: id}
	err = rv.extractFromColumns(r)
	return
}

// Retrieve a list of all events between the two specified dates.
func FetchEventRange(db *cassandra.RetryCassandraClient, conf *DutyCalConfig,
	from, to time.Time, quorum bool) ([]*Event, error) {
	var parent *cassandra.ColumnParent
	var clause *cassandra.IndexClause
	var predicate *cassandra.SlicePredicate
	var expr *cassandra.IndexExpression
	var cl cassandra.ConsistencyLevel

	var res []*cassandra.KeySlice
	var ire *cassandra.InvalidRequestException
	var ue *cassandra.UnavailableException
	var te *cassandra.TimedOutException
	var err error

	var ks *cassandra.KeySlice
	var rv []*Event
	var duration time.Duration

	if from.After(to) {
		duration = from.Sub(to)
	} else {
		duration = to.Sub(from)
	}

	parent.ColumnFamily = conf.GetEventsColumnFamily()
	clause.Count = int32(conf.GetMaxEventsPerDay()/int32(duration.Hours()*24)) + 1
	predicate.ColumnNames = kEventAllColumns

	expr = cassandra.NewIndexExpression()
	expr.ColumnName = []byte("start")
	expr.Op = cassandra.IndexOperator_LTE
	expr.Value, err = to.MarshalBinary()
	if err != nil {
		return []*Event{}, err
	}
	clause.Expressions = append(clause.Expressions, expr)

	expr = cassandra.NewIndexExpression()
	expr.ColumnName = []byte("end")
	expr.Op = cassandra.IndexOperator_GTE
	expr.Value, err = from.MarshalBinary()
	if err != nil {
		return []*Event{}, err
	}
	clause.Expressions = append(clause.Expressions, expr)

	if quorum {
		cl = cassandra.ConsistencyLevel_QUORUM
	} else {
		cl = cassandra.ConsistencyLevel_ONE
	}

	res, ire, ue, te, err = db.GetIndexedSlices(
		parent, clause, predicate, cl)
	if err != nil {
		return []*Event{}, err
	}
	if ire != nil {
		err = fmt.Errorf("Invalid request error in index reading: %s",
			ire.Why)
		return []*Event{}, err
	}
	if ue != nil {
		err = fmt.Errorf("Cassandra unavailable when reading from %s to %s",
			from.String(), to.String())
		return []*Event{}, err
	}
	if te != nil {
		err = fmt.Errorf("Cassandra timed out when reading from %s to %s: %s",
			from.String(), to.String(), te.String())
		return []*Event{}, err
	}

	for _, ks = range res {
		var e *Event = &Event{
			id: string(ks.Key),
		}

		err = e.extractFromColumns(ks.Columns)
		if err != nil {
			return []*Event{}, err
		}

		rv = append(rv, e)
	}

	return rv, nil
}

// Extract event data from a number of columns.
func (e *Event) extractFromColumns(r []*cassandra.ColumnOrSuperColumn) error {
	var cos *cassandra.ColumnOrSuperColumn
	var end time.Time
	var err error

	for _, cos = range r {
		var col *cassandra.Column = cos.Column
		var cname string

		if col == nil {
			continue
		}

		cname = string(col.Name)

		if cname == "title" {
			e.Title = string(col.Value)
		} else if cname == "description" {
			e.Description = string(col.Value)
		} else if cname == "owner" {
			e.Owner = string(col.Value)
		} else if cname == "start" {
			err = e.Start.UnmarshalBinary(col.Value)
			if err != nil {
				return err
			}
		} else if cname == "end" {
			err = end.UnmarshalBinary(col.Value)
			if err != nil {
				return err
			}
		}
	}

	if e.Start.After(end) {
		e.Duration = e.Start.Sub(end)
		e.Start = end
	} else {
		e.Duration = end.Sub(e.Start)
	}

	return nil
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
		return fmt.Errorf("Invalid request error in batch mutation: %s",
			ire.Why)
	}
	if ue != nil {
		return fmt.Errorf("Cassandra unavailable when updating %s",
			e.id)
	}
	if te != nil {
		return fmt.Errorf("Cassandra timed out when updating %s: %s",
			e.id, te.String())
	}

	return nil
}
