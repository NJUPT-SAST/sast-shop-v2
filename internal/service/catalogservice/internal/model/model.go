package model

import (
	"time"

	"github.com/uptrace/bun"
)

type CatalogStore struct {
	bun.BaseModel `bun:"table:catalog.catalog_store,alias:cs"`

	ID              int64         `bun:"id,pk,autoincrement"`
	Name            string        `bun:"name,notnull"`
	Address         string        `bun:"address,notnull"`
	LogoURL         string        `bun:"logo_url,notnull,default:''"`
	ThemeColor      string        `bun:"theme_color,notnull,default:'#FFFFFF'"`
	Status          CatalogStatus `bun:"status,notnull,default:'active'"`
	CreatedByUserID int64         `bun:"created_by_user_id,notnull"`
	CreatedAt       time.Time     `bun:"created_at,notnull,default:current_timestamp"`
	UpdatedAt       time.Time     `bun:"updated_at,notnull,default:current_timestamp"`
}

type CatalogProductTemplate struct {
	bun.BaseModel `bun:"table:catalog.catalog_product_template,alias:cpt"`

	ID              int64         `bun:"id,pk,autoincrement"`
	Title           string        `bun:"title,notnull"`
	Description     string        `bun:"description,notnull,default:''"`
	PriceCents      int32         `bun:"price_cents,notnull"`
	StoreID         int64         `bun:"store_id,notnull"`
	Status          CatalogStatus `bun:"status,notnull,default:'active'"`
	CreatedByUserID int64         `bun:"created_by_user_id,notnull"`
	PublishedAt     *time.Time    `bun:"published_at"`
	CreatedAt       time.Time     `bun:"created_at,notnull,default:current_timestamp"`
	UpdatedAt       time.Time     `bun:"updated_at,notnull,default:current_timestamp"`
}

type CatalogProductBarcode struct {
	bun.BaseModel `bun:"table:catalog.catalog_product_barcode,alias:cpb"`

	ID                int64     `bun:"id,pk,autoincrement"`
	ProductTemplateID int64     `bun:"product_template_id,notnull"`
	Barcode           string    `bun:"barcode,notnull"`
	CreatedAt         time.Time `bun:"created_at,notnull,default:current_timestamp"`
}

type CatalogProductImage struct {
	bun.BaseModel `bun:"table:catalog.catalog_product_image,alias:cpi"`

	ID                int64     `bun:"id,pk,autoincrement"`
	ProductTemplateID int64     `bun:"product_template_id,notnull"`
	ImageURL          string    `bun:"image_url,notnull"`
	SortOrder         int32     `bun:"sort_order,notnull,default:0"`
	CreatedAt         time.Time `bun:"created_at,notnull,default:current_timestamp"`
}

type CatalogStatus string

const (
	CatalogStatusDraft   CatalogStatus = "draft"
	CatalogStatusActive  CatalogStatus = "active"
	CatalogStatusHidden  CatalogStatus = "hidden"
	CatalogStatusRemoved CatalogStatus = "removed"
)
