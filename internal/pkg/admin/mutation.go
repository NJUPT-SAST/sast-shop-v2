package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/uptrace/bun"
)

type Entry struct {
	Domain     string
	ActorID    int64
	Action     string
	TargetType string
	TargetID   int64
	RequestID  string
	Reason     string
	Evidence   string
	Before     any
	After      any
}

type Mutation struct {
	Domain    string
	ActorID   int64
	Action    string
	RequestID string
	Input     any
}

type MutationResult struct {
	Replay   bool
	Response json.RawMessage
}

func ValidateReason(reason, evidence string) error {
	if !validText(reason, 1000) || !validText(evidence, 4000) {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("请填写有效的操作原因和核实依据，核实依据不超过4000字"))
	}
	return nil
}

func validText(value string, limit int) bool {
	return strings.TrimSpace(value) != "" && utf8.ValidString(value) && !strings.ContainsRune(value, '\x00') &&
		utf8.RuneCountInString(value) <= limit
}

func table(domain, name string) (bun.Ident, error) {
	switch domain {
	case "user", "catalog", "spot", "errand", "payment", "westpocket":
		return bun.Ident(domain + "." + name), nil
	default:
		return "", errors.New("invalid administration domain")
	}
}

func WriteAudit(ctx context.Context, db bun.IDB, entry Entry) error {
	target, err := table(entry.Domain, "admin_action_log")
	if err != nil {
		return err
	}
	if err = ValidateReason(entry.Reason, entry.Evidence); err != nil {
		return err
	}
	if entry.ActorID <= 0 || entry.TargetID <= 0 || !validText(entry.Action, 100) ||
		!validText(entry.TargetType, 100) ||
		!validText(entry.RequestID, 128) {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid audit entry"))
	}
	before, err := json.Marshal(entry.Before)
	if err != nil {
		return err
	}
	after, err := json.Marshal(entry.After)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(
		ctx,
		`INSERT INTO ? (actor_id, action, target_type, target_id, request_id, reason, evidence, before_state, after_state)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?::jsonb, ?::jsonb)`,
		target,
		entry.ActorID,
		entry.Action,
		entry.TargetType,
		entry.TargetID,
		entry.RequestID,
		strings.TrimSpace(entry.Reason),
		strings.TrimSpace(entry.Evidence),
		string(before),
		string(after),
	)
	return err
}

// BeginMutation and CompleteMutation belong to the same business transaction.
func BeginMutation(ctx context.Context, tx bun.Tx, mutation Mutation) (*MutationResult, error) {
	target, err := table(mutation.Domain, "admin_request_dedup")
	if err != nil {
		return nil, err
	}
	if mutation.ActorID <= 0 || !validText(mutation.Action, 100) || !validText(mutation.RequestID, 128) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid administration request"))
	}
	input, err := json.Marshal(mutation.Input)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(input)
	hash := hex.EncodeToString(digest[:])
	result, err := tx.ExecContext(ctx, `INSERT INTO ? (actor_id, action, request_id, input_hash)
		VALUES (?, ?, ?, ?) ON CONFLICT (actor_id, action, request_id) DO NOTHING`, target,
		mutation.ActorID, mutation.Action, mutation.RequestID, hash)
	if err != nil {
		return nil, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	var stored struct {
		InputHash string          `bun:"input_hash"`
		Response  json.RawMessage `bun:"response"`
		Completed bool            `bun:"completed"`
	}
	err = tx.NewRaw(`SELECT input_hash, response, completed FROM ? WHERE actor_id=? AND action=? AND request_id=? FOR UPDATE`, target,
		mutation.ActorID, mutation.Action, mutation.RequestID).
		Scan(ctx, &stored)
	if err != nil {
		return nil, err
	}
	if stored.InputHash != hash {
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("请求编号已用于不同操作内容"))
	}
	if inserted == 0 && !stored.Completed {
		return nil, connect.NewError(connect.CodeAborted, errors.New("操作尚未完成"))
	}
	return &MutationResult{Replay: stored.Completed, Response: stored.Response}, nil
}

func CompleteMutation(ctx context.Context, tx bun.Tx, mutation Mutation, response any) error {
	target, err := table(mutation.Domain, "admin_request_dedup")
	if err != nil {
		return err
	}
	data, err := json.Marshal(response)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(
		ctx,
		`UPDATE ? SET response=?::jsonb, completed=true WHERE actor_id=? AND action=? AND request_id=? AND NOT completed`,
		target,
		string(data),
		mutation.ActorID,
		mutation.Action,
		mutation.RequestID,
	)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("administration request was not started")
	}
	return nil
}
