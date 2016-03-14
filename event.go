package dutycal

import (
	"crypto/sha256"
	"database/cassandra"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"time"
)

var eventAllColumns [][]byte = [][]byte{
	[]byte("title"), []byte("description"), []byte("owner"),
	[]byte("start"), []byte("end"), []byte("required"), []byte("week"),
	[]byte("reference"), []byte("generatorID"),
}

// Event is an object representing individual event calendar entries.
// They can be loaded from database or written to it.
type Event struct {
	db   *cassandra.RetryCassandraClient
	conf *DutyCalConfig

	ID          string
	Title       string
	Description string
	Start       time.Time
	Duration    time.Duration
	Owner       string
	Reference   *url.URL
	Required    bool
	GeneratorID []byte

	location *time.Location
	updateTS int64
}

func getWeekFromTimestamp(ts time.Time) int64 {
	var offset int
	_, offset = ts.Zone()
	// We'll have to add 3 days because Jan 1 1970 was a Thursday
	return ts.Add(time.Duration(offset)*time.Second).Add(3*24*time.Hour).Unix() / (7 * 24 * 60 * 60)
}

// CreateEvent creates a new event with the speicfied details.
func CreateEvent(db *cassandra.RetryCassandraClient, conf *DutyCalConfig,
	title, description, owner string,
	start time.Time, duration time.Duration, location *time.Location,
	reference *url.URL, required bool) *Event {
	return &Event{
		db:   db,
		conf: conf,

		location: location,

		Title:       title,
		Description: description,
		Start:       start.In(location),
		Duration:    duration,
		Owner:       owner,
		Reference:   reference,
		Required:    required,
	}
}

// FetchEvent recreates in-memory event objects from the database. The record
// "id" is read from the database designated as "db" as specified in the
// configuration "conf". If "quorum" is specified, a quorum read from the
// database will be performed rather than just reading from a single replica.
func FetchEvent(db *cassandra.RetryCassandraClient, conf *DutyCalConfig,
	id string, loc *time.Location, quorum bool) (
	rv *Event, err error) {
	var cp *cassandra.ColumnParent = cassandra.NewColumnParent()
	var pred *cassandra.SlicePredicate = cassandra.NewSlicePredicate()
	var cl cassandra.ConsistencyLevel
	var r []*cassandra.ColumnOrSuperColumn

	cp.ColumnFamily = conf.GetEventsColumnFamily()
	pred.ColumnNames = eventAllColumns

	if quorum {
		cl = cassandra.ConsistencyLevel_QUORUM
	} else {
		cl = cassandra.ConsistencyLevel_ONE
	}

	r, err = db.GetSlice([]byte(id), cp, pred, cl)
	if err != nil {
		return
	}

	rv = &Event{
		db:   db,
		conf: conf,
		ID:   id,
	}
	err = rv.extractFromColumns(r, loc)
	return
}

// FetchEventRange retrieves a list of all events between the two specified
// dates. If a limit is given, only up to that many records will be returned.
// If "user" is not nil, the user must match the specified user (e.g. an empty
// string for unassigned slots).
func FetchEventRange(db *cassandra.RetryCassandraClient, conf *DutyCalConfig,
	from, to time.Time, limit int32, loc *time.Location, user *string,
	quorum bool) ([]*Event, error) {
	var parent *cassandra.ColumnParent
	var clause *cassandra.IndexClause
	var predicate *cassandra.SlicePredicate
	var expr *cassandra.IndexExpression
	var cl cassandra.ConsistencyLevel

	var res []*cassandra.KeySlice
	var err error

	var ks *cassandra.KeySlice
	var rv []*Event
	var duration time.Duration

	if to.Unix() != 0 && from.After(to) {
		duration = from.Sub(to)
	} else {
		duration = to.Sub(from)
	}

	parent = cassandra.NewColumnParent()
	parent.ColumnFamily = conf.GetEventsColumnFamily()
	clause = cassandra.NewIndexClause()
	// TODO(caoimhe): this could start from the start timestamp…
	clause.StartKey = make([]byte, 0)
	if limit > 0 {
		clause.Count = limit
	} else {
		clause.Count = int32(
			(conf.GetMaxEventsPerDay()*int32(duration.Hours()))/24) + 1
	}
	predicate = cassandra.NewSlicePredicate()
	predicate.ColumnNames = eventAllColumns

	expr = cassandra.NewIndexExpression()
	expr.ColumnName = []byte("week")
	expr.Op = cassandra.IndexOperator_EQ
	expr.Value = make([]byte, 8)
	binary.BigEndian.PutUint64(expr.Value, uint64(getWeekFromTimestamp(from)))
	clause.Expressions = append(clause.Expressions, expr)

	if user != nil {
		expr = cassandra.NewIndexExpression()
		expr.ColumnName = []byte("owner")
		expr.Op = cassandra.IndexOperator_EQ
		expr.Value = []byte(*user)
		clause.Expressions = append(clause.Expressions, expr)
	}

	if to.Unix() != 0 {
		expr = cassandra.NewIndexExpression()
		expr.ColumnName = []byte("start")
		expr.Op = cassandra.IndexOperator_LTE
		expr.Value = make([]byte, 8)
		binary.BigEndian.PutUint64(expr.Value, uint64(to.Unix()*1000))
		clause.Expressions = append(clause.Expressions, expr)
	}

	if from.Unix() != 0 {
		expr = cassandra.NewIndexExpression()
		expr.ColumnName = []byte("end")
		expr.Op = cassandra.IndexOperator_GTE
		expr.Value = make([]byte, 8)
		binary.BigEndian.PutUint64(expr.Value, uint64(from.Unix()*1000))
		clause.Expressions = append(clause.Expressions, expr)
	}

	if quorum {
		cl = cassandra.ConsistencyLevel_QUORUM
	} else {
		cl = cassandra.ConsistencyLevel_ONE
	}

	res, err = db.GetIndexedSlices(
		parent, clause, predicate, cl)
	if err != nil {
		return []*Event{}, err
	}

	for _, ks = range res {
		var e *Event = &Event{
			db:   db,
			conf: conf,
			ID:   string(ks.Key),
		}

		err = e.extractFromColumns(ks.Columns, loc)
		if err != nil {
			return rv, err
		}

		rv = append(rv, e)
	}

	return rv, nil
}

