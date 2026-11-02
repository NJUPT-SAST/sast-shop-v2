package discovery

import (
	"github.com/hashicorp/consul/api"
)

func Deregister(client *api.Client, id string) error {
	return client.Agent().ServiceDeregister(id)

}
