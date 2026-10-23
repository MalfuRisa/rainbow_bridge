package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/biz/model/common"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testService(t *testing.T) *Service {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := conn.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(2)
	// Keep the isolated in-memory database alive when cancellation discards a connection.
	keeper, err := sqlDB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { keeper.Close(); sqlDB.Close() })
	if err := conn.AutoMigrate(
		&model.Config{}, &model.Asset{}, &model.Environment{}, &model.Pipeline{},
		&model.ConfigFreezeState{}, &model.ConfigFreezeEvent{}, &model.ConfigWriteGate{},
	); err != nil {
		t.Fatal(err)
	}
	svc := NewService(conn, nil, "/rainbow-bridge", nil)
	// 冻结基础结构随夹具就绪:写协调行 + 存量渠道状态行
	if err := svc.EnsureFreezeBootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	return svc
}

func allConfigRows(t *testing.T, s *Service) []model.Config {
	t.Helper()
	var rows []model.Config
	if err := s.logic.db.Order("id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

func seedConfigs(t *testing.T, s *Service, configs ...model.Config) []model.Config {
	t.Helper()
	for i := range configs {
		if err := s.logic.AddConfig(context.Background(), &configs[i]); err != nil {
			t.Fatal(err)
		}
	}
	return allConfigRows(t, s)
}

func TestImportConflictPolicyAndScope(t *testing.T) {
	for _, overwrite := range []bool{false, true} {
		t.Run(map[bool]string{false: "skip", true: "update"}[overwrite], func(t *testing.T) {
			s := testService(t)
			otherPipe := textConfig("a", "other pipeline")
			otherPipe.PipelineKey = "other"
			prod := textConfig("a", "prod")
			prod.EnvironmentKey = "prod"
			old := seedConfigs(t, s, textConfig("a", "old"), textConfig("untouched", "keep"), otherPipe, prod)
			input := textConfig("a", "updated")
			input.ResourceKey = "foreign-source-id"
			for attempt := 0; attempt < 2; attempt++ {
				written, err := s.ImportConfigsWithResult(context.Background(), []*common.ResourceConfig{modelConfigToPB(&input)}, overwrite)
				if err != nil {
					t.Fatal(err)
				}
				expected := 0
				if overwrite {
					expected = 1
				}
				if len(written) != expected {
					t.Fatalf("written=%+v", written)
				}
				rows := allConfigRows(t, s)
				if len(rows) != 4 {
					t.Fatalf("rows=%+v", rows)
				}
				value := "old"
				if overwrite {
					value = "updated"
				}
				if rows[0].Content != value || rows[0].ID != old[0].ID || rows[0].ResourceKey != old[0].ResourceKey || rows[0].Alias != "a" {
					t.Fatalf("identity/value changed: %+v", rows[0])
				}
				if !reflect.DeepEqual(rows[1:], old[1:]) {
					t.Fatalf("unrelated scopes changed: %+v", rows)
				}
			}
		})
	}
}

func TestImportIdentityAmbiguity(t *testing.T) {
	for _, alias := range []string{"b", "unknown"} {
		t.Run(alias, func(t *testing.T) {
			s := testService(t)
			old := seedConfigs(t, s, textConfig("a", "old-a"), textConfig("b", "old-b"))
			bad := textConfig(alias, "changed")
			bad.ResourceKey = old[0].ResourceKey
			if err := s.logic.ImportConfigs(context.Background(), []model.Config{textConfig("new", "new"), bad}, true); err == nil {
				t.Fatal("expected ambiguity")
			}
			if got := allConfigRows(t, s); !reflect.DeepEqual(got, old) {
				t.Fatalf("changed rows: %+v", got)
			}
		})
	}
	t.Run("empty alias retains identity", func(t *testing.T) {
		s := testService(t)
		old := seedConfigs(t, s, textConfig("a", "old"))
		cfg := textConfig("", "changed")
		cfg.ResourceKey = old[0].ResourceKey
		if err := s.logic.ImportConfigs(context.Background(), []model.Config{cfg}, true); err != nil {
			t.Fatal(err)
		}
		got := allConfigRows(t, s)
		if len(got) != 1 || got[0].Alias != "a" || got[0].ResourceKey != old[0].ResourceKey {
			t.Fatalf("changed identity: %+v", got)
		}
	})
}

func TestImportDuplicateFirstWins(t *testing.T) {
	for _, by := range []string{"alias", "resource_key"} {
		t.Run(by, func(t *testing.T) {
			s := testService(t)
			first := textConfig("a", "first")
			first.ResourceKey = "source-id"
			second := first
			second.Content = "second"
			if by == "resource_key" {
				second.Alias = "b"
			} else {
				second.ResourceKey = "other-source"
			}
			written, err := s.logic.importConfigs(context.Background(), []model.Config{first, second}, true)
			if err != nil || len(written) != 1 {
				t.Fatalf("written=%+v err=%v", written, err)
			}
			got := allConfigRows(t, s)
			if len(got) != 1 || got[0].Content != "first" {
				t.Fatalf("rows=%+v", got)
			}
			second.Type = "object"
			second.Content = "null"
			before := allConfigRows(t, s)
			if err := s.logic.ImportConfigs(context.Background(), []model.Config{first, second}, true); err == nil {
				t.Fatal("invalid duplicate accepted")
			}
			if !reflect.DeepEqual(before, allConfigRows(t, s)) {
				t.Fatal("invalid duplicate changed database")
			}
		})
	}
}

func TestImportRejectsEmptyInput(t *testing.T) {
	cases := [][]*common.ResourceConfig{nil, {}, {nil}, {{Alias: "a", PipelineKey: "main", Type: "text", Content: "v"}}, {{Alias: "a", EnvironmentKey: "dev", Type: "text", Content: "v"}}, {{EnvironmentKey: "dev", PipelineKey: "main", Type: "text", Content: "v"}}}
	for i, input := range cases {
		s := testService(t)
		old := seedConfigs(t, s, textConfig("old", "old"))
		if err := s.ImportConfigs(context.Background(), input, true); err == nil {
			t.Fatalf("case %d accepted", i)
		}
		if !reflect.DeepEqual(old, allConfigRows(t, s)) {
			t.Fatalf("case %d changed data", i)
		}
	}
}

func TestImportTransactionRollback(t *testing.T) {
	for _, mode := range []string{"create", "update", "query", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			s := testService(t)
			old := seedConfigs(t, s, textConfig("a", "old"), textConfig("keep", "old"))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fail := errors.New("injected database failure")
			calls := 0
			callback := func(tx *gorm.DB) {
				if tx.Statement.Schema == nil || tx.Statement.Schema.Table != "resource_config" {
					return
				}
				calls++
				if calls == 2 {
					if mode == "cancel" {
						cancel()
					} else {
						tx.AddError(fail)
					}
				}
			}
			input := []model.Config{textConfig("new-1", "one"), textConfig("new-2", "two")}
			switch mode {
			case "update":
				input[0] = textConfig("a", "changed")
				input[1] = textConfig("keep", "changed")
				s.logic.db.Callback().Update().Before("gorm:update").Register("test:failure", callback)
			case "query":
				s.logic.db.Callback().Query().Before("gorm:query").Register("test:failure", callback)
			default:
				s.logic.db.Callback().Create().Before("gorm:create").Register("test:failure", callback)
			}
			if err := s.logic.ImportConfigs(ctx, input, true); err == nil {
				t.Fatal("expected rollback")
			}
			if got := allConfigRows(t, s); !reflect.DeepEqual(got, old) {
				t.Fatalf("not rolled back: %+v", got)
			}
		})
	}
	t.Run("already cancelled", func(t *testing.T) {
		s := testService(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := s.logic.ImportConfigs(ctx, []model.Config{textConfig("a", "v")}, true); !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v", err)
		}
		if len(allConfigRows(t, s)) != 0 {
			t.Fatal("cancelled import wrote")
		}
	})
}

