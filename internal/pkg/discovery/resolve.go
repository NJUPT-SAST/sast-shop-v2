package discovery

import (
	"fmt"
	"net"
	"strconv"

	"github.com/hashicorp/consul/api"
)

func Resolve(client *api.Client, name string) (string, error) {
	entries, _, err := client.Health().Service(name, "", true, nil)
	if err != nil {
		return "", fmt.Errorf("discover service %q: %w", name, err)
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("no healthy instances for service %q", name)
	}

	service := entries[0].Service
	address := net.JoinHostPort(service.Address, strconv.Itoa(service.Port))
	return "http://" + address, nil
}
