package image

import (
	"context"
	"fmt"

	"github.com/DiMalovanyy/kube-vim/internal/config"
)

type option interface {
    apply(interface{}) error
}

type commonDvOpts struct {
    // Namespace within Data Volume k8s object
    Namespace string
    Ctx context.Context
}

type getDvOpts struct {
    commonDvOpts
    // Query params
    Name string
    UID string
    SourceUrl string
}

func getDefaultCommonOpts() commonDvOpts {
    return commonDvOpts{
        Namespace: config.KubeNfvDefaultNamespace,
        Ctx: context.Background(),
    }
}
func getDefaultDvOpts() getDvOpts {
    return getDvOpts{
        commonDvOpts: getDefaultCommonOpts(),
    }
}

// Specify namespace for kubevirt Data Volumes resources. If not specified dafault will be used.
type namespaceOption struct {
    Namespace string
}
func (o *namespaceOption) apply(ifOpts interface{}) error {
    commonOpts, ok := ifOpts.(*commonDvOpts)
    if !ok {
        return fmt.Errorf("namespace option can be apply only to the commonOpts: %w", ApplyOptionErr)
    }
    commonOpts.Namespace = o.Namespace
    return nil
}
func WithNamespace(namespace string) option {
    return &namespaceOption{ Namespace: namespace }
}

// Specify context for k8s api server request.
type contextOption struct {
    Ctx context.Context
}
func (o *contextOption) apply(ifOpts interface{}) error {
    commonOpts, ok := ifOpts.(*commonDvOpts)
    if !ok {
        return fmt.Errorf("context option can be apply only to the commonOpts: %w", ApplyOptionErr)
    }
    commonOpts.Ctx = o.Ctx
    return nil
}
func WithContext(ctx context.Context) option {
    return &contextOption{ Ctx: ctx }
}

// Option to specify Name for k8s resource. The best option to make Data Volume queries since it won't do bulk Get.
type getByNameOption struct {
    Name string
}
func (o *getByNameOption) apply(ifOpts interface{}) error {
    getDvOpts, ok := ifOpts.(*getDvOpts)
    if !ok {
        return fmt.Errorf("name option can be apply only to the getDvOpts: %w", ApplyOptionErr)
    }
    getDvOpts.Name = o.Name
    return nil
}
func WithName(name string) option {
    return &getByNameOption{ Name: name }
}

// Option to specify UID. If WithName specified togather it will be ignored.
type getByUIDOption struct {
    UID string
}
func (o *getByUIDOption) apply(ifOpts interface{}) error {
    getDvOpts, ok := ifOpts.(*getDvOpts)
    if !ok {
        return fmt.Errorf("uid option can be apply only to the getDvOpts: %w", ApplyOptionErr)
    }
    getDvOpts.UID = o.UID
    return nil
}
func WithUID(uid string) option {
    return &getByUIDOption{ UID: uid }
}
// Option to specify Source. If either WithName or WithUID specified it will be ignored
type getBySourceUrl struct {
    Source string
}
func (o *getBySourceUrl) apply(ifOpts interface{}) error {
    getDvOpts, ok := ifOpts.(*getDvOpts)
    if !ok {
        return fmt.Errorf("source url option can be apply only to the getDvOpts: %w", ApplyOptionErr)
    }
    getDvOpts.SourceUrl = o.Source
    return nil
}
func WithSourceUrl(sourceUrl string) option {
    return &getBySourceUrl{ Source: sourceUrl }
}
