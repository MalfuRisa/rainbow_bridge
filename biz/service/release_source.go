package service

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
)

// Captured identity of one source, never a user-supplied URL or bucket.
type releaseSource struct {
	Path      string
	Size      int64
	Modified  time.Time
	ETag      string
	VersionID string
	MinIO     bool
}

func (s *Service) releaseMinioClient() (*minio.Client, error) {
	if s.config == nil || s.config.Storage.Type != "minio" {
		return nil, errors.New("MinIO source is not configured")
	}
	cfg := s.config.Storage.Minio
	// Reading a release source must not create a bucket or use a background context.
	return minio.New(cfg.Endpoint, &minio.Options{Creds: credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""), Secure: cfg.UseSSL, Region: cfg.Region})
}

func (s *Service) inspectReleaseSource(ctx context.Context, asset *model.Asset) (releaseSource, error) {
	if err := ctx.Err(); err != nil {
		return releaseSource{}, err
	}
	if s.config != nil && s.config.Storage.Type == "minio" {
		client, err := s.releaseMinioClient()
		if err != nil {
			return releaseSource{}, releaseSourceFailure(ctx, err)
		}
		info, err := client.StatObject(ctx, s.config.Storage.Minio.Bucket, asset.Path, minio.StatObjectOptions{})
		if err != nil {
			return releaseSource{}, releaseSourceFailure(ctx, err)
		}
		if strings.TrimSpace(info.ETag) == "" {
			return releaseSource{}, releaseStorageError(errors.New("source object has no consistency token"))
		}
		return releaseSource{Path: asset.Path, Size: info.Size, Modified: info.LastModified, ETag: info.ETag, VersionID: info.VersionID, MinIO: true}, nil
	}
	path := filepath.Join(dataDirectory, asset.Path)
	info, err := os.Stat(path)
	if err != nil {
		return releaseSource{}, releaseSourceFailure(ctx, err)
	}
	if !info.Mode().IsRegular() {
		return releaseSource{}, releaseStorageError(errors.New("release source is not a regular file"))
	}
	return releaseSource{Path: path, Size: info.Size(), Modified: info.ModTime()}, nil
}

// Source failures expose no endpoint, object credentials, signed URL or SDK details.
func releaseSourceFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, os.ErrNotExist) || isMissingObject(err) {
		return os.ErrNotExist
	}
	return releaseStorageError(errors.New("managed release source could not be read consistently"))
}

func (s *Service) openReleaseSource(ctx context.Context, source releaseSource) (io.ReadCloser, func() error, error) {
	if !source.MinIO {
		file, err := os.Open(source.Path)
		if err != nil {
			return nil, nil, releaseSourceFailure(ctx, err)
		}
		verify := func() error {
			info, err := file.Stat()
			if err != nil {
				return releaseSourceFailure(ctx, err)
			}
			if info.Size() != source.Size || !info.ModTime().Equal(source.Modified) {
				return releaseStorageError(errors.New("local release source changed during capture"))
			}
			return ctx.Err()
		}
		if err := verify(); err != nil {
			file.Close()
			return nil, nil, err
		}
		return file, verify, nil
	}
	client, err := s.releaseMinioClient()
	if err != nil {
		return nil, nil, releaseSourceFailure(ctx, err)
	}
	options := minio.GetObjectOptions{VersionID: source.VersionID}
	if err := options.SetMatchETag(source.ETag); err != nil {
		return nil, nil, releaseSourceFailure(ctx, err)
	}
	object, err := client.GetObject(ctx, s.config.Storage.Minio.Bucket, source.Path, options)
	if err != nil {
		return nil, nil, releaseSourceFailure(ctx, err)
	}
	verify := func() error {
		info, err := client.StatObject(ctx, s.config.Storage.Minio.Bucket, source.Path, minio.StatObjectOptions{VersionID: source.VersionID})
		if err != nil {
			return releaseSourceFailure(ctx, err)
		}
		if info.Size != source.Size || info.ETag != source.ETag || !info.LastModified.Equal(source.Modified) {
			return releaseStorageError(errors.New("source object changed during capture"))
		}
		return ctx.Err()
	}
	return object, verify, nil
}

type releaseContextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r releaseContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(p)
}

func (s *Service) copyReleaseSource(ctx context.Context, storage *releaseStorage, snapshotID string, item releaseAssetPlan) (releaseCopiedAsset, error) {
	source, verify, err := s.openReleaseSource(ctx, item.Source)
	if err != nil {
		return releaseCopiedAsset{}, releaseCopySourceError(item, ctx, err)
	}
	defer source.Close()
	record, err := storage.copyAssetStreamToStaging(ctx, snapshotID, item.AssetID, source, item.FileName, item.ContentType, item.DeclaredSize, model.ReleaseMaxAssetByte)
	if err != nil {
		return releaseCopiedAsset{}, releaseCopySourceError(item, ctx, err)
	}
	if err := verify(); err != nil {
		return releaseCopiedAsset{}, releaseCopySourceError(item, ctx, err)
	}
	return record, nil
}

func releaseCopySourceError(item releaseAssetPlan, ctx context.Context, err error) error {
	if errors.Is(err, os.ErrNotExist) || isMissingObject(err) {
		return releaseResourceInvalidError([]ReleaseResourceIssue{{ResourceKey: item.ResourceKey, Alias: item.Alias, FileID: item.SourceFileID, Reason: "file_missing"}})
	}
	if _, ok := AsReleaseError(err); ok {
		return err
	}
	return releaseSourceFailure(ctx, err)
}
