package redis

import (
	"context"
	"reflect"
	"testing"

	goredis "github.com/redis/go-redis/v9"
)

func TestPrefixHook(t *testing.T) {
	tests := []struct {
		name        string
		projectOnly bool
		args        []any
		want        []any
	}{
		{"get", false, []any{"get", "session:token"}, []any{"get", "shop:catalog:session:token"}},
		{"shared session", true, []any{"get", "session:token"}, []any{"get", "shop:session:token"}},
		{
			"eval",
			false,
			[]any{"eval", "return redis.call('GET', KEYS[1])", 2, "quota", "limit", "argument"},
			[]any{
				"eval",
				"return redis.call('GET', KEYS[1])",
				2,
				"shop:catalog:quota",
				"shop:catalog:limit",
				"argument",
			},
		},
		{
			"evalsha",
			false,
			[]any{"evalsha", "script-hash", 1, "limit", 60, 10},
			[]any{"evalsha", "script-hash", 1, "shop:catalog:limit", 60, 10},
		},
		{
			"script with no keys",
			false,
			[]any{"eval", "return ARGV[1]", 0, "argument"},
			[]any{"eval", "return ARGV[1]", 0, "argument"},
		},
		{
			"hello",
			false,
			[]any{"hello", 3, "auth", "default", "secret"},
			[]any{"hello", 3, "auth", "default", "secret"},
		},
		{"auth", false, []any{"auth", "secret"}, []any{"auth", "secret"}},
		{
			"client",
			false,
			[]any{"client", "setinfo", "LIB-NAME", "go-redis"},
			[]any{"client", "setinfo", "LIB-NAME", "go-redis"},
		},
		{"script load", false, []any{"script", "load", "return 1"}, []any{"script", "load", "return 1"}},
		{"multiple keys", false, []any{"del", "one", "two"}, []any{"del", "shop:catalog:one", "shop:catalog:two"}},
		{
			"multiple values",
			false,
			[]any{"mset", "one", "value1", "two", "value2"},
			[]any{"mset", "shop:catalog:one", "value1", "shop:catalog:two", "value2"},
		},
		{"similar prefix", false, []any{"get", "shop:catalogue:key"}, []any{"get", "shop:catalog:shop:catalogue:key"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.projectOnly {
				ctx = WithProjectPrefixOnly(ctx)
			}
			hook := &prefixHook{projectPrefix: "shop", servicePrefix: "catalog"}
			cmd := goredis.NewCmd(ctx, tt.args...)
			process := hook.ProcessHook(func(_ context.Context, cmd goredis.Cmder) error {
				if !reflect.DeepEqual(cmd.Args(), tt.want) {
					t.Fatalf("args = %#v, want %#v", cmd.Args(), tt.want)
				}
				return nil
			})
			// Reusing a command must not duplicate the namespace prefix.
			for range 2 {
				if err := process(ctx, cmd); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestPrefixHookPipeline(t *testing.T) {
	ctx := WithProjectPrefixOnly(context.Background())
	cmds := []goredis.Cmder{
		goredis.NewCmd(ctx, "set", "session:token", "value"),
		goredis.NewCmd(ctx, "evalsha", "script-hash", 1, "quota", 10),
	}
	want := [][]any{{"set", "shop:session:token", "value"}, {"evalsha", "script-hash", 1, "shop:quota", 10}}
	hook := &prefixHook{projectPrefix: "shop", servicePrefix: "catalog"}
	process := hook.ProcessPipelineHook(func(_ context.Context, cmds []goredis.Cmder) error {
		for i, cmd := range cmds {
			if !reflect.DeepEqual(cmd.Args(), want[i]) {
				t.Fatalf("command %d = %#v, want %#v", i, cmd.Args(), want[i])
			}
		}
		return nil
	})
	for range 2 {
		if err := process(ctx, cmds); err != nil {
			t.Fatal(err)
		}
	}
}
