package discovery

import (
	"fmt"

	"github.com/hashicorp/consul/api"
)

func Register(client *api.Client, id, name, address string, port int) error {
	registration := &api.AgentServiceRegistration{
		ID:      id,
		Name:    name,
		Address: address,
		Port:    port,
		Check: &api.AgentServiceCheck{
			HTTP:     fmt.Sprintf("http://%s:%d/health/live", address, port),
			Interval: "10s",
			Timeout:  "2s",
		},
	}
	return client.Agent().ServiceRegister(registration)
}
