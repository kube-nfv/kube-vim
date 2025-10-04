package errors

import (
	"errors"
	"fmt"
)

// Untyped errors for simple cases that don't need additional fields
var (
	ErrNotImplemented = errors.New("not implemented")
	ErrUnsupported    = errors.New("unsupported")
)

// Typed errors for cases that benefit from carrying additional fields

// ErrNotFound for cases where entity and identifier matter
type ErrNotFound struct {
	Entity     string
	Identifier string
}

func (e *ErrNotFound) Error() string {
	if e.Identifier != "" {
		return fmt.Sprintf("%s '%s' not found", e.Entity, e.Identifier)
	}
	return fmt.Sprintf("%s not found", e.Entity)
}

// ErrInvalidArgument for cases where field and reason matter
type ErrInvalidArgument struct {
	Field  string
	Reason string
}

func (e *ErrInvalidArgument) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("invalid %s: %s", e.Field, e.Reason)
	}
	return fmt.Sprintf("invalid %s", e.Field)
}

// ErrAlreadyExists for cases where entity and identifier matter
type ErrAlreadyExists struct {
	Entity     string
	Identifier string
}

func (e *ErrAlreadyExists) Error() string {
	if e.Identifier != "" {
		return fmt.Sprintf("%s '%s' already exists", e.Entity, e.Identifier)
	}
	return fmt.Sprintf("%s already exists", e.Entity)
}

// ErrPermissionDenied for cases where resource and reason matter
type ErrPermissionDenied struct {
	Resource string
	Reason   string
}

func (e *ErrPermissionDenied) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("access denied to %s: %s", e.Resource, e.Reason)
	}
	return fmt.Sprintf("access denied to %s", e.Resource)
}

// ErrK8sObjectNotInstantiated indicates that the object is not from Kubernetes
type ErrK8sObjectNotInstantiated struct {
	ObjectType string
}

func (e *ErrK8sObjectNotInstantiated) Error() string {
	return fmt.Sprintf("%s is not from Kubernetes (likely created manually)", e.ObjectType)
}

// ErrK8sObjectNotManagedByKubeNfv indicates that the object was not created by kube-nfv
type ErrK8sObjectNotManagedByKubeNfv struct {
	ObjectType string
	ObjectName string
	ObjectId   string
}

func (e *ErrK8sObjectNotManagedByKubeNfv) Error() string {
	return fmt.Sprintf("%s '%s' (uid: %s) not managed by kube-nfv", e.ObjectType, e.ObjectName, e.ObjectId)
}
