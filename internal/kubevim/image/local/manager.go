package local

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	vivnfm "github.com/kube-nfv/kube-vim-api/pkg/apis/vivnfm"
	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/config/kubevim"
)

type manager struct {
	location string
	lock     sync.Mutex
}

func NewLocalImageManager(cfg *config.LocalImageConfig) (*manager, error) {
	stat, err := os.Stat(*cfg.Location)
	if err != nil {
		return nil, fmt.Errorf("get file stats for location '%s': %w", *cfg.Location, err)
	}
	if !stat.IsDir() {
		return nil, &apperrors.ErrInvalidArgument{Field: "location", Reason: fmt.Sprintf("'%s' is not a directory", *cfg.Location)}
	}
	if stat.Mode()&0400 == 0 {
		return nil, &apperrors.ErrPermissionDenied{Resource: fmt.Sprintf("location '%s'", *cfg.Location), Reason: "no read permissions"}
	}
	if stat.Mode()&0200 == 0 {
		return nil, &apperrors.ErrPermissionDenied{Resource: fmt.Sprintf("location '%s'", *cfg.Location), Reason: "no write permissions"}
	}
	return &manager{
		location: *cfg.Location,
		lock:     sync.Mutex{},
	}, nil
}

func (m *manager) GetImage(ctx context.Context, id *nfvcommon.Identifier) (*vivnfm.SoftwareImageInformation, error) {
	m.lock.Lock()
	defer m.lock.Unlock()
	if id == nil || id.Value == "" {
		return nil, &apperrors.ErrInvalidArgument{Field: "image id", Reason: "cannot be empty"}
	}
	files, err := os.ReadDir(m.location)
	if err != nil {
		return nil, fmt.Errorf("read image directory '%s': %w", m.location, err)
	}
	for _, file := range files {
		if nameMatchRule(id, file) {
			return convertImage(file)
		}
	}
	return nil, &apperrors.ErrNotFound{Entity: "image", Identifier: id.Value}
}

func (m *manager) ListImages(ctx context.Context) ([]*vivnfm.SoftwareImageInformation, error) {
	m.lock.Lock()
	defer m.lock.Unlock()
	files, err := os.ReadDir(m.location)
	if err != nil {
		return nil, fmt.Errorf("read image directory '%s': %w", m.location, err)
	}
	res := make([]*vivnfm.SoftwareImageInformation, 0, len(files))
	for _, file := range files {
		imageInfo, err := convertImage(file)
		if err != nil {
			continue
		}
		res = append(res, imageInfo)
	}
	return res, nil
}

func (m *manager) UploadImage(ctx context.Context, id *nfvcommon.Identifier, location string) error {
	if id == nil || id.Value == "" {
		return &apperrors.ErrInvalidArgument{Field: "image id", Reason: "cannot be empty"}
	}
	m.lock.Lock()
	defer m.lock.Unlock()

	files, err := os.ReadDir(m.location)
	if err != nil {
		return fmt.Errorf("read source directory '%s': %w", m.location, err)
	}
	var sourceFilePath string
	for _, file := range files {
		if nameMatchRule(id, file) {
			sourceFilePath = filepath.Join(m.location, file.Name())
			break
		}
	}
	if sourceFilePath == "" {
		return &apperrors.ErrNotFound{Entity: "image file", Identifier: id.Value}
	}

	destDir := filepath.Dir(location)
	info, err := os.Stat(destDir)
	if os.IsNotExist(err) {
		return &apperrors.ErrNotFound{Entity: "destination directory", Identifier: destDir}
	}
	if err != nil || !info.IsDir() {
		return fmt.Errorf("access destination directory '%s': %w", destDir, err)
	}

	sourceFile, err := os.Open(sourceFilePath)
	if err != nil {
		return fmt.Errorf("open source file '%s' for image '%s': %w", sourceFilePath, id.Value, err)
	}
	defer sourceFile.Close()

	destinationFilePath := filepath.Join(location, id.Value)
	destFile, err := os.Create(destinationFilePath)
	if err != nil {
		return fmt.Errorf("create destination file '%s' for image '%s': %w", destinationFilePath, id.Value, err)
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return fmt.Errorf("copy image '%s' from '%s' to '%s': %w", id.Value, sourceFilePath, destinationFilePath, err)
	}
	return nil
}

func convertImage(file os.DirEntry) (*vivnfm.SoftwareImageInformation, error) {
	// TODO:
	return &vivnfm.SoftwareImageInformation{}, nil
}

// nameMatchRule checks if the file name matches the identifier, ignoring the extension.
// Rule is based on the name equality without extension eg.
// nameMatchRule("ubuntu-22.0.4", file{name: ubuntu-22.0.4}) -> true (equal match)
// nameMatchRule("ubuntu-22.0.4", file{name: ubuntu-22.0.4.iso}) -> true (match without extension)
func nameMatchRule(id *nfvcommon.Identifier, file os.DirEntry) bool {
	if id == nil {
		return false
	}
	fileName := file.Name()
	fileBase := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	return fileBase == id.Value || fileName == id.Value
}
