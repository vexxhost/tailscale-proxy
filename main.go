package main

import (
	"flag"
	"log"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/vexxhost/tailscale-proxy/endpoint"
)

var configFile = flag.String("config", "config.yml", "path to config file")

type Config struct {
	Endpoints []endpoint.Endpoint
}

func init() {
	flag.Parse()
}

func main() {
	config := &Config{}
	file, err := os.Open(*configFile)
	if err != nil {
		log.Fatalf("failed to open config file: %v", err)
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	if err := decoder.Decode(config); err != nil {
		log.Fatalf("failed to decode config file: %v", err)
	}

	for _, ep := range config.Endpoints {
		go func(ep endpoint.Endpoint) {
			ep.Start()
		}(ep)

		defer ep.Close()
	}

	select {}
}
