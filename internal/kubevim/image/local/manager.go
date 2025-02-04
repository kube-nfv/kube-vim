package local

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/kube-nfv/kube-vim-api/pb/nfv"
	"github.com/kube-nfv/kube-vim/internal/config/kubevim"
)

type manager struct {
	location string
	lock     sync.Mutex
}

func NewLocalImageManager(cfg *config.LocalImageConfig) (*manager, error) {
	stat, err := os.Stat(*cfg.Location)
	if err != nil {
		return nil, fmt.Errorf("failed to get file \"%s\"stats: %w", *cfg.Location, err)
	}
	if !stat.IsDir() {
		return nil, fmt.Errorf("provided location \"%s\" is not directory", *cfg.Location)
	}
	if stat.Mode()&0400 == 0 {
		return nil, fmt.Errorf("no read permissions for provided location \"%s\"", *cfg.Location)
	}
	if stat.Mode()&0200 == 0 {
		return nil, fmt.Errorf("no write permissions for provided location \"%s\"", *cfg.Location)
	}
	return &manager{
		location: *cfg.Location,
		lock:     sync.Mutex{},
	}, nil
}

func (m *manager) GetImage(ctx context.Context, id *nfv.Identifier) (*nfv.SoftwareImageInformation, error) {
	m.lock.Lock()
	defer m.lock.Unlock()
	if id == nil || id.Value == "" {
		return nil, fmt.Errorf("id should not be empty")
	}
	files, err := os.ReadDir(m.location)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory \"%s\"", m.location)
	}
	for _, file := range files {
		if nameMatchRule(id, file) {
			return convertImage(file)
		}
	}
	return nil, fmt.Errorf("image with id \"%s\" not found", id.Value)
}

func (m *manager) GetImages(ctx context.Context) ([]*nfv.SoftwareImageInformation, error) {
	m.lock.Lock()
	defer m.lock.Unlock()
	files, err := os.ReadDir(m.location)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory \"%s\"", m.location)
	}
	res := make([]*nfv.SoftwareImageInformation, 0, len(files))
	for _, file := range files {
		imageInfo, err := convertImage(file)
		if err != nil {
			continue
		}
		res = append(res, imageInfo)
	}
	return res, nil
}

func (m *manager) UploadImage(ctx context.Context, id *nfv.Identifier, location string) error {
	if id == nil || id.Value == "" {
		return fmt.Errorf("id should not be empty")
	}
	m.lock.Lock()
	defer m.lock.Unlock()

	files, err := os.ReadDir(m.location)
	if err != nil {
		return fmt.Errorf("failed to read source directory: %v", err)
	}
	var sourceFilePath string
	for _, file := range files {
		if nameMatchRule(id, file) {
			sourceFilePath = filepath.Join(m.location, file.Name())
			break
		}
	}
	if sourceFilePath == "" {
		return fmt.Errorf("file with Id \"%s\" not found", id.Value)
	}

	destDir := filepath.Dir(location)
	info, err := os.Stat(destDir)
	if os.IsNotExist(err) {
		return fmt.Errorf("destination directory does not exist: %v", destDir)
	}
	if err != nil || !info.IsDir() {
		return fmt.Errorf("failed to access destination directory: %v", err)
	}

	sourceFile, err := os.Open(sourceFilePath)
	if err != nil {
		return fmt.Errorf("failed to open source file \"%s\": %w", sourceFilePath, err)
	}
	defer sourceFile.Close()

	destinationFilePath := filepath.Join(location, id.Value)
	destFile, err := os.Create(destinationFilePath)
	if err != nil {
		return fmt.Errorf("failed to create destination file \"%s\": %w", destinationFilePath, err)
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return fmt.Errorf("failed to copy from \"%s\" to \"%s\": %w", sourceFilePath, destinationFilePath, err)
	}
	return nil
}

func convertImage(file os.DirEntry) (*nfv.SoftwareImageInformation, error) {
	// TODO:
	return &nfv.SoftwareImageInformation{}, nil
}

// nameMatchRule checks if the file name matches the identifier, ignoring the extension.
// Rule is based on the name equality without extension eg.
// nameMatchRule("ubuntu-22.0.4", file{name: ubuntu-22.0.4}) -> true (equal match)
// nameMatchRule("ubuntu-22.0.4", file{name: ubuntu-22.0.4.iso}) -> true (match without extension)
func nameMatchRule(id *nfv.Identifier, file os.DirEntry) bool {
	if id == nil {
		return false
	}
	fileName := file.Name()
	fileBase := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	return fileBase == id.Value || fileName == id.Value
}
