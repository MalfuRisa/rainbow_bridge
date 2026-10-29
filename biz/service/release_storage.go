package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// 独立快照目录:与普通上传/导入/迁移完全分离。
const (
	releaseDirEnv         = "RAINBOW_RELEASE_DIR"
	releaseDefaultDirName = "release-snapshots"
	releaseStagingDirName = ".staging"
	releaseAssetsDirName  = "assets"
	releaseAssetBinSuffix = ".bin"
)

type releaseStorage struct {
	root string
}

// releaseStorage 解析并校验快照根:RAINBOW_RELEASE_DIR(必须绝对)或 <cwd>/data/release-snapshots。
// 首次创建后解析为确定的绝对根;该路径不进入 HTTP 响应与审计。
func (s *Service) releaseStorage() (*releaseStorage, error) {
	return s.resolveReleaseStorage(true)
}

func (s *Service) existingReleaseStorage() (*releaseStorage, error) {
	return s.resolveReleaseStorage(false)
}

func (s *Service) resolveReleaseStorage(create bool) (*releaseStorage, error) {
	root := strings.TrimSpace(os.Getenv(releaseDirEnv))
	if root != "" {
		if !filepath.IsAbs(root) {
			return nil, newReleaseError(500, ErrTypeReleaseStorageError, "RAINBOW_RELEASE_DIR must be an absolute path")
		}
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, releaseStorageError(err)
		}
		root = filepath.Join(cwd, dataDirectory, releaseDefaultDirName)
	}
	if create {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return nil, releaseStorageError(err)
		}
	} else {
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			return nil, releaseIncompleteError("local snapshot root is unavailable")
		}
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, releaseStorageError(err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, releaseStorageError(err)
	}
	return &releaseStorage{root: resolved}, nil
}

func (st *releaseStorage) stagingRoot() string {
	return filepath.Join(st.root, releaseStagingDirName)
}

func (st *releaseStorage) stagingDir(snapshotID string) string {
	return filepath.Join(st.stagingRoot(), snapshotID)
}

func (st *releaseStorage) finalDir(snapshotID string) string {
	return filepath.Join(st.root, snapshotID)
}

func (st *releaseStorage) assetPath(snapshotID, assetID string) string {
	return filepath.Join(st.finalDir(snapshotID), releaseAssetsDirName, assetID+releaseAssetBinSuffix)
}

func (st *releaseStorage) stagingAssetPath(snapshotID, assetID string) string {
	return filepath.Join(st.stagingDir(snapshotID), releaseAssetsDirName, assetID+releaseAssetBinSuffix)
}

// ensureWithinRoot 校验目标仍在快照根内(拒绝符号链接/重解析点越界)。
func (st *releaseStorage) ensureWithinRoot(target string) error {
	absolute, err := filepath.Abs(target)
	if err != nil {
		return releaseStorageError(err)
	}
	relative, err := filepath.Rel(st.root, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return newReleaseError(500, ErrTypeReleaseStorageError, "release path escapes snapshot root")
	}
	for current := filepath.Dir(absolute); current != st.root && len(current) > len(st.root); current = filepath.Dir(current) {
		if info, err := os.Lstat(current); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return newReleaseError(500, ErrTypeReleaseStorageError, "release path traverses a symlink")
		}
	}
	return nil
}

// releaseCopiedAsset 是一次成功复制的资源记录。
type releaseCopiedAsset struct {
	AssetID      string
	SourceFileID string
	FileName     string
	ContentType  string
	Size         int64
	Sha256       string
}

// copyAssetToStaging 流式复制单个资源到准备目录,边复制边统计与计算 SHA-256,
// 落盘后复读校验;超过单份上限返回 413。
func (st *releaseStorage) copyAssetToStaging(snapshotID, assetID, sourcePath, fileName, contentType string, maxBytes int64) (releaseCopiedAsset, error) {
	source, err := os.Open(sourcePath)
	if err != nil {
		return releaseCopiedAsset{}, releaseStorageError(err)
	}
	defer source.Close()
	return st.copyAssetStreamToStaging(context.Background(), snapshotID, assetID, source, fileName, contentType, -1, maxBytes)
}

