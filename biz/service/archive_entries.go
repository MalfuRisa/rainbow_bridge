package service

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func archiveEntryName(raw string) (string, error) {
	name := strings.ReplaceAll(raw, "\\", "/")
	if name == "" || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\x00:") {
		return "", fmt.Errorf("unsafe archive entry %q", raw)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", fmt.Errorf("unsafe archive entry %q", raw)
		}
	}
	name = path.Clean(name)
	if name == "." {
		return "", fmt.Errorf("empty archive entry %q", raw)
	}
	return name, nil
}

type configArchive struct {
	metadata   map[string]any
	files      map[string][]byte
	transferV2 bool
}

func readConfigArchive(data []byte, format string) (*configArchive, error) {
	entries := map[string][]byte{}
	seen := map[string]bool{}
	add := func(raw string, directory bool, mode os.FileMode, r io.Reader) error {
		name, err := archiveEntryName(raw)
		if err != nil {
			return err
		}
		if seen[name] {
			return fmt.Errorf("duplicate normalized archive entry %q", name)
		}
		seen[name] = true
		if directory {
			return nil
		}
		if !mode.IsRegular() {
			return fmt.Errorf("unsupported archive entry type %q", name)
		}
		if name != "configs.json" && !strings.HasPrefix(name, "files/") {
			return nil
		}
		if strings.HasPrefix(name, "files/") && len(strings.Split(name, "/")) != 3 {
			return fmt.Errorf("invalid resource entry %q", name)
		}
		payload, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		entries[name] = payload
		return nil
	}
	if format == "tar.gz" {
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		reader := tar.NewReader(gz)
		for {
			h, err := reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if h.Typeflag != tar.TypeDir && h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
				return nil, fmt.Errorf("unsupported TAR entry %q", h.Name)
			}
			if err := add(h.Name, h.Typeflag == tar.TypeDir, h.FileInfo().Mode(), reader); err != nil {
				return nil, err
			}
		}
	} else {
		reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range reader.File {
			r, err := f.Open()
			if err != nil {
				return nil, err
			}
			err = add(f.Name, f.FileInfo().IsDir(), f.Mode(), r)
			r.Close()
			if err != nil {
				return nil, err
			}
		}
	}
	payload, ok := entries["configs.json"]
	if !ok {
		return nil, errors.New("configs.json not found in archive")
	}
	var metadata map[string]any
	if err := DecodeConfigJSON(payload, &metadata); err != nil || metadata == nil {
		return nil, errors.New("invalid configs.json format")
	}
	delete(entries, "configs.json")
	archive := &configArchive{metadata: metadata, files: entries}
	if err := validateArchiveFormat(archive); err != nil {
		return nil, err
	}
	if err := validateV2Resources(archive); err != nil {
		return nil, err
	}
	if _, err := buildResourceRestorePlan(archive, nil, nil, "", ""); err != nil {
		return nil, err
	}
	return archive, nil
}

func validateArchiveFormat(archive *configArchive) error {
	raw, ok := archive.metadata["archive_format"]
	if !ok || raw == nil || strings.TrimSpace(fmt.Sprint(raw)) == "" {
		return nil
	}
	format, _ := raw.(string)
	switch format {
	case transferArchiveFormatV2:
		archive.transferV2 = true
		if source, ok := archive.metadata["export_source"].(map[string]any); ok {
			kind, _ := source["kind"].(string)
			switch kind {
			case TransferSourceEditor, TransferSourceActive, TransferSourceVersion:
			default:
				return errors.New("unknown export_source.kind in transfer archive")
			}
		}
		return nil
	case "rainbow-business-backup-v1":
		return errors.New("business backup package cannot be imported as a transfer archive")
	case "rainbow-release-static-v1":
		return errors.New("runtime static package cannot be imported as a transfer archive")
	default:
		return fmt.Errorf("unsupported transfer archive format %q", format)
	}
}

type v2ResourceEntry struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	Size     int64  `json:"size"`
	Sha256   string `json:"sha256"`
}

func validateV2Resources(archive *configArchive) error {
	if !archive.transferV2 {
		return nil
	}
	raw, ok := archive.metadata["resources"]
	if !ok {
		raw, ok = archive.metadata["assets"]
		if !ok {
			return nil
		}
	}
	encoded, err := jsonContent(raw)
	if err != nil {
		return err
	}
	var entries []v2ResourceEntry
	if err := DecodeConfigJSON([]byte(encoded), &entries); err != nil {
		return fmt.Errorf("invalid v2 resource summary: %w", err)
	}
	for _, entry := range entries {
		if entry.FileID == "" {
			return errors.New("v2 resource summary file_id required")
		}
		entryPath := path.Join("files", entry.FileID, entry.FileName)
		data, ok := archive.files[entryPath]
		if !ok {
			return fmt.Errorf("v2 resource file missing: %s", entry.FileID)
		}
		if entry.Size > 0 && int64(len(data)) != entry.Size {
			return fmt.Errorf("v2 resource size mismatch: %s", entry.FileID)
		}
		if entry.Sha256 != "" {
			digest := sha256.Sum256(data)
			if hex.EncodeToString(digest[:]) != strings.ToLower(entry.Sha256) {
				return fmt.Errorf("v2 resource digest mismatch: %s", entry.FileID)
			}
		}
	}
	return nil
}

func resourceDiskPath(fileID, fileName string) (string, string, error) {
	for _, value := range []string{fileID, fileName} {
		name, err := archiveEntryName(value)
		if err != nil || name != value || strings.Contains(name, "/") {
			return "", "", errors.New("invalid resource identity or filename")
		}
	}
	relative := filepath.Join(uploadDirectory, fileID, fileName)
	root, err := filepath.Abs(filepath.Join(dataDirectory, uploadDirectory))
	if err != nil {
		return "", "", err
	}
	full, err := filepath.Abs(filepath.Join(dataDirectory, relative))
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", errors.New("resource path escapes uploads")
	}
	// Do not follow existing symlink/reparse directories into another storage tree.
	for current := filepath.Dir(full); current != filepath.Dir(root); current = filepath.Dir(current) {
		if info, err := os.Lstat(current); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", "", errors.New("resource directory is a symlink")
		}
		if current == root {
			break
		}
	}
	return relative, full, nil
}