// Extract event data from a number of columns.
func (e *Event) extractFromColumns(r []*cassandra.ColumnOrSuperColumn,
	loc *time.Location) error {
	var cos *cassandra.ColumnOrSuperColumn
	var end time.Time

	for _, cos = range r {
		var col *cassandra.Column = cos.Column
		var cname string

		if col == nil {
			continue
		}

		cname = string(col.Name)
		if col.IsSetTimestamp() {
			e.updateTS = col.GetTimestamp()
		}

		if cname == "title" {
			e.Title = string(col.Value)
		} else if cname == "description" {
			e.Description = string(col.Value)
		} else if cname == "owner" {
			e.Owner = string(col.Value)
		} else if cname == "start" {
			var start int64

			start = int64(binary.BigEndian.Uint64(col.Value))
			e.Start = time.Unix(start/1000, (start%1000)*1000).In(loc)
		} else if cname == "end" {
			var endTS int64

			endTS = int64(binary.BigEndian.Uint64(col.Value))
			end = time.Unix(endTS/1000, (endTS%1000)*1000).In(loc)
		} else if cname == "reference" {
			e.Reference, _ = url.Parse(string(col.Value))
		} else if cname == "required" {
			e.Required = (len(col.Value) > 0 && col.Value[0] > 0)
		} else if cname == "generatorID" {
			e.GeneratorID = col.Value
		}
	}

	if e.Start.After(end) {
		e.Duration = e.Start.Sub(end)
		e.Start = end
	} else {
		e.Duration = end.Sub(e.Start)
	}
	e.location = loc

	return nil
}

// Generate an event ID (but don't overwrite it).
func (e *Event) genEventID() string {
	var etitle [sha256.Size224]byte
	if len(e.Title) == 0 {
		return ""
	}
	etitle = sha256.Sum224([]byte(e.Title))
	return fmt.Sprintf("%08X:%016X:%s.%s", getWeekFromTimestamp(e.Start),
		e.Start.Unix(), e.Duration.String(), hex.EncodeToString(etitle[:]))
}

