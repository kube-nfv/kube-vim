package k8s

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// LabelIndex is a cache index over the value of Label on objects of Obj's type, named after
// the label. Look objects up with client.MatchingFields{Label: value}, so the cache answers
// from the index instead of scanning the namespace as a label selector does.
type LabelIndex struct {
	Obj   client.Object
	Label string
}

// Extract returns obj's index values: its Label value, or none.
func (i LabelIndex) Extract(obj client.Object) []string {
	if v := obj.GetLabels()[i.Label]; v != "" {
		return []string{v}
	}
	return nil
}

// IndexLabels registers indexes on indexer. It must run before the cache starts.
func IndexLabels(ctx context.Context, indexer client.FieldIndexer, indexes ...LabelIndex) error {
	for _, i := range indexes {
		if err := indexer.IndexField(ctx, i.Obj, i.Label, i.Extract); err != nil {
			return fmt.Errorf("index %T by label '%s': %w", i.Obj, i.Label, err)
		}
	}
	return nil
}
