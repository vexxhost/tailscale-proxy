package endpoint

import "net"

type Endpoint struct {
	Hostname  string     `yaml:"hostname"`
	IP        string     `yaml:"ip"`
	Tags      []string   `yaml:"tags"`
	Ephemeral *bool      `yaml:"ephemeral"`
	Listeners []Listener `yaml:"listeners"`
}

// ephemeral reports whether the endpoint registers as an ephemeral node,
// which defaults to true when unset.
func (ep *Endpoint) ephemeral() bool {
	return ep.Ephemeral == nil || *ep.Ephemeral
}

type ListenerType string

const (
	ListenerTypeTLS ListenerType = "tls"
	ListenerTypeUDP ListenerType = "udp"
)

type Listener struct {
	Type ListenerType `yaml:"type"`
	Port uint16       `yaml:"port"`

	listener net.Listener
}
