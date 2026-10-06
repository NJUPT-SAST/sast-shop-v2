package search

import (
	"strings"
	"testing"
)

func TestNormalizeKeyword(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", " \t\n", " water ", "　矿泉水　", " %_\\' "} {
		normalized, err := NormalizeKeyword(value)
		if err != nil || normalized != strings.TrimSpace(value) {
			t.Errorf("NormalizeKeyword(%q) = %q, %v", value, normalized, err)
		}
	}
	boundary := strings.Repeat("水", 200)
	if normalized, err := NormalizeKeyword("　" + boundary + "　"); err != nil || normalized != boundary {
		t.Fatalf("200 Unicode characters should be accepted: %v", err)
	}
	if _, err := NormalizeKeyword(boundary + "水"); err == nil {
		t.Fatal("201 Unicode characters should be rejected")
	}
}
