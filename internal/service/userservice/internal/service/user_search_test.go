package service

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
)

func TestUserSearchCursorCannotBeChangedOrReusedForAnotherQuery(t *testing.T) {
	token, err := encodeUserSearchCursor(userSearchCursor{Query: "小明", After: 42}, "directory-secret")
	if err != nil {
		t.Fatal(err)
	}
	if after, err := decodeUserSearchCursor(token, "小明", "directory-secret"); err != nil || after != 42 {
		t.Fatalf("valid cursor: after=%d err=%v", after, err)
	}
	for _, test := range []struct{ token, query, secret string }{
		{token, "小红", "directory-secret"},
		{token, "小明", "different-secret"},
		{token + "A", "小明", "directory-secret"},
		{strings.Repeat("a", 2049), "小明", "directory-secret"},
		{"42", "小明", "directory-secret"},
	} {
		if _, err := decodeUserSearchCursor(test.token, test.query, test.secret); err == nil {
			t.Fatalf("accepted invalid cursor: %+v", test)
		}
	}
}

func TestUserSearchRejectsUnboundedInputBeforeDatabaseAccess(t *testing.T) {
	for _, test := range []struct {
		query string
		size  int32
	}{
		{"", 20}, {"  ", 20}, {"a", 21}, {"a", -1}, {strings.Repeat("界", 101), 20}, {string([]byte{0xff}), 20}, {"a\x00", 20},
	} {
		_, _, err := SearchUsers(context.Background(), test.query, test.size, "")
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("query=%q size=%d: %v", test.query, test.size, err)
		}
	}
}
