package backup

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// writePackage builds the ZIP in a .part file, verifies its digest and only
// then publishes it under the final backup id.
func writePackage(ctx context.Context, finalPath, databasePath string, manifest *Manifest,
	plans []filePlan, limits Limits) (int64, string, error) {
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o700); err != nil {
		return 0, "", err
	}
	partPath := finalPath + ".part"
	_ = os.Remove(partPath)
	handle, err := os.OpenFile(partPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, "", err
	}
	cleanup := func() {
		_ = handle.Close()
		_ = os.Remove(partPath)
	}
	hasher := sha256.New()
	zipWriter := zip.NewWriter(io.MultiWriter(handle, hasher))

	manifestPayload, err := manifest.encode()
	if err != nil {
		cleanup()
		return 0, "", err
	}
	if err := writeZipBytes(zipWriter, ManifestEntry, manifestPayload); err != nil {
		cleanup()
		return 0, "", err
	}
	dbSize, dbSha, err := verifyRegularFile(databasePath)
	if err != nil {
		cleanup()
		return 0, "", err
	}
	if err := writeZipFile(ctx, zipWriter, DatabaseEntry, databasePath, dbSize, dbSha); err != nil {
		cleanup()
		return 0, "", err
	}
	for _, plan := range plans {
		if err := ctx.Err(); err != nil {
			cleanup()
			return 0, "", err
		}
		if err := writeZipFile(ctx, zipWriter, plan.ArchivePath, plan.SourcePath, plan.Size, plan.Sha256); err != nil {
			cleanup()
			return 0, "", err
		}
	}
	if err := zipWriter.Close(); err != nil {
		cleanup()
		return 0, "", err
	}
	if err := handle.Sync(); err != nil {
		cleanup()
		return 0, "", err
	}
	info, err := handle.Stat()
	if err != nil {
		cleanup()
		return 0, "", err
	}
	if info.Size() > limits.MaxPackageBytes {
		cleanup()
		return 0, "", limitError("backup package exceeds the size limit")
	}
	if err := handle.Close(); err != nil {
		_ = os.Remove(partPath)
		return 0, "", err
	}
	if err := os.Rename(partPath, finalPath); err != nil {
		_ = os.Remove(partPath)
		return 0, "", err
	}
	if err := syncDir(filepath.Dir(finalPath)); err != nil {
		return 0, "", err
	}
	return info.Size(), hex.EncodeToString(hasher.Sum(nil)), nil
}

func writeZipBytes(writer *zip.Writer, name string, payload []byte) error {
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.Modified = time.Now().UTC()
	header.SetMode(0o600)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = entry.Write(payload)
	return err
}

func writeZipFile(ctx context.Context, writer *zip.Writer, name, sourcePath string,
	expectedSize int64, expectedSha string) error {
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.Modified = time.Now().UTC()
	header.SetMode(0o600)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		return err
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(entry, hasher), contextReader{ctx: ctx, reader: source})
	if err != nil {
		return err
	}
	if written != expectedSize || hex.EncodeToString(hasher.Sum(nil)) != expectedSha {
		return fmt.Errorf("file changed while packaging")
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
