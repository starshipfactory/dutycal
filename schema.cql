CREATE KEYSPACE dutycal WITH replication = {'class': 'SimpleStrategy', 'replication_factor' : 1};

CREATE COLUMNFAMILY events (
    key ascii PRIMARY KEY,
    title text,
    description text,
    owner ascii,
    start timestamp,
    end timestamp,
    required bool,
    week int64,
    reference ascii,
    generatorID blob
);
CREATE INDEX ON events (start);
CREATE INDEX ON events (end);
CREATE INDEX ON events (owner);
CREATE INDEX ON events (week);
