// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package imaging

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"syscall"

	resize "github.com/disintegration/imaging"

	"github.com/olegiv/ocms-go/internal/model"
)

// RootFileIdentity identifies a file created through an already-open root.
// Callers retain the FileInfo snapshot so compensating cleanup can distinguish
// their file from a same-path replacement created by another actor.
type RootFileIdentity struct {
	Path string
	Info fs.FileInfo
}

// RootFileCreation contains the published file identity and, when this write
// atomically reserved its UUID directory, that directory's identity as well.
type RootFileCreation struct {
	File      RootFileIdentity
	Directory *RootFileIdentity
}

// CreateVariantFromRoot creates one standard variant without resolving an
// ordinary filesystem path. Both the original read and the exclusive variant
// publish stay bound to uploadRoot, even if its former pathname is renamed or
// replaced while a multi-file import is in progress.
func CreateVariantFromRoot(
	uploadRoot *os.Root,
	mediaUUID, filename, variantType string,
) (*VariantResult, *RootFileCreation, error) {
	if uploadRoot == nil {
		return nil, nil, errors.New("uploads root is nil")
	}
	if !IsCanonicalMediaUUID(mediaUUID) {
		return nil, nil, fmt.Errorf("invalid media UUID %q", mediaUUID)
	}
	if filename == "" || filename != filepath.Base(filename) || !fs.ValidPath(filename) {
		return nil, nil, errors.New("invalid filename")
	}
	config, supported := model.ImageVariants[variantType]
	if !supported {
		return nil, nil, fmt.Errorf("invalid image variant type %q", variantType)
	}

	sourcePath := path.Join(model.OriginalsDir, mediaUUID, filename)
	source, err := uploadRoot.Open(sourcePath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open source image: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(source, maxDecodableBytes+1))
	closeErr := source.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, nil, fmt.Errorf("failed to read source image: %w", err)
	}
	if len(data) > maxDecodableBytes {
		return nil, nil, fmt.Errorf("image exceeds maximum size of %d bytes", int64(maxDecodableBytes))
	}
	width, height, mimeType, err := ValidateImage(bytes.NewReader(data))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to validate source image: %w", err)
	}
	if err := validateImageDimensions(width, height); err != nil {
		return nil, nil, err
	}
	format := detectFormatFromFilename(filename)
	if formatToMimeType(format) != mimeType {
		return nil, nil, fmt.Errorf("source image MIME type %q does not match filename %q", mimeType, filename)
	}

	img, err := resize.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decode source image: %w", err)
	}
	bounds := img.Bounds()
	srcWidth, srcHeight := bounds.Dx(), bounds.Dy()
	if !config.Crop && srcWidth < config.Width/2 && srcHeight < config.Height/2 {
		return nil, nil, nil
	}

	var resizedImage = img
	if config.Crop {
		resizedImage = resize.Fill(img, config.Width, config.Height, resize.Center, resize.Lanczos)
	} else {
		resizedImage = resize.Fit(img, config.Width, config.Height, resize.Lanczos)
	}
	resizedBounds := resizedImage.Bounds()
	processed, err := encodeImage(resizedImage, format, config.Quality)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to encode variant: %w", err)
	}

	relativePath := path.Join(variantType, mediaUUID, filename)
	creation, err := writeRootFileExclusive(uploadRoot, relativePath, processed)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to save %s variant: %w", variantType, err)
	}
	return &VariantResult{
		Type:     variantType,
		Width:    resizedBounds.Dx(),
		Height:   resizedBounds.Dy(),
		Size:     int64(len(processed)),
		FilePath: filepath.Join(uploadRoot.Name(), filepath.FromSlash(relativePath)),
	}, creation, nil
}

// writeRootFileExclusive publishes data without truncating a path that another
// actor created. The returned identity is captured from the open descriptor,
// not by reopening the pathname after publication.
func writeRootFileExclusive(uploadRoot *os.Root, relativePath string, data []byte) (*RootFileCreation, error) {
	if uploadRoot == nil {
		return nil, errors.New("uploads root is nil")
	}
	if relativePath == "." || !fs.ValidPath(relativePath) {
		return nil, fmt.Errorf("invalid root-relative file path %q", relativePath)
	}
	directory := path.Dir(relativePath)
	storageDirectory := path.Dir(directory)
	if err := uploadRoot.Mkdir(storageDirectory, 0o750); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("create storage directory: %w", err)
	}
	storageInfo, err := uploadRoot.Lstat(storageDirectory)
	if err != nil {
		return nil, fmt.Errorf("inspect storage directory: %w", err)
	}
	if !storageInfo.IsDir() || storageInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("storage path %q is not a directory", storageDirectory)
	}
	if err := uploadRoot.Mkdir(directory, 0o750); err != nil {
		return nil, fmt.Errorf("create destination directory: %w", err)
	}
	directoryInfo, err := uploadRoot.Lstat(directory)
	if err != nil {
		return nil, fmt.Errorf("inspect destination directory: %w", err)
	}
	directoryIdentity := &RootFileIdentity{Path: directory, Info: directoryInfo}

	file, err := uploadRoot.OpenFile(relativePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		removeDirErr := removeRootDirectoryIfSameAndEmpty(uploadRoot, directoryIdentity)
		return nil, errors.Join(fmt.Errorf("create destination file: %w", err), removeDirErr)
	}
	identityInfo, identityErr := file.Stat()
	if identityErr != nil {
		closeErr := file.Close()
		return nil, errors.Join(fmt.Errorf("inspect destination file: %w", identityErr), closeErr)
	}
	identity := &RootFileIdentity{Path: relativePath, Info: identityInfo}

	written, writeErr := file.Write(data)
	if writeErr == nil && written != len(data) {
		writeErr = io.ErrShortWrite
	}
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		cleanupErr := removeRootFileIfSame(uploadRoot, identity)
		removeDirErr := removeRootDirectoryIfSameAndEmpty(uploadRoot, directoryIdentity)
		return nil, errors.Join(fmt.Errorf("write destination file: %w", err), cleanupErr, removeDirErr)
	}
	return &RootFileCreation{File: *identity, Directory: directoryIdentity}, nil
}

func removeRootFileIfSame(uploadRoot *os.Root, identity *RootFileIdentity) error {
	if identity == nil || identity.Info == nil {
		return nil
	}
	current, err := uploadRoot.Lstat(identity.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect created file for cleanup: %w", err)
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(current, identity.Info) {
		return nil
	}
	if err := uploadRoot.Remove(identity.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove created file: %w", err)
	}
	return nil
}

func removeRootDirectoryIfSameAndEmpty(uploadRoot *os.Root, identity *RootFileIdentity) error {
	if identity == nil || identity.Info == nil {
		return nil
	}
	current, err := uploadRoot.Lstat(identity.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect destination directory for cleanup: %w", err)
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.IsDir() || !os.SameFile(current, identity.Info) {
		return nil
	}
	err = uploadRoot.Remove(identity.Path)
	if err == nil || errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST) {
		return nil
	}
	return fmt.Errorf("remove empty destination directory: %w", err)
}
