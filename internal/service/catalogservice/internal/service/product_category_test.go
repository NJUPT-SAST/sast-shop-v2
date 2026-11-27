package service

import (
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/catalogservice/internal/model"
)

func TestNormalizeProductCategoryNameTrimsAndCountsUnicodeRunes(t *testing.T) {
	name, err := normalizeProductCategoryName("  生活用品  ")
	if err != nil {
		t.Fatalf("normalizeProductCategoryName returned error: %v", err)
	}
	if name != "生活用品" {
		t.Fatalf("normalized name = %q, want %q", name, "生活用品")
	}

	name, err = normalizeProductCategoryName("   ")
	if err == nil || name != "" {
		t.Fatalf("blank category name = %q, %v, want empty name and validation error", name, err)
	}
}

func TestValidateTemplateHasCategoryRequiresUncategorizedEditsToSelectCategory(t *testing.T) {
	err := validateTemplateHasCategory(&model.CatalogProductTemplate{}, map[string]any{"title": "New title"})
	if err == nil {
		t.Fatal("expected an ordinary edit on an uncategorized template to require category selection")
	}
}

func TestValidateTemplateStoreMatchesLockedStoreRejectsStaleSourceStore(t *testing.T) {
	err := validateTemplateStoreMatchesLockedStore(10, 20)
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("stale source store error = %v, want aborted", err)
	}
}

func TestNormalizeProductCategoryNameRejectsMoreThanFiftyUnicodeRunes(t *testing.T) {
	name := "中" + strings.Repeat("a", 50)
	_, err := normalizeProductCategoryName(name)
	if err == nil {
		t.Fatal("expected category name longer than 50 Unicode characters to be rejected")
	}
}