func TestInvalidScalarTypesHaveNoWrites(t *testing.T) {
	for typ, values := range map[string][]string{
		"number": {"1.2", "1e3", "", "+1"}, "decimal": {".5", "1.", "NaN", "Infinity", "1e3", ""}, "boolean": {"TRUE", "0", ""},
		"object": {"null", "[]", "42", "true"}, "keyvalue": {"null", "[]", "\"s\""},
	} {
		for _, value := range values {
			t.Run(typ+value, func(t *testing.T) {
				s := testService(t)
				cfg := textConfig("a", value)
				cfg.Type = typ
				if err := s.logic.AddConfig(context.Background(), &cfg); err == nil {
					t.Fatal("accepted invalid content")
				}
				if len(allConfigRows(t, s)) != 0 {
					t.Fatal("invalid create persisted")
				}
			})
		}
	}
	s := testService(t)
	cfg := textConfig("huge", strings.Repeat("9", 100))
	cfg.Type = "number"
	if err := s.logic.AddConfig(context.Background(), &cfg); err != nil {
		t.Fatal("integer has an unwanted range limit:", err)
	}
}

func TestNestedAssetReferencesAndBasePath(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	asset := model.Asset{FileID: "asset-id", FileName: "icon.png"}
	if err := s.logic.db.Create(&asset).Error; err != nil {
		t.Fatal(err)
	}
	cfg := textConfig("nested", `{"nested":{"items":["asset://asset-id","plain",3,true]},"existing":"/rainbow-bridge/api/v1/asset/file/asset-id/icon.png"}`)
	cfg.Type = "object"
	if err := s.logic.AddConfig(ctx, &cfg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg.Content, `"/api/v1/asset/file/asset-id/icon.png"`) {
		t.Fatalf("reference unresolved: %s", cfg.Content)
	}
	decorated := s.decorateConfig(modelConfigToPB(&cfg))
	if strings.Contains(decorated.Content, "/rainbow-bridge/rainbow-bridge") || strings.Count(decorated.Content, "/rainbow-bridge/api/v1/asset/file/asset-id/icon.png") != 2 {
		t.Fatalf("invalid prefix: %s", decorated.Content)
	}
	if !strings.Contains(decorated.Content, `"plain",3,true`) {
		t.Fatalf("ordinary values changed: %s", decorated.Content)
	}
}

