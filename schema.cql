CREATE KEYSPACE dutycal WITH replication = {'class': 'SimpleStrategy', 'replication_factor' : 1};

CREATE COLUMNFAMILY events (
    key blob PRIMARY KEY,
    title text,
    description text,
    owner ascii,
    start timestamp,
    end timestamp
);
CREATE INDEX ON events (start);
CREATE INDEX ON events (end);
CREATE INDEX ON events (owner);
