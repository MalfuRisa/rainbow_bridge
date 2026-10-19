package service

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	redisclient "github.com/redis/go-redis/v9"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/pkg/redis"
	"gorm.io/gorm"
)

type importCacheHook struct {
	calls []string
	check func()
	fail  bool
}

func (h *importCacheHook) DialHook(next redisclient.DialHook) redisclient.DialHook {
	return func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("no network allowed") }
}
func (h *importCacheHook) ProcessPipelineHook(next redisclient.ProcessPipelineHook) redisclient.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redisclient.Cmder) error {
		for _, cmd := range cmds {
			if err := h.ProcessHook(nil)(ctx, cmd); err != nil {
				return err
			}
		}
		return nil
	}
}
func (h *importCacheHook) ProcessHook(next redisclient.ProcessHook) redisclient.ProcessHook {
	return func(ctx context.Context, cmd redisclient.Cmder) error {
		if h.check != nil {
			h.check()
		}
		args := make([]string, 0, len(cmd.Args()))
		for _, arg := range cmd.Args() {
			if v, ok := arg.(string); ok {
				args = append(args, v)
			}
		}
		h.calls = append(h.calls, strings.Join(args, " "))
		if h.fail {
			return errors.New("injected cache failure")
		}
		switch c := cmd.(type) {
		case *redisclient.IntCmd:
			c.SetVal(1)
		case *redisclient.StringSliceCmd:
			c.SetVal([]string{"matched-list-key"})
		}
		return nil
	}
}

func TestImportCacheInvalidationAfterCommit(t *testing.T) {
	s := testService(t)
	old := seedConfigs(t, s, textConfig("a", "old"))[0]
	hook := &importCacheHook{}
	client := redisclient.NewClient(&redisclient.Options{Addr: "unused"})
	client.AddHook(hook)
	defer client.Close()
	s.logic.redisClient = client
	input := textConfig("a", "new")
	hook.check = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		var got model.Config
		if err := s.logic.db.WithContext(ctx).First(&got, old.ID).Error; err != nil || got.Content != "new" {
			t.Fatalf("cache invalidated before commit: %+v %v", got, err)
		}
	}
	if err := s.logic.ImportConfigs(context.Background(), []model.Config{input}, true); err != nil {
		t.Fatal(err)
	}
	expected := []string{"del " + redis.GenerateConfigKey("dev", "main", old.ResourceKey), "del rainbow_bridge:config:key:" + old.ResourceKey, "keys rainbow_bridge:config:list:dev:main:*", "del matched-list-key", "del " + redis.GenerateConfigMapKey("dev", "main")}
	if len(hook.calls) != len(expected) {
		t.Fatalf("calls=%v", hook.calls)
	}
	for i := range expected {
		if hook.calls[i] != expected[i] {
			t.Fatalf("calls=%v", hook.calls)
		}
	}
	hook.calls = nil
	bad := textConfig("bad", "null")
	bad.Type = "object"
	if err := s.logic.ImportConfigs(context.Background(), []model.Config{input, bad}, true); err == nil {
		t.Fatal("expected invalid input")
	}
	if len(hook.calls) != 0 {
		t.Fatal("invalid batch evicted cache")
	}
	s.logic.db.Callback().Update().Before("gorm:update").Register("test:cache_failure", func(tx *gorm.DB) { tx.AddError(errors.New("injected write failure")) })
	if err := s.logic.ImportConfigs(context.Background(), []model.Config{input}, true); err == nil {
		t.Fatal("expected write failure")
	}
	if len(hook.calls) != 0 {
		t.Fatal("rollback evicted cache")
	}
	s.logic.db.Callback().Update().Remove("test:cache_failure")
	hook.fail = true
	if err := s.logic.ImportConfigs(context.Background(), []model.Config{input}, true); err != nil {
		t.Fatalf("committed DB success became an error: %v", err)
	}
}

func TestConfigCacheScopeUsesLiteralKeys(t *testing.T) {
	s := testService(t)
	hook := &importCacheHook{}
	client := redisclient.NewClient(&redisclient.Options{Addr: "unused"})
	client.AddHook(hook)
	defer client.Close()
	s.logic.redisClient = client
	cfg := textConfig("a", "value")
	cfg.EnvironmentKey = "dev*"
	cfg.PipelineKey = "main?[x]"
	if err := s.logic.ImportConfigs(context.Background(), []model.Config{cfg}, true); err != nil {
		t.Fatal(err)
	}
	pattern := `keys rainbow_bridge:config:list:dev\*:main\?\[x\]:*`
	found := false
	for _, call := range hook.calls {
		if strings.HasPrefix(call, "keys ") {
			if call != pattern {
				t.Fatalf("scope became a wildcard: %s", call)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("missing scoped list invalidation")
	}
}
