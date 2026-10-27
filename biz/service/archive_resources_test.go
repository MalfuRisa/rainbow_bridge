package service

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
)

func resourcePackage(t *testing.T, format, entry string, includeFile, includeMetadata bool) []byte {
	t.Helper()
	meta := []model.Asset{}
	if includeMetadata {
		meta = append(meta, model.Asset{FileID: "resource-id", FileName: "中文 空格.png", EnvironmentKey: "dev", PipelineKey: "main", ContentType: "image/png"})
	}
	metadata := map[string]any{"business_configs": []map[string]any{{"alias": "LOGO", "environment_key": "dev", "pipeline_key": "main", "type": "image", "content": "asset://resource-id"}}, "assets": meta}
	raw, _ := json.Marshal(metadata)
	var output bytes.Buffer
	if format == "zip" {
		writer := zip.NewWriter(&output)
		file, err := writer.Create("configs.json")
		if err != nil {
			t.Fatal(err)
		}
		file.Write(raw)
		if includeFile {
			file, err = writer.Create(entry)
			if err != nil {
				t.Fatal(err)
			}
			file.Write([]byte("image bytes"))
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		gz := gzip.NewWriter(&output)
		writer := tar.NewWriter(gz)
		writer.WriteHeader(&tar.Header{Name: "configs.json", Mode: 0644, Size: int64(len(raw))})
		writer.Write(raw)
		if includeFile {
			data := []byte("image bytes")
			writer.WriteHeader(&tar.Header{Name: entry, Mode: 0644, Size: int64(len(data))})
			writer.Write(data)
		}
		writer.Close()
		gz.Close()
	}
	return output.Bytes()
}

func TestResourceArchivePathsAndRetries(t *testing.T) {
	for _, format := range []string{"zip", "tar.gz"} {
		for _, separator := range []string{"/", "\\"} {
			t.Run(format+separator, func(t *testing.T) {
				t.Chdir(t.TempDir())
				s := testService(t)
				ctx := context.Background()
				entry := strings.Join([]string{"files", "resource-id", "中文 空格.png"}, separator)
				data := resourcePackage(t, format, entry, true, true)
				result, err := s.ImportArchiveWithResult(ctx, data, format, "", "", func(string, string, string) bool { return true }, false)
				if err != nil || result.Resources.Status != "complete" || result.Resources.Restored != 1 {
					t.Fatalf("result=%+v err=%v", result, err)
				}
				content, err := os.ReadFile(filepath.Join("data", "uploads", "resource-id", "中文 空格.png"))
				if err != nil || string(content) != "image bytes" {
					t.Fatalf("restored=%s %v", content, err)
				}
				retry, err := s.ImportArchiveWithResult(ctx, data, format, "", "", func(string, string, string) bool { return true }, false)
				if err != nil || len(retry.Configs) != 0 || retry.Resources.Reused != 1 || retry.Resources.Expected != 1 {
					t.Fatalf("retry=%+v err=%v", retry, err)
				}
				// The re-exported package must always use portable separators.
				rows := allConfigRows(t, s)
				var exported []byte
				if format == "zip" {
					exported, err = s.writeConfigArchive(ctx, rows)
				} else {
					exported, err = s.writeConfigTarGz(ctx, rows)
				}
				if err != nil {
					t.Fatal(err)
				}
				archive, err := readConfigArchive(exported, format)
				if err != nil {
					t.Fatal(err)
				}
				if string(archive.files["files/resource-id/中文 空格.png"]) != "image bytes" {
					t.Fatal("portable round trip lost bytes")
				}
			})
		}
	}
}

func TestResourceImportPartialFailureAndRetry(t *testing.T) {
	for _, failure := range []string{"missing_file", "missing_metadata", "file_write_failed", "metadata_write_failed", "conflict"} {
		t.Run(failure, func(t *testing.T) {
			t.Chdir(t.TempDir())
			s := testService(t)
			ctx := context.Background()
			entry := "files/resource-id/中文 空格.png"
			data := resourcePackage(t, "zip", entry, failure != "missing_file", failure != "missing_metadata")
			if failure == "file_write_failed" {
				if err := os.MkdirAll("data", 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile("data/uploads", []byte("block directory"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if failure == "metadata_write_failed" {
				s.logic.db.Callback().Create().Before("gorm:create").Register("test:asset_failure", func(tx *gorm.DB) {
					if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "asset" {
						tx.AddError(errors.New("injected asset row failure"))
					}
				})
			}
			if failure == "conflict" {
				if err := s.logic.db.Create(&model.Asset{FileID: "resource-id", FileName: "中文 空格.png", EnvironmentKey: "prod", PipelineKey: "main"}).Error; err != nil {
					t.Fatal(err)
				}
			}
			result, err := s.ImportArchiveWithResult(ctx, data, "zip", "", "", func(string, string, string) bool { return true }, false)
			if err != nil || result.Resources.Status != "partial" || result.Resources.Expected != 1 || result.Resources.Failed != 1 || len(result.Resources.Issues) != 1 || len(result.Configs) != 1 {
				t.Fatalf("partial=%+v err=%v", result, err)
			}
			if result.Resources.Expected != result.Resources.Restored+result.Resources.Reused+result.Resources.Failed {
				t.Fatal("incorrect counts")
			}
			if len(allConfigRows(t, s)) != 1 {
				t.Fatal("partial import did not commit config")
			}
			if failure == "metadata_write_failed" {
				if _, err := os.Stat(filepath.Join("data", "uploads", "resource-id", "中文 空格.png")); !os.IsNotExist(err) {
					t.Fatal("new orphan file was not cleaned")
				}
				s.logic.db.Callback().Create().Remove("test:asset_failure")
			}
			if failure == "file_write_failed" {
				if err := os.Remove("data/uploads"); err != nil {
					t.Fatal(err)
				}
			}
			if failure != "conflict" {
				fixed := resourcePackage(t, "zip", entry, true, true)
				retry, err := s.ImportArchiveWithResult(ctx, fixed, "zip", "", "", func(string, string, string) bool { return true }, false)
				if err != nil || len(retry.Configs) != 0 || retry.Resources.Status != "complete" || retry.Resources.Restored != 1 {
					t.Fatalf("retry=%+v err=%v", retry, err)
				}
			}
		})
	}
}

func TestArchiveRejectsDangerousAndAmbiguousPaths(t *testing.T) {
	for _, name := range []string{"../outside.png", "/absolute.png", `C:\outside.png`, `\\server\share\x`, `files/id/../escape.png`, "files/id/nul\x00.png"} {
		t.Run(name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			s := testService(t)
			data := resourcePackage(t, "zip", name, true, true)
			if _, err := s.ImportArchiveWithResult(context.Background(), data, "zip", "", "", nil, true); err == nil {
				t.Fatal("dangerous archive accepted")
			}
			if len(allConfigRows(t, s)) != 0 {
				t.Fatal("dangerous archive committed configs")
			}
		})
	}
	t.Run("duplicate normalized names", func(t *testing.T) {
		var buf bytes.Buffer
		writer := zip.NewWriter(&buf)
		for _, name := range []string{"configs.json", `files\id\a.png`, "files/id/a.png"} {
			file, _ := writer.Create(name)
			file.Write([]byte(`{"business_configs":[]}`))
		}
		writer.Close()
		if _, err := readConfigArchive(buf.Bytes(), "zip"); err == nil {
			t.Fatal("ambiguous archive accepted")
		}
	})
}

func TestMissingExportResourceIsRejected(t *testing.T) {
	t.Chdir(t.TempDir())
	s := testService(t)
	cfg := textConfig("LOGO", "asset://missing")
	cfg.Type = "image"
	rows := seedConfigs(t, s, cfg)
	for _, format := range []string{"zip", "tar.gz"} {
		var err error
		if format == "zip" {
			_, err = s.writeConfigArchive(context.Background(), rows)
		} else {
			_, err = s.writeConfigTarGz(context.Background(), rows)
		}
		var incomplete *IncompleteExportError
		if !errors.As(err, &incomplete) || len(incomplete.Issues) != 1 {
			t.Fatalf("format=%s err=%v", format, err)
		}
	}
}
