package service

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"testing"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/biz/model/runtime"
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
	s := testService(t)
	object := textConfig("a", `{"nested":[1,true]}`)
	object.Type = "object"
	object.Description = "description"
	empty := textConfig("b", "false")
	empty.Type = "boolean"
	data, err := s.writeRuntimeConfigArchive(context.Background(), &runtime.RuntimeConfigData{Configs: configSliceToPB([]model.Config{object, empty})})
	if err != nil {
		t.Fatal(err)
	}
	meta := archiveJSON(t, data, "zip", "config.json")
	configs := meta["data"].(map[string]any)["configs"].([]any)
	if configs[0].(map[string]any)["description"] != "description" {
		t.Fatal("static description missing")
	}
	if _, ok := configs[0].(map[string]any)["content"].(map[string]any); !ok {
		t.Fatal("static object shape changed")
	}
	if value, ok := configs[1].(map[string]any)["description"]; !ok || value != "" {
		t.Fatal("static empty description missing")
	}
	if configs[1].(map[string]any)["content"] != "false" {
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