// Sync writes the modified event object back to the database.
func (e *Event) Sync() error {
	var mmap map[string]map[string][]*cassandra.Mutation
	var mutations []*cassandra.Mutation
	var mutation *cassandra.Mutation
	var col *cassandra.Column
	var ts int64
	var err error

	if len(e.ID) == 0 {
		e.ID = e.genEventID()
	}

	// Timestamps should be in microseconds.
	ts = time.Now().UnixNano() / 1000

	col = cassandra.NewColumn()
	col.Name = []byte("title")
	col.Value = []byte(e.Title)
	col.Timestamp = &ts

	mutation = cassandra.NewMutation()
	mutation.ColumnOrSupercolumn = cassandra.NewColumnOrSuperColumn()
	mutation.ColumnOrSupercolumn.Column = col
	mutations = append(mutations, mutation)

	col = cassandra.NewColumn()
	col.Name = []byte("description")
	col.Value = []byte(e.Description)
	col.Timestamp = &ts

	mutation = cassandra.NewMutation()
	mutation.ColumnOrSupercolumn = cassandra.NewColumnOrSuperColumn()
	mutation.ColumnOrSupercolumn.Column = col
	mutations = append(mutations, mutation)

	col = cassandra.NewColumn()
	col.Name = []byte("owner")
	col.Value = []byte(e.Owner)
	col.Timestamp = &ts

	mutation = cassandra.NewMutation()
	mutation.ColumnOrSupercolumn = cassandra.NewColumnOrSuperColumn()
	mutation.ColumnOrSupercolumn.Column = col
	mutations = append(mutations, mutation)

	col = cassandra.NewColumn()
	col.Name = []byte("start")
	col.Value = make([]byte, 8)
	binary.BigEndian.PutUint64(
		col.Value, uint64(e.Start.Unix()*1000))
	col.Timestamp = &ts

	mutation = cassandra.NewMutation()
	mutation.ColumnOrSupercolumn = cassandra.NewColumnOrSuperColumn()
	mutation.ColumnOrSupercolumn.Column = col
	mutations = append(mutations, mutation)

	col = cassandra.NewColumn()
	col.Name = []byte("end")
	col.Value = make([]byte, 8)
	binary.BigEndian.PutUint64(
		col.Value, uint64(e.Start.Add(e.Duration).Unix()*1000))
	col.Timestamp = &ts

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
	col.Timestamp = &ts

	mutation = cassandra.NewMutation()
	mutation.ColumnOrSupercolumn = cassandra.NewColumnOrSuperColumn()
	mutation.ColumnOrSupercolumn.Column = col
	mutations = append(mutations, mutation)

	col = cassandra.NewColumn()
	col.Name = []byte("week")
	col.Value = make([]byte, 8)
	binary.BigEndian.PutUint64(
		col.Value, uint64(getWeekFromTimestamp(e.Start)))
	col.Timestamp = &ts

	mutation = cassandra.NewMutation()
	mutation.ColumnOrSupercolumn = cassandra.NewColumnOrSuperColumn()
	mutation.ColumnOrSupercolumn.Column = col
	mutations = append(mutations, mutation)

	if e.Reference != nil {
		col = cassandra.NewColumn()
		col.Name = []byte("reference")
		col.Value = []byte(e.Reference.String())
		col.Timestamp = &ts

		mutation = cassandra.NewMutation()
		mutation.ColumnOrSupercolumn = cassandra.NewColumnOrSuperColumn()
		mutation.ColumnOrSupercolumn.Column = col
		mutations = append(mutations, mutation)
	}

	if len(e.GeneratorID) > 0 {
		col = cassandra.NewColumn()
		col.Name = []byte("generatorID")
		col.Value = e.GeneratorID
		col.Timestamp = &ts

		mutation = cassandra.NewMutation()
		mutation.ColumnOrSupercolumn = cassandra.NewColumnOrSuperColumn()
		mutation.ColumnOrSupercolumn.Column = col
		mutations = append(mutations, mutation)
	}

	mmap = make(map[string]map[string][]*cassandra.Mutation)
	mmap[e.ID] = make(map[string][]*cassandra.Mutation)
	mmap[e.ID][e.conf.GetEventsColumnFamily()] = mutations

	err = e.db.AtomicBatchMutate(mmap, cassandra.ConsistencyLevel_QUORUM)
	if err != nil {
		return err
	}

	// Update the update timestamp in case we want to delete the event again.
	e.updateTS = ts

	return nil
}

// Delete the database representation of the event.
func (e *Event) Delete() error {
	var sp *cassandra.SlicePredicate
	var mmap map[string]map[string][]*cassandra.Mutation
	var mutations []*cassandra.Mutation
	var mutation *cassandra.Mutation
	var err error

	if e.updateTS == 0 {
		return errors.New("Object not synced to database yet")
	}

	sp = cassandra.NewSlicePredicate()
	sp.ColumnNames = eventAllColumns

	mutation = cassandra.NewMutation()
	mutation.Deletion = cassandra.NewDeletion()
	mutation.Deletion.Timestamp = &e.updateTS
	mutation.Deletion.Predicate = sp
	mutations = append(mutations, mutation)

	mmap = make(map[string]map[string][]*cassandra.Mutation)
	mmap[e.ID] = make(map[string][]*cassandra.Mutation)
	mmap[e.ID][e.conf.GetEventsColumnFamily()] = mutations

	err = e.db.AtomicBatchMutate(mmap, cassandra.ConsistencyLevel_QUORUM)
	if err != nil {
		return err
	}

	return nil
}