func (st *releaseStorage) copyAssetStreamToStaging(ctx context.Context, snapshotID, assetID string, source io.Reader, fileName, contentType string, expectedSize, maxBytes int64) (releaseCopiedAsset, error) {
	target := st.stagingAssetPath(snapshotID, assetID)
	if err := st.ensureWithinRoot(target); err != nil {
		return releaseCopiedAsset{}, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return releaseCopiedAsset{}, releaseStorageError(err)
	}
	destination, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return releaseCopiedAsset{}, releaseStorageError(err)
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(destination, hasher), io.LimitReader(releaseContextReader{ctx: ctx, source: source}, maxBytes+1))
	closeErr := destination.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(target)
		if copyErr != nil {
			return releaseCopiedAsset{}, copyErr
		}
		return releaseCopiedAsset{}, releaseStorageError(closeErr)
	}
	if written > maxBytes {
		_ = os.Remove(target)
		return releaseCopiedAsset{}, releaseLimitExceededError(ReleaseLimitInfo{Item: "asset_bytes", Limit: maxBytes, Actual: written})
	}
	if expectedSize >= 0 && written != expectedSize {
		_ = os.Remove(target)
		return releaseCopiedAsset{}, releaseStorageError(errors.New("release source size differs from captured metadata"))
	}
	digest := hex.EncodeToString(hasher.Sum(nil))

	// 复读校验:字节数与摘要必须与写入一致
	verifiedSize, verifiedDigest, err := st.verifyFile(target)
	if err != nil {
		_ = os.Remove(target)
		return releaseCopiedAsset{}, err
	}
	if verifiedSize != written || verifiedDigest != digest {
		_ = os.Remove(target)
		return releaseCopiedAsset{}, releaseStorageError(errors.New("release asset verification mismatch after write"))
	}
	return releaseCopiedAsset{
		AssetID:      assetID,
		SourceFileID: "",
		FileName:     fileName,
		ContentType:  contentType,
		Size:         written,
		Sha256:       digest,
	}, nil
}

// verifyFile 复读文件并返回实际字节数与 SHA-256。
func (st *releaseStorage) verifyFile(path string) (int64, string, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, "", releaseIncompleteError("release asset file is missing")
		}
		return 0, "", releaseStorageError(err)
	}
	defer file.Close()
	hasher := sha256.New()
	size, err := io.Copy(hasher, file)
	if err != nil {
		return 0, "", releaseStorageError(err)
	}
	return size, hex.EncodeToString(hasher.Sum(nil)), nil
}

// verifyAssetFile 校验清单行与磁盘文件一致(激活与下载共用)。
func (st *releaseStorage) verifyAssetFile(snapshotID, assetID string, expectedSize int64, expectedSha string) error {
	stream, err := st.openAsset(snapshotID, assetID)
	if err != nil {
		return err
	}
	defer stream.Close()
	return verifySnapshotStream(context.Background(), stream, expectedSize, expectedSha, io.Discard)
}

// readAssetBytes 读取并校验完整 SHA-256 后返回字节(下载路径)。
func (st *releaseStorage) readAssetBytes(snapshotID, assetID string, expectedSize int64, expectedSha string) ([]byte, error) {
	return readSnapshotAsset(context.Background(), st, snapshotID, assetID, expectedSize, expectedSha)
}

// moveStagingToFinal 将准备目录移动到最终 UUID 目录;不覆盖任何已有目录。
func (st *releaseStorage) moveStagingToFinal(snapshotID string) error {
	staging := st.stagingDir(snapshotID)
	final := st.finalDir(snapshotID)
	if err := st.ensureWithinRoot(staging); err != nil {
		return err
	}
	if err := st.ensureWithinRoot(final); err != nil {
		return err
	}
	if _, err := os.Stat(final); err == nil {
		return releaseStorageError(errors.New("final release directory already exists"))
	} else if !errors.Is(err, os.ErrNotExist) {
		return releaseStorageError(err)
	}
	if err := os.Rename(staging, final); err != nil {
		return releaseStorageError(err)
	}
	return nil
}

// removeStaging 清理本次准备目录(只允许自己的 UUID 目录)。
func (st *releaseStorage) removeStaging(snapshotID string) error {
	staging := st.stagingDir(snapshotID)
	if err := st.ensureWithinRoot(staging); err != nil {
		return err
	}
	return os.RemoveAll(staging)
}

// removeFinal 清理本次最终目录(仅用于确认未提交后的自清理)。
func (st *releaseStorage) removeFinal(snapshotID string) error {
	final := st.finalDir(snapshotID)
	if err := st.ensureWithinRoot(final); err != nil {
		return err
	}
	return os.RemoveAll(final)
}
