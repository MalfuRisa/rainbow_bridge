package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
)

type minioSnapshotStore struct {
	ctx    context.Context
	client *minio.Client
	row    *model.ConfigReleaseVersion
}

type snapshotObjectEntry struct {
	AssetID string
	Size    int64
	SHA256  string
}
type snapshotObjectManifest struct {
	Schema                                               int
	SnapshotID, AttemptID, PayloadSHA256, ManifestSHA256 string
	Assets                                               []snapshotObjectEntry
}

func objectManifest(row *model.ConfigReleaseVersion, assets []model.ConfigReleaseAsset) ([]byte, error) {
	var total int64
	for _, a := range assets {
		if a.Size < 0 || a.Size > model.ReleaseMaxAssetByte {
			return nil, releaseIncompleteError("invalid snapshot asset size")
		}
		total += a.Size
	}
	if len(assets) != row.AssetCount || total != row.AssetBytes || buildReleaseManifest(assets) != row.ManifestSha256 || payloadDigest(row.Payload) != row.PayloadSha256 {
		return nil, releaseIncompleteError("snapshot DB manifest mismatch")
	}
	m := snapshotObjectManifest{Schema: 1, SnapshotID: row.SnapshotID, AttemptID: row.StorageAttempt, PayloadSHA256: row.PayloadSha256, ManifestSHA256: row.ManifestSha256, Assets: []snapshotObjectEntry{}}
	for _, a := range assets {
		m.Assets = append(m.Assets, snapshotObjectEntry{a.AssetID, a.Size, a.Sha256})
	}
	sort.Slice(m.Assets, func(i, j int) bool { return m.Assets[i].AssetID < m.Assets[j].AssetID })
	return json.Marshal(m)
}

func snapshotObjectError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if isMissingObject(err) {
		return releaseIncompleteError("snapshot object is missing")
	}
	return releaseStorageError(errors.New("snapshot object operation failed"))
}

func (st *minioSnapshotStore) openKey(key string) (io.ReadCloser, error) {
	o, err := st.client.GetObject(st.ctx, st.row.StorageBucket, st.row.StoragePrefix+key, minio.GetObjectOptions{})
	if err != nil {
		return nil, snapshotObjectError(st.ctx, err)
	}
	return o, nil
}

func (st *minioSnapshotStore) openAsset(snapshot, asset string) (io.ReadCloser, error) {
	if snapshot != st.row.SnapshotID {
		return nil, releaseIncompleteError("snapshot identity mismatch")
	}
	if _, err := uuid.Parse(asset); err != nil {
		return nil, releaseIncompleteError("invalid snapshot asset identity")
	}
	return st.openKey("assets/" + asset + ".bin")
}

func (st *minioSnapshotStore) verifyAssetFile(snapshot, asset string, size int64, digest string) error {
	stream, err := st.openAsset(snapshot, asset)
	if err != nil {
		return err
	}
	defer stream.Close()
	return verifySnapshotStream(st.ctx, stream, size, digest, io.Discard)
}

// Reads at most the recorded size+1, with a hard business limit independent of DB values.
func verifySnapshotStream(ctx context.Context, source io.Reader, size int64, digest string, target io.Writer) error {
	if size < 0 || size > model.ReleaseMaxAssetByte {
		return releaseIncompleteError("invalid snapshot asset size")
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(target, h), io.LimitReader(releaseContextReader{ctx: ctx, source: source}, size+1))
	if err != nil {
		return snapshotObjectError(ctx, err)
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != digest {
		return releaseIncompleteError("snapshot asset integrity mismatch")
	}
	return nil
}

func (st *minioSnapshotStore) readAssetBytes(snapshot, asset string, size int64, digest string) ([]byte, error) {
	stream, err := st.openAsset(snapshot, asset)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	var buffer bytes.Buffer
	if err := verifySnapshotStream(st.ctx, stream, size, digest, &buffer); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func (st *minioSnapshotStore) checkManifest(assets []model.ConfigReleaseAsset) error {
	want, err := objectManifest(st.row, assets)
	if err != nil {
		return releaseInternalError(err)
	}
	stream, err := st.openKey("manifest.json")
	if err != nil {
		return err
	}
	defer stream.Close()
	actual, err := io.ReadAll(io.LimitReader(releaseContextReader{ctx: st.ctx, source: stream}, int64(len(want))+1))
	if err != nil {
		return snapshotObjectError(st.ctx, err)
	}
	if !bytes.Equal(actual, want) {
		return releaseIncompleteError("snapshot completeness manifest mismatch")
	}
	return nil
}

// A single PUT (<=32MiB) avoids multipart orphan creation; cleanup still handles
// incomplete uploads left by interrupted/older attempts. Never overwrite a conflict.
func (st *minioSnapshotStore) putChecked(key string, source io.Reader, size int64, digest string) error {
	full := st.row.StoragePrefix + key
	info, err := st.client.StatObject(st.ctx, st.row.StorageBucket, full, minio.StatObjectOptions{})
	if err == nil {
		if info.Size != size {
			return releaseIncompleteError("snapshot object conflict")
		}
	} else if !isMissingObject(err) {
		return snapshotObjectError(st.ctx, err)
	} else {
		result, err := st.client.PutObject(st.ctx, st.row.StorageBucket, full, io.LimitReader(releaseContextReader{ctx: st.ctx, source: source}, size+1), size, minio.PutObjectOptions{DisableMultipart: true, ContentType: "application/octet-stream"})
		if err != nil {
			return snapshotObjectError(st.ctx, err)
		}
		if result.Size != size {
			return releaseIncompleteError("snapshot upload size mismatch")
		}
	}
	stream, err := st.openKey(key)
	if err != nil {
		return err
	}
	defer stream.Close()
	// Generated manifest and resource GETs both remain within the business cap.
	return verifySnapshotStream(st.ctx, stream, size, digest, io.Discard)
}

func (s *Service) publishSnapshotObjects(ctx context.Context, local *releaseStorage, row *model.ConfigReleaseVersion, assets []model.ConfigReleaseAsset, attempt *model.ReleaseStorageAttempt) error {
	row.StorageBackend, row.StorageBucket, row.StoragePrefix, row.StorageAttempt = "minio", attempt.Bucket, attempt.Prefix, attempt.ID
	client, err := s.snapshotMinioClient()
	if err != nil {
		return err
	}
	st := &minioSnapshotStore{ctx: ctx, client: client, row: row}
	for _, asset := range assets {
		source, err := local.openAsset(row.SnapshotID, asset.AssetID)
		if err != nil {
			return err
		}
		err = st.putChecked("assets/"+asset.AssetID+".bin", source, asset.Size, asset.Sha256)
		source.Close()
		if err != nil {
			return err
		}
	}
	manifest, err := objectManifest(row, assets)
	if err != nil {
		return releaseInternalError(err)
	}
	if err := st.putChecked("manifest.json", bytes.NewReader(manifest), int64(len(manifest)), payloadDigest(string(manifest))); err != nil {
		return err
	}
	if err := st.checkManifest(assets); err != nil {
		return err
	}
	maybeReleaseCrash("after_snapshot_upload")
	return nil
}

func temporarySnapshotStorage() (*releaseStorage, func(), error) {
	root, err := os.MkdirTemp("", "rainbow-snapshot-attempt-*")
	if err != nil {
		return nil, nil, releaseStorageError(err)
	}
	return &releaseStorage{root: root}, func() { os.RemoveAll(root) }, nil
}
