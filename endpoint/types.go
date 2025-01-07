package endpoint

import "net"

type Endpoint struct {
	Hostname  string     `yaml:"hostname"`
	IP        string     `yaml:"ip"`
	Tags      []string   `yaml:"tags"`
	Listeners []Listener `yaml:"listeners"`
}

type ListenerType string

const (
	ListenerTypeTLS ListenerType = "tls"
)

type Listener struct {
	Type ListenerType `yaml:"type"`
	Port uint16       `yaml:"port"`

	listener net.Listener
}
