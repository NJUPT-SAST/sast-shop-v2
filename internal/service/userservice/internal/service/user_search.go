package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/userservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/userservice/internal/repository"
)

type userSearchCursor struct {
	Query string `json:"q"`
	After int64  `json:"a"`
}

func SearchUsers(
	ctx context.Context,
	query string,
	pageSize int32,
	pageToken string,
) ([]*model.UserAccount, string, error) {
	query = strings.TrimSpace(query)
	if !utf8.ValidString(query) || strings.ContainsRune(query, '\x00') || utf8.RuneCountInString(query) == 0 ||
		utf8.RuneCountInString(query) > 100 ||
		pageSize < 0 ||
		pageSize > 20 {
		return nil, "", connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user search"))
	}
	if pageSize == 0 {
		pageSize = 20
	}
	secret := os.Getenv("WEST_POCKET_INTERNAL_TOKEN")
	if secret == "" {
		return nil, "", connect.NewError(connect.CodeUnavailable, errors.New("directory search is not configured"))
	}
	afterID, err := decodeUserSearchCursor(pageToken, query, secret)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeInvalidArgument, err)
	}
	users, err := repository.SearchActiveUsers(ctx, query, afterID, int(pageSize)+1)
	if err != nil {
		return nil, "", err
	}
	var next string
	if len(users) > int(pageSize) {
		users = users[:pageSize]
		next, err = encodeUserSearchCursor(userSearchCursor{Query: query, After: users[len(users)-1].ID}, secret)
	}
	return users, next, err
}

func encodeUserSearchCursor(cursor userSearchCursor, secret string) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(data)
	return base64.RawURLEncoding.EncodeToString(data) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func decodeUserSearchCursor(token, query, secret string) (int64, error) {
	if token == "" {
		return 0, nil
	}
	invalid := errors.New("invalid search page token")
	if len(token) > 2048 {
		return 0, invalid
	}
	dataPart, signaturePart, ok := strings.Cut(token, ".")
	if !ok {
		return 0, invalid
	}
	data, err := base64.RawURLEncoding.DecodeString(dataPart)
	if err != nil {
		return 0, invalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(signaturePart)
	if err != nil {
		return 0, invalid
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(data)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return 0, invalid
	}
	var cursor userSearchCursor
	if json.Unmarshal(data, &cursor) != nil || cursor.Query != query || cursor.After <= 0 {
		return 0, invalid
	}
	return cursor.After, nil
}
