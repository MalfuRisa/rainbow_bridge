package service

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
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
	metadata map[string]any
	files    map[string][]byte
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
	if _, err := buildResourceRestorePlan(archive, nil, nil, "", ""); err != nil {
		return nil, err
	}
	return archive, nil
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
