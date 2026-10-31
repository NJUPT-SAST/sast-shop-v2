package search

import (
	"errors"
	"strings"
	"unicode/utf8"
)

func NormalizeKeyword(value string) (string, error) {
	keyword := strings.TrimSpace(value)
	if utf8.RuneCountInString(keyword) > 200 {
		return "", errors.New("搜索关键词不能超过 200 个字符")
	}
	return keyword, nil
}
