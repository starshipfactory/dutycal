package main

import (
	"database/cassandra"
	"flag"
	"io/ioutil"
	"log"

	"github.com/golang/protobuf/proto"
	"github.com/starshipfactory/dutycal"
)

func main() {
	var db *cassandra.RetryCassandraClient
	var ire *cassandra.InvalidRequestException
	var rev *dutycal.RecurringEvent
	var config dutycal.DutyCalConfig
	var config_path string
	var configdata []byte
	var err error

	flag.StringVar(&config_path, "config", "",
		"Path to the configuration file")
	flag.Parse()

	if len(config_path) == 0 {
		flag.Usage()
		log.Fatal("No config file has been specified")
	}

	configdata, err = ioutil.ReadFile(config_path)
	if err != nil {
		log.Fatal("Error reading config file ", config_path, ": ", err)
	}

	err = proto.UnmarshalText(string(configdata), &config)
	if err != nil {
		log.Fatal("Error reading config file: ", err)
	}

	db, err = cassandra.NewRetryCassandraClient(config.GetDbServer())
	if err != nil {
		log.Fatal("Error connecting to Cassandra at ",
			config.GetDbServer(), ": ", err)
	}

	ire, err = db.SetKeyspace(config.GetKeyspace())
	if ire != nil {
		log.Fatal("Error switching keyspace to ", config.GetKeyspace(),
			": ", ire.Why)
	}
	if err != nil {
		log.Fatal("Error switching keyspace to ", config.GetKeyspace(),
			": ", err)
	}

	// For each recurring event, make sure we have enough scheduled for the
	// near future.
	for _, rev = range config.RecurringEvents {
		ScheduleRecurringEvent(db, &config, rev)
	}
}
