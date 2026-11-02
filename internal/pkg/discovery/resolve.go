package discovery

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/hashicorp/consul/api"
)

func Resolve(ctx context.Context, client *api.Client, name string) (string, error) {
	// 创建一个指针，所有字段取0值，对刚创建的指针调用withoutContext，把ctx写入内部未到出字段ctx，返回一个新的queryOption指针
	// 构造一个带上下文、其余取默认值的 Consul 查询选项对象

	options := (&api.QueryOptions{}).WithContext(ctx)

	entries, _, err := client.Health().Service(name, "", true, options)
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
