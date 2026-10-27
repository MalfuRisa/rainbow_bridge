package service

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"os"
	"reflect"
	"testing"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/biz/model/transfer"
)

func archiveJSON(t *testing.T, data []byte, format, name string) map[string]any {
	t.Helper()
	var payload []byte
	if format == "zip" {
		reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range reader.File {
			if f.Name == name {
				r, err := f.Open()
				if err != nil {
					t.Fatal(err)
				}
				payload, err = io.ReadAll(r)
				r.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
		}
	} else {
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		defer gz.Close()
		reader := tar.NewReader(gz)
		for {
			h, err := reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if h.Name == name {
				payload, err = io.ReadAll(reader)
				if err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if payload == nil {
		t.Fatalf("missing %s", name)
	}
	var result map[string]any
	if err := json.Unmarshal(payload, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestArchiveDescriptionAndContentRoundTrip(t *testing.T) {
	for _, format := range []string{"zip", "tar.gz"} {
		t.Run(format, func(t *testing.T) {
			source := testService(t)
			ctx := context.Background()
			object := textConfig("object", `{"items":[true,3,"v"]}`)
			object.Type = "object"
			object.Description = "object description"
			empty := textConfig("empty", "text")
			empty.Description = ""
			configs := seedConfigs(t, source, object, empty)
			var data []byte
			var err error
			if format == "zip" {
				data, err = source.writeConfigArchive(ctx, configs)
			} else {
				data, err = source.writeConfigTarGz(ctx, configs)
			}
			if err != nil {
				t.Fatal(err)
			}
			meta := archiveJSON(t, data, format, "configs.json")
			entries := meta["business_configs"].([]any)
			if entries[0].(map[string]any)["description"] != "object description" {
				t.Fatal("description missing")
			}
			if value, ok := entries[1].(map[string]any)["description"]; !ok || value != "" {
				t.Fatal("empty description omitted")
			}
			if _, ok := entries[0].(map[string]any)["content"].(map[string]any); !ok {
				t.Fatal("object content shape changed")
			}
			if entries[1].(map[string]any)["content"] != "text" {
				t.Fatal("text content shape changed")
			}
			target := testService(t)
			seedConfigs(t, target, textConfig("untouched", "keep"))
			selections := []*transfer.ExportSelection{{EnvironmentKey: "dev", PipelineKey: "main"}}
			written, err := target.ImportConfigsSelective(ctx, data, "configs."+format, selections, true)
			if err != nil || len(written) != 2 {
				t.Fatalf("written=%+v err=%v", written, err)
			}
			if len(allConfigRows(t, target)) != 3 {
				t.Fatal("unselected data lost")
			}
			if written[0].Description != object.Description || written[0].Content != object.Content || written[1].Description != "" {
				t.Fatalf("roundtrip changed fields: %+v", written)
			}
			before := allConfigRows(t, target)
			skipped, err := target.ImportConfigsSelective(ctx, data, "configs."+format, selections, false)
			if err != nil || len(skipped) != 0 || !reflect.DeepEqual(before, allConfigRows(t, target)) {
				t.Fatalf("skip failed: %+v %v", skipped, err)
			}
			preview, err := target.ImportConfigsPreview(ctx, data, "configs."+format)
			if err != nil || preview.Summary.TotalConfigs != 2 {
				t.Fatalf("preview=%+v err=%v", preview, err)
			}
			if format == "zip" {
				plain := testService(t)
				seedConfigs(t, plain, textConfig("untouched", "keep"))
				written, err = plain.ImportConfigsArchive(ctx, data, "", "", true)
				if err != nil || len(written) != 2 || written[0].Description != object.Description || len(allConfigRows(t, plain)) != 3 {
					t.Fatalf("ordinary ZIP roundtrip: %+v %v", written, err)
				}
				skipped, err = plain.ImportConfigsArchive(ctx, data, "", "", false)
				if err != nil || len(skipped) != 0 {
					t.Fatalf("ordinary ZIP summary: %+v %v", skipped, err)
				}
			}
		})
	}
}

func TestStaticArchiveDescriptionAndContent(t *testing.T) {
	// 旧编辑区构包器已移除:快照静态包必须同样保留描述、空值与布尔字符串形态
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "qa_prod", "main")
	ctx := adminTestContext()
	object := buildConfig("qa_prod", "main", "REL_OBJ", "object", `{"nested":[1,true]}`)
	object.Description = "description"
	if _, err := svc.AddConfig(ctx, object); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddConfig(ctx, buildConfig("qa_prod", "main", "REL_BOOL", "boolean", "false")); err != nil {
		t.Fatal(err)
	}
	mustLock(t, svc, "qa_prod", "main", "静态包描述与空值")
	created, err := svc.CreateRelease(ctx, ReleaseCreateInput{
		EnvironmentKey: "qa_prod", PipelineKey: "main", Version: "v1.0",
		ExpectedFreezeRevisionRaw: "1", RequestID: uuid.NewString(), Reason: "静态包验收",
	})
	if err != nil {
		t.Fatal(err)
	}
	exported, err := svc.ReleaseStaticExport(ctx, ReleaseStaticExportInput{
		EnvironmentKey: "qa_prod", PipelineKey: "main", Version: "v1.0",
		ExpectedSnapshotID: created.Meta.SnapshotID,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(exported.TempPath)
	data, err := os.ReadFile(exported.TempPath)
	if err != nil {
		t.Fatal(err)
	}
	meta := archiveJSON(t, data, "zip", "config.json")
	configs := meta["data"].(map[string]any)["configs"].([]any)
	titles := map[string]map[string]any{}
	for _, item := range configs {
		entry := item.(map[string]any)
		titles[entry["alias"].(string)] = entry
	}
	if titles["REL_OBJ"]["description"] != "description" {
		t.Fatal("static description missing")
	}
	if _, ok := titles["REL_OBJ"]["content"].(map[string]any); !ok {
		t.Fatal("static object shape changed")
	}
	if value, ok := titles["REL_BOOL"]["description"]; !ok || value != "" {
		t.Fatal("static empty description missing")
	}
	if titles["REL_BOOL"]["content"] != "false" {
		t.Fatal("boolean must remain string")
	}
}

func TestArchiveInvalidBatchPreservesConfigs(t *testing.T) {
	for _, format := range []string{"zip", "tar.gz"} {
		t.Run(format, func(t *testing.T) {
			s := testService(t)
			bad := textConfig("bad", "null")
			bad.Type = "object"
			configs := []model.Config{textConfig("good", "good"), bad}
			var data []byte
			var err error
			if format == "zip" {
				data, err = s.writeConfigArchive(context.Background(), configs)
			} else {
				data, err = s.writeConfigTarGz(context.Background(), configs)
			}
			if err != nil {
				t.Fatal(err)
			}
			old := seedConfigs(t, s, textConfig("old", "old"))
			if _, err = s.ImportConfigsSelective(context.Background(), data, "configs."+format, nil, true); err == nil {
				t.Fatal("invalid archive accepted")
			}
			if !reflect.DeepEqual(old, allConfigRows(t, s)) {
				t.Fatal("invalid selective archive changed config data")
			}
			if format == "zip" {
				if _, err = s.ImportConfigsArchive(context.Background(), data, "", "", true); err == nil {
					t.Fatal("invalid plain archive accepted")
				}
				if !reflect.DeepEqual(old, allConfigRows(t, s)) {
					t.Fatal("invalid plain archive changed config data")
				}
			}
		})
	}
}
