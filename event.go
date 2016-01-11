package dutycal

import (
	"crypto/sha256"
	"database/cassandra"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"time"
)

var kEventAllColumns [][]byte = [][]byte{
	[]byte("title"), []byte("description"), []byte("owner"),
	[]byte("start"), []byte("end"), []byte("required"), []byte("week"),
}

type Event struct {
	db   *cassandra.RetryCassandraClient
	conf *DutyCalConfig

	Id          string
	Title       string
	Description string
	Start       time.Time
	Duration    time.Duration
	Owner       string
	Required    bool
}

func getWeekFromTimestamp(ts time.Time) int64 {
	var offset int
	_, offset = ts.Zone()
	return (ts.Unix() + int64(offset)) / (7 * 24 * 60 * 60)
}

// Create a new event with the speicfied details.
func CreateEvent(db *cassandra.RetryCassandraClient, conf *DutyCalConfig,
	title, description, owner string,
	start time.Time, duration time.Duration, required bool) *Event {
	return &Event{
		db:   db,
		conf: conf,

		Title:       title,
		Description: description,
		Start:       start,
		Duration:    duration,
		Owner:       owner,
		Required:    required,
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

	rv = &Event{
		db:   db,
		conf: conf,
		Id:   id,
	}
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

	parent = cassandra.NewColumnParent()
	parent.ColumnFamily = conf.GetEventsColumnFamily()
	clause = cassandra.NewIndexClause()
	clause.StartKey = make([]byte, 0)
	clause.Count = int32((conf.GetMaxEventsPerDay()*int32(duration.Hours()))/24) + 1
	predicate = cassandra.NewSlicePredicate()
	predicate.ColumnNames = kEventAllColumns

	expr = cassandra.NewIndexExpression()
	expr.ColumnName = []byte("week")
	expr.Op = cassandra.IndexOperator_EQ
	expr.Value = make([]byte, 8)
	binary.BigEndian.PutUint64(expr.Value, uint64(getWeekFromTimestamp(from)))
	clause.Expressions = append(clause.Expressions, expr)

	expr = cassandra.NewIndexExpression()
	expr.ColumnName = []byte("start")
	expr.Op = cassandra.IndexOperator_LTE
	expr.Value = make([]byte, 8)
	binary.BigEndian.PutUint64(expr.Value, uint64(to.Unix()*1000))
	clause.Expressions = append(clause.Expressions, expr)

	expr = cassandra.NewIndexExpression()
	expr.ColumnName = []byte("end")
	expr.Op = cassandra.IndexOperator_GTE
	expr.Value = make([]byte, 8)
	binary.BigEndian.PutUint64(expr.Value, uint64(from.Unix()*1000))
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
		err = fmt.Errorf(
			"Invalid request error in index reading from %s to %s: %s",
			from.String(), to.String(), ire.Why)
		return rv, err
	}
	if ue != nil {
		err = fmt.Errorf("Cassandra unavailable when reading from %s to %s",
			from.String(), to.String())
		return rv, err
	}
	if te != nil {
		err = fmt.Errorf("Cassandra timed out when reading from %s to %s: %s",
			from.String(), to.String(), te.String())
		return rv, err
	}

	for _, ks = range res {
		var e *Event = &Event{
			db:   db,
			conf: conf,
			Id:   string(ks.Key),
		}

		err = e.extractFromColumns(ks.Columns)
		if err != nil {
			return rv, err
		}

		rv = append(rv, e)
	}

	return rv, nil
}

// Extract event data from a number of columns.
func (e *Event) extractFromColumns(r []*cassandra.ColumnOrSuperColumn) error {
	var cos *cassandra.ColumnOrSuperColumn
	var end time.Time

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
			var start int64

			start = int64(binary.BigEndian.Uint64(col.Value))
			e.Start = time.Unix(start/1000, (start%1000)*1000)
		} else if cname == "end" {
			var end_ts int64

			end_ts = int64(binary.BigEndian.Uint64(col.Value))
			end = time.Unix(end_ts/1000, (end_ts%1000)*1000)
		} else if cname == "required" {
			e.Required = (len(col.Value) > 0 && col.Value[0] > 0)
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
	return fmt.Sprintf("%08X:%08X:%s.%s", getWeekFromTimestamp(e.Start),
		e.Start.Unix(), e.Duration.String(), hex.EncodeToString(etitle[:]))
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

	if len(e.Id) == 0 {
		e.Id = e.genEventID()
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
	col.Value = make([]byte, 8)
	binary.BigEndian.PutUint64(
		col.Value, uint64(e.Start.Unix()*1000))
	col.Timestamp = ts

	mutation = cassandra.NewMutation()
	mutation.ColumnOrSupercolumn = cassandra.NewColumnOrSuperColumn()
	mutation.ColumnOrSupercolumn.Column = col
	mutations = append(mutations, mutation)

	col = cassandra.NewColumn()
	col.Name = []byte("end")
	col.Value = make([]byte, 8)
	binary.BigEndian.PutUint64(
		col.Value, uint64(e.Start.Add(e.Duration).Unix()*1000))
	col.Timestamp = ts

	mutation = cassandra.NewMutation()
	mutation.ColumnOrSupercolumn = cassandra.NewColumnOrSuperColumn()
	mutation.ColumnOrSupercolumn.Column = col
	mutations = append(mutations, mutation)

	col = cassandra.NewColumn()
	col.Name = []byte("required")
	if e.Required {
		col.Value = []byte{1}
	} else {
		col.Value = []byte{0}
	}
	col.Timestamp = ts

	mutation = cassandra.NewMutation()
	mutation.ColumnOrSupercolumn = cassandra.NewColumnOrSuperColumn()
	mutation.ColumnOrSupercolumn.Column = col
	mutations = append(mutations, mutation)

	col = cassandra.NewColumn()
	col.Name = []byte("week")
	col.Value = make([]byte, 8)
	binary.BigEndian.PutUint64(
		col.Value, uint64(getWeekFromTimestamp(e.Start)))
	col.Timestamp = ts

	mutation = cassandra.NewMutation()
	mutation.ColumnOrSupercolumn = cassandra.NewColumnOrSuperColumn()
	mutation.ColumnOrSupercolumn.Column = col
	mutations = append(mutations, mutation)

	col = cassandra.NewColumn()
	col.Name = []byte("end")
	col.Value = make([]byte, 8)
	binary.BigEndian.PutUint64(
		col.Value, uint64(e.Start.Add(e.Duration).Unix()*1000))
	col.Timestamp = ts

	mutation = cassandra.NewMutation()
	mutation.ColumnOrSupercolumn = cassandra.NewColumnOrSuperColumn()
	mutation.ColumnOrSupercolumn.Column = col
	mutations = append(mutations, mutation)

	mmap = make(map[string]map[string][]*cassandra.Mutation)
	mmap[e.Id] = make(map[string][]*cassandra.Mutation)
	mmap[e.Id][e.conf.GetEventsColumnFamily()] = mutations

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
			e.Id)
	}
	if te != nil {
		return fmt.Errorf("Cassandra timed out when updating %s: %s",
			e.Id, te.String())
	}

	return nil
}
