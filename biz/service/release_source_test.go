package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/pkg/config"
)

type releaseS3Fixture struct {
	mu      sync.Mutex
	data    []byte
	mode    string
	etag    string
	matched bool
	heads   int
}

func (f *releaseS3Fixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	data, mode, etag := append([]byte(nil), f.data...), f.mode, f.etag
	if r.Method == "HEAD" {
		f.heads++
	}
	if r.Method == "GET" && r.Header.Get("If-Match") == "\""+etag+"\"" {
		f.matched = true
	}
	f.mu.Unlock()
	if mode == "missing" || mode == "lazy-missing" && r.Method == "GET" {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(404)
		fmt.Fprint(w, "<Error><Code>NoSuchKey</Code></Error>")
		return
	}
	if mode == "denied" {
		w.WriteHeader(403)
		fmt.Fprint(w, "<Error><Code>AccessDenied</Code></Error>")
		return
	}
	if mode == "disconnect" && r.Method == "GET" {
		if h, ok := w.(http.Hijacker); ok {
			conn, _, _ := h.Hijack()
			conn.Close()
		}
		return
	}
	if mode == "cancel" && r.Method == "GET" {
		<-r.Context().Done()
		return
	}
	if r.Method == "GET" && r.Header.Get("If-Match") != "" && r.Header.Get("If-Match") != "\""+etag+"\"" {
		w.WriteHeader(412)
		fmt.Fprint(w, "<Error><Code>PreconditionFailed</Code></Error>")
		return
	}
	w.Header().Set("ETag", "\""+etag+"\"")
	w.Header().Set("Last-Modified", "Mon, 01 Jan 2024 00:00:00 GMT")
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	w.Header().Set("Content-Type", "application/octet-stream")
	if r.Method == "GET" {
		_, _ = w.Write(data)
	}
}
func sourceS3(t *testing.T) (*Service, *releaseS3Fixture) {
	t.Helper()
	f := &releaseS3Fixture{data: []byte("immutable source bytes"), etag: "version-one"}
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	s := NewService(nil, nil, "", &config.Config{Storage: config.StorageConfig{Type: "minio", Minio: config.MinioConfig{Endpoint: strings.TrimPrefix(server.URL, "http://"), Bucket: "qa-release", AccessKey: "fixture", SecretKey: "fixture-only", Region: "us-east-1"}}})
	return s, f
}
func TestReleaseMinIOSourceStream(t *testing.T) {
	s, f := sourceS3(t)
	asset := &model.Asset{Path: "owned/object", FileSize: int64(len(f.data))}
	source, err := s.inspectReleaseSource(context.Background(), asset)
	if err != nil {
		t.Fatal(err)
	}
	storage := &releaseStorage{root: t.TempDir()}
	item := releaseAssetPlan{AssetID: uuid.NewString(), SourceFileID: "file", Source: source, DeclaredSize: source.Size, FileName: "file.txt", ContentType: "text/plain"}
	snapshot := uuid.NewString()
	copied, err := s.copyReleaseSource(context.Background(), storage, snapshot, item)
	if err != nil {
		t.Fatal(err)
	}
	if !f.matched || copied.Size != asset.FileSize {
		t.Fatal("missing conditional read or bad byte count")
	}
	before, err := os.ReadFile(storage.stagingAssetPath(snapshot, item.AssetID))
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.data = []byte("replaced mutable source")
	f.etag = "version-two"
	f.mu.Unlock()
	if !bytes.Equal(before, []byte("immutable source bytes")) {
		t.Fatal("wrong snapshot bytes")
	}
	if _, err := s.copyReleaseSource(context.Background(), storage, uuid.NewString(), item); err == nil {
		t.Fatal("changed object accepted with old ETag")
	}
}
func TestReleaseMinIOSourceFailures(t *testing.T) {
	for _, mode := range []string{"missing", "lazy-missing", "denied", "disconnect", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			s, f := sourceS3(t)
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			if mode == "missing" || mode == "denied" {
				f.mode = mode
			}
			source, err := s.inspectReleaseSource(ctx, &model.Asset{Path: "object"})
			if mode == "missing" {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("missing classification: %v", err)
				}
				return
			}
			if mode == "denied" {
				if err == nil || errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "fixture-only") {
					t.Fatalf("permission classification: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			f.mode = mode
			f.mu.Unlock()
			_, err = s.copyReleaseSource(ctx, &releaseStorage{root: t.TempDir()}, uuid.NewString(), releaseAssetPlan{AssetID: uuid.NewString(), Source: source, DeclaredSize: source.Size})
			if err == nil {
				t.Fatal("source failure accepted")
			}
			if mode == "lazy-missing" {
				requireReleaseErrorType(t, err, ErrTypeReleaseResourceInvalid)
			}
			if strings.Contains(err.Error(), "fixture-only") {
				t.Fatal("credential leaked")
			}
		})
	}
}
func TestReleaseStreamBoundsAndSize(t *testing.T) {
	for _, test := range []struct {
		name      string
		size, max int64
		ctx       context.Context
	}{
		{"oversize", 10, 4, context.Background()}, {"metadata", 8, 100, context.Background()},
	} {
		t.Run(test.name, func(t *testing.T) {
			st := &releaseStorage{root: t.TempDir()}
			_, err := st.copyAssetStreamToStaging(test.ctx, "snap", "asset", strings.NewReader("0123456789"), "name", "text/plain", test.size, test.max)
			if err == nil {
				t.Fatal("invalid size accepted")
			}
			if _, err := os.Stat(st.stagingAssetPath("snap", "asset")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed copy left a file")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (&releaseStorage{root: t.TempDir()}).copyAssetStreamToStaging(ctx, "snap", "asset", io.LimitReader(bytes.NewReader([]byte("abc")), 3), "name", "text/plain", 3, 4)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel lost: %v", err)
	}
}

func TestReleaseMinIOEmptyAndResourceFree(t *testing.T) {
	for _, withText := range []bool{false, true} {
		t.Run(fmt.Sprint(withText), func(t *testing.T) {
			s := newFreezeService(t)
			seedFreezeScope(t, s, "minio_empty", "default")
			if withText {
				if _, err := s.AddConfig(adminTestContext(), buildConfig("minio_empty", "default", "TEXT", "text", "value")); err != nil {
					t.Fatal(err)
				}
			}
			s.config = &config.Config{Storage: config.StorageConfig{Type: "minio"}}
			state := mustLock(t, s, "minio_empty", "default", "pure configuration")
			created, err := s.CreateRelease(adminTestContext(), ReleaseCreateInput{EnvironmentKey: "minio_empty", PipelineKey: "default", ExpectedFreezeRevisionRaw: fmt.Sprint(state.Revision), RequestID: uuid.NewString()})
			if err != nil || created.Meta.AssetCount != 0 {
				t.Fatalf("resource-free release failed: %+v %v", created, err)
			}
			want := 0
			if withText {
				want = 1
			}
			if created.Meta.ConfigCount != want {
				t.Fatalf("config count=%d want=%d", created.Meta.ConfigCount, want)
			}
		})
	}
}
