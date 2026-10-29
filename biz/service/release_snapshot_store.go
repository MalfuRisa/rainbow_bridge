package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
)

// Source adapters and immutable snapshot stores deliberately have separate roles.
type releaseAssetStore interface {
	readAssetBytes(snapshot, asset string, size int64, digest string) ([]byte, error)
	verifyAssetFile(snapshot, asset string, size int64, digest string) error
	openAsset(snapshot, asset string) (io.ReadCloser, error)
}

type localSnapshotStore struct {
	*releaseStorage
	ctx context.Context
}

func (st *localSnapshotStore) readAssetBytes(snapshot, asset string, size int64, digest string) ([]byte, error) {
	return readSnapshotAsset(st.ctx, st, snapshot, asset, size, digest)
}
func (st *localSnapshotStore) verifyAssetFile(snapshot, asset string, size int64, digest string) error {
	stream, err := st.openAsset(snapshot, asset)
	if err != nil {
		return err
	}
	defer stream.Close()
	return verifySnapshotStream(st.ctx, stream, size, digest, io.Discard)
}
func readSnapshotAsset(ctx context.Context, st releaseAssetStore, snapshot, asset string, size int64, digest string) ([]byte, error) {
	stream, err := st.openAsset(snapshot, asset)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	var buffer bytes.Buffer
	if err := verifySnapshotStream(ctx, stream, size, digest, &buffer); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func (st *releaseStorage) openAsset(snapshot, asset string) (io.ReadCloser, error) {
	name := st.assetPath(snapshot, asset)
	if err := st.ensureWithinRoot(name); err != nil {
		return nil, err
	}
	info, err := os.Lstat(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, releaseIncompleteError("release asset is missing")
		}
		return nil, releaseStorageError(err)
	}
	if !info.Mode().IsRegular() {
		return nil, releaseIncompleteError("release asset is not a regular file")
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, releaseStorageError(err)
	}
	return f, nil
}

var snapshotBucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

func (s *Service) newSnapshotBackend() (string, error) {
	typ := "local"
	if s.config != nil && strings.TrimSpace(s.config.ReleaseStorage.Type) != "" {
		typ = s.config.ReleaseStorage.Type
	}
	if typ != "local" && typ != "minio" {
		return "", releaseStorageError(errors.New("unknown release storage backend"))
	}
	if typ == "minio" {
		bucket := s.config.ReleaseStorage.Bucket
		if !snapshotBucketPattern.MatchString(bucket) || strings.Contains(bucket, "..") || bucket == s.config.Storage.Minio.Bucket {
			return "", releaseStorageError(errors.New("a distinct explicit snapshot bucket is required"))
		}
	}
	return typ, nil
}

func (s *Service) snapshotMinioClient() (*minio.Client, error) {
	if s.config == nil {
		return nil, releaseStorageError(errors.New("snapshot connection is not configured"))
	}
	cfg := s.config.Storage.Minio
	client, err := minio.New(cfg.Endpoint, &minio.Options{Creds: credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""), Secure: cfg.UseSSL, Region: cfg.Region})
	if err != nil {
		return nil, releaseStorageError(errors.New("invalid snapshot connection"))
	}
	return client, nil
}

func validSnapshotLocator(row *model.ConfigReleaseVersion) bool {
	_, e1 := uuid.Parse(row.SnapshotID)
	_, e2 := uuid.Parse(row.StorageAttempt)
	return e1 == nil && e2 == nil && snapshotBucketPattern.MatchString(row.StorageBucket) && row.StoragePrefix == "snapshots/v1/"+row.SnapshotID+"/"+row.StorageAttempt+"/"
}

func (s *Service) snapshotStore(ctx context.Context, l *Logic, row *model.ConfigReleaseVersion) (releaseAssetStore, error) {
	switch row.StorageBackend {
	case "", "local":
		local, err := s.existingReleaseStorage()
		if err != nil {
			return nil, err
		}
		return &localSnapshotStore{releaseStorage: local, ctx: ctx}, nil
	case "minio":
		if !validSnapshotLocator(row) {
			return nil, releaseIncompleteError("invalid snapshot locator")
		}
		client, err := s.snapshotMinioClient()
		if err != nil {
			return nil, err
		}
		st := &minioSnapshotStore{ctx: ctx, client: client, row: row}
		assets, err := l.releaseDAO.ListAssets(ctx, l.db, row.SnapshotID)
		if err != nil {
			return nil, releaseInternalError(err)
		}
		if err := st.checkManifest(assets); err != nil {
			return nil, err
		}
		return st, nil
	default:
		return nil, releaseIncompleteError("unknown persisted snapshot backend")
	}
}
