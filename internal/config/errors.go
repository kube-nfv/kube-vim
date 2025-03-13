package common

import (
	"errors"
	"fmt"
)

// TODO: Change this errors to the gRPC Errors
var (
	UnsupportedErr     = errors.New("unsupported")
	NotImplementedErr  = errors.New("not implemented")
	NotFoundErr        = errors.New("not found")
	InvalidArgumentErr = errors.New("invalid argument")
)

// The error that indicates that the object is not exist in the k8s cluster (not received via Get request).
type K8sObjectNotInstantiatedErr struct {
	ObjectType string
}

func (e *K8sObjectNotInstantiatedErr) Error() string {
	return fmt.Sprintf("%s is not from Kubernetes (likely created manually)", e.ObjectType)
}

// The error that indicates that the object was not created by the kube-vim. In the most cases identified by the
// k8s managed-by label.
type K8sObjectNotManagedByKubeNfvErr struct {
	ObjectType string
	ObjectName string
	ObjectId   string
}

func (e *K8sObjectNotManagedByKubeNfvErr) Error() string {
	return fmt.Sprintf("%s \"%s\" with uid \"%s\" is not managed by the kube-nfv", e.ObjectType, e.ObjectName, e.ObjectId)
}
