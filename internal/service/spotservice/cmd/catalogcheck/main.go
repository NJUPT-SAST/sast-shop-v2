package main

import (
	"context"
	"log"
	"time"

	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/client"
	"github.com/hashicorp/consul/api"
)

func main() {
	config.Init()
	cfg := config.AppConfig
	consulCfg := api.DefaultConfig()
	consulCfg.Address = cfg.ConsulAddress

	consulClient, err := api.NewClient(consulCfg)
	if err != nil {
		log.Fatalf("create consul client: %v", err)
	}
	client.Init(consulClient, cfg.CatalogConsulName)
	log.Printf("[check] Consul address: %s, service: %s", cfg.ConsulAddress, cfg.CatalogConsulName)

	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		reqMsg := &catalogv1.GetProductTemplatesRequest{
			ProductTemplateIds: []int64{1},
		}
		resp, err := client.CatalogInternalServiceClient.GetProductTemplates(
			ctx,
			connect.NewRequest(reqMsg),
		)
		cancel()

		if err != nil {
			log.Printf("[check] get templates failed: %v", err)
		} else {
			log.Printf("[check] get templates ok: %d items", len(resp.Msg.GetProductTemplates()))
		}

		time.Sleep(3 * time.Second)
	}
}
