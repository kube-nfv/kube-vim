package k8s

import (
	"context"
	"fmt"
	"time"

	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const cacheSyncPollInterval = 50 * time.Millisecond

// WaitForCached blocks until the cache-backed reader returns every object in objs, so
// reads made after a write (by this or any later call) see what was just created.
func WaitForCached(ctx context.Context, reader client.Reader, timeout time.Duration, objs ...client.Object) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for _, obj := range objs {
		key := client.ObjectKeyFromObject(obj)
		probe := obj.DeepCopyObject().(client.Object)
		for {
			err := reader.Get(ctx, key, probe)
			if err == nil {
				break
			}
			if !k8s_errors.IsNotFound(err) {
				return fmt.Errorf("get %T '%s' from cache: %w", obj, key, err)
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("%T '%s' not in cache after %s: %w", obj, key, timeout, ctx.Err())
			case <-time.After(cacheSyncPollInterval):
			}
		}
	}
	return nil
}
