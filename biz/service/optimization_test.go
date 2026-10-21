package service

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/biz/model/common"
)

func TestMigrationIdempotentAndResponseCopy(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	if err := s.logic.db.Create(&model.Asset{FileID: "asset-1", FileName: "persist.png"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"", "/rainbow-bridge", "/rainbow-bridge/rainbow-bridge"} {
		cfg := textConfig("m"+prefix, prefix+"/api/v1/asset/file/asset-1/persist.png?x=1#f")
		cfg.Type = "image"
		cfg.ResourceKey = "key" + prefix
		if err := s.logic.db.Create(&cfg).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		if err := s.MigrateToFullAssetPaths(ctx); err != nil {
			t.Fatal(err)
		}
		for _, cfg := range allConfigRows(t, s) {
			if cfg.Content != "/api/v1/asset/file/asset-1/persist.png?x=1#f" {
				t.Fatalf("migration %d: %s", i, cfg.Content)
			}
		}
	}
	pb := modelConfigToPB(&allConfigRows(t, s)[0])
	before := pb.Content
	got := s.decorateConfig(pb)
	again := s.decorateConfig(got)
	if pb.Content != before || got.Content != again.Content || strings.Count(got.Content, "/rainbow-bridge") != 1 {
		t.Fatalf("DTO mutated or decorated twice: %v %v", got, again)
	}
}

func archiveJSONBytes(t *testing.T, data []byte, name string) []byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range reader.File {
		if file.Name == name {
			r, err := file.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			raw, err := io.ReadAll(r)
			if err != nil {
				t.Fatal(err)
			}
			return raw
		}
	}
	t.Fatalf("missing %s", name)
	return nil
}

func TestStoredPrefixesAndExternalPaths(t *testing.T) {
	s := testService(t)
	for i, value := range []string{"/rainbow-bridge/api/v1/asset/file/id/name.png?x=1#f", "/rainbow-bridge/rainbow-bridge/api/v1/asset/file/id/name.png?x=1#f", "https://example.com/rainbow-bridge/api/v1/asset/file/id/name.png?x=1#f", "/other/api/v1/asset/file/id/name.png"} {
		expected := value
		if i < 2 {
			expected = "/api/v1/asset/file/id/name.png?x=1#f"
		}
		cfg := &common.ResourceConfig{EnvironmentKey: "dev", PipelineKey: "main", Alias: value, Type: "image", Content: value}
		created, err := s.AddConfig(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		var stored model.Config
		if err := s.logic.db.Where("resource_key = ?", created.ResourceKey).First(&stored).Error; err != nil {
			t.Fatal(err)
		}
		if stored.Content != expected {
			t.Fatalf("stored=%s expected=%s", stored.Content, expected)
		}
	}
}

func TestPrecisionArchiveAndStaticRoundTrips(t *testing.T) {
	t.Chdir(t.TempDir())
	const content = `{"big":9007199254740993,"negative":-9007199254740993,"decimal":0.123456789012345678901,"exponent":1.234567890123456789e+40,"array":[9007199254740993]}`
	for _, typ := range []string{"object", "keyvalue"} {
		for _, format := range []string{"zip", "tar.gz"} {
			t.Run(typ+format, func(t *testing.T) {
				source := testService(t)
				cfg := textConfig("precision", content)
				cfg.Type = typ
				rows := seedConfigs(t, source, cfg)
				var data []byte
				var err error
				if format == "zip" {
					data, err = source.writeConfigArchive(context.Background(), rows)
				} else {
					data, err = source.writeConfigTarGz(context.Background(), rows)
				}
				if err != nil {
					t.Fatal(err)
				}
				target := testService(t)
				result, err := target.ImportConfigsSelective(context.Background(), data, "precision."+format, nil, true)
				if err != nil || len(result) != 1 {
					t.Fatalf("%+v %v", result, err)
				}
				assertNumbers := func(value string) {
					t.Helper()
					for _, token := range []string{"9007199254740993", "-9007199254740993", "0.123456789012345678901", "1.234567890123456789e+40"} {
						if !strings.Contains(value, token) {
							t.Fatalf("lost %s: %s", token, value)
						}
					}
				}
				assertNumbers(result[0].Content)
				// 静态包精度与描述断言由快照构包用例覆盖(TestReleaseStaticExportPackageContents、
				// TestStaticArchiveDescriptionAndContent);旧编辑区构包器已移除,这里只保留在线读取断言。
			})
		}
	}
}

func TestPrecisionAcrossContentBoundaries(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	content := `{"large":9007199254740993,"negative":-9007199254740993,"decimal":0.123456789012345678901,"exponent":1.234567890123456789e+40,"items":[9007199254740993,"asset://missing"]}`
	for _, typ := range []string{"object", "keyvalue"} {
		cfg := textConfig(typ, content)
		cfg.Type = typ
		if err := s.logic.AddConfig(ctx, &cfg); err != nil {
			t.Fatal(err)
		}
		for _, value := range []string{cfg.Content, s.expandConfigContent(cfg.Content, typ)} {
			for _, token := range []string{"9007199254740993", "-9007199254740993", "0.123456789012345678901", "1.234567890123456789e+40"} {
				if !strings.Contains(value, token) {
					t.Fatalf("lost %s: %s", token, value)
				}
			}
		}
		mapped := archiveConfigData([]model.Config{cfg})
		if _, ok := mapped[0]["content"].(map[string]any); !ok {
			t.Fatal("object became string")
		}
	}
	for _, bad := range []string{`{} {}`, `{} true`, `null`, `[]`} {
		cfg := textConfig("bad", bad)
		cfg.Type = "object"
		if err := s.logic.AddConfig(ctx, &cfg); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}