func TestExistingContentRules(t *testing.T) {
	for _, tc := range []struct {
		typ, content, expected string
		valid                  bool
	}{
		{"text", "hello", "hello", true}, {"textarea", "line", "line", true}, {"richtext", "<p>text</p>", "<p>text</p>", true},
		{"text", "", "", false}, {"textarea", "", "", false}, {"richtext", "", "", false},
		{"color", "#abc", "#AABBCC", true}, {"color", "bad", "", false},
		{"image", "asset://missing", "/api/v1/asset/file/missing", true}, {"file", "https://example.com/a", "https://example.com/a", true},
		{"image", "relative.png", "", false}, {"file", "", "", false},
		{"unknown", `{"a":1}`, `{"a":1}`, true}, {"kv", `{"a":1}`, `{"a":1}`, true}, {"unknown", "scalar", "", false},
	} {
		t.Run(tc.typ+tc.content, func(t *testing.T) {
			s := testService(t)
			cfg := textConfig("a", tc.content)
			cfg.Type = tc.typ
			err := s.logic.AddConfig(context.Background(), &cfg)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t err=%v", tc.valid, err)
			}
			if tc.valid && cfg.Content != tc.expected {
				t.Fatalf("content=%q expected=%q", cfg.Content, tc.expected)
			}
		})
	}
}

func textConfig(alias, value string) model.Config {
	return model.Config{EnvironmentKey: "dev", PipelineKey: "main", Alias: alias, Type: "text", Content: value}
}

func TestInvalidImportPreservesAllScopes(t *testing.T) {
	for _, overwrite := range []bool{false, true} {
		t.Run(map[bool]string{false: "merge", true: "overwrite"}[overwrite], func(t *testing.T) {
			s := testService(t)
			ctx := context.Background()
			old := textConfig("old", "old")
			other := textConfig("other", "prod")
			other.EnvironmentKey = "prod"
			for _, c := range []*model.Config{&old, &other} {
				if err := s.logic.AddConfig(ctx, c); err != nil {
					t.Fatal(err)
				}
			}
			bad := textConfig("bad", "{")
			bad.Type = "object"
			if err := s.logic.ImportConfigs(ctx, []model.Config{textConfig("new", "new"), bad}, overwrite); err == nil {
				t.Fatal("expected invalid import")
			}
			var rows []model.Config
			s.logic.db.Order("id").Find(&rows)
			if len(rows) != 2 || rows[0].Content != "old" || rows[1].Content != "prod" {
				t.Fatalf("changed old data: %+v", rows)
			}
		})
	}
}

func TestScalarConfigTypes(t *testing.T) {
	for _, tc := range []struct{ typ, content string }{{"number", "42"}, {"number", "-42"}, {"decimal", "3.14"}, {"decimal", "3"}, {"decimal", "-0.5"}, {"boolean", "false"}, {"boolean", "true"}, {"number", " 42 "}, {"boolean", " false "}} {
		t.Run(tc.typ+tc.content, func(t *testing.T) {
			s := testService(t)
			c := textConfig("a", tc.content)
			c.Type = tc.typ
			if err := s.logic.AddConfig(context.Background(), &c); err != nil {
				t.Fatal(err)
			}
			got, err := s.logic.GetConfig(context.Background(), "dev", "main", c.ResourceKey)
			if err != nil || got.Content != strings.TrimSpace(tc.content) {
				t.Fatalf("got=%+v err=%v", got, err)
			}
		})
	}
}

func TestDescriptionCanBeCleared(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	c := textConfig("a", "content")
	c.Description = "before"
	if err := s.logic.AddConfig(ctx, &c); err != nil {
		t.Fatal(err)
	}
	for _, description := range []string{"", "after"} {
		c.Description = description
		c.Alias = "ignored"
		if err := s.logic.UpdateConfig(ctx, &c, c.EditRevision); err != nil {
			t.Fatal(err)
		}
		got, err := s.logic.GetConfig(ctx, "dev", "main", c.ResourceKey)
		if err != nil || got.Description != description || got.Alias != "a" {
			t.Fatalf("got=%+v err=%v", got, err)
		}
	}
}
