package main

import (
	// "database/cassandra"
	"flag"
	"io/ioutil"
	"log"

	"github.com/golang/protobuf/proto"
	"github.com/starshipfactory/dutycal"
)

func main() {
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
}
