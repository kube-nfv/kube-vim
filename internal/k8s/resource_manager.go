package k8s

import (
	"context"
	"sync"

	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
    corev1 "k8s.io/api/core/v1"
)

type NodeStore interface {
    GetNodes() []Node
}

// Object is responsibe for tracking k8s resources availability
// It should track nodes availability as well as node resources
type resourceManager struct {
    client *kubernetes.Clientset
    logger *zap.SugaredLogger
    lock sync.RWMutex

    nodes map[string]*corev1.Node
}

func NewResourceManager(logger *zap.SugaredLogger, client *kubernetes.Clientset) (*resourceManager, error) {
    return &resourceManager{
        client: client,
        logger: logger,
        lock: sync.RWMutex{},
    }, nil
}

func (m *resourceManager) Start(ctx context.Context) error {

    return nil
}

func (m *resourceManager) GetNodes() []Node {
    m.lock.RLock()
    defer m.lock.RUnlock()

    return nil
}
