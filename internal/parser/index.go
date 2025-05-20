package parser

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var accessor = meta.NewAccessor()

type ResourceIndex map[ref]runtime.Object

func (r ResourceIndex) Push(obj runtime.Object) error {
	gvk := obj.GetObjectKind().GroupVersionKind()
	ns, _ := accessor.Namespace(obj)
	name, _ := accessor.Name(obj)

	ref := ref{
		GroupKind: schema.GroupKind{
			Group: gvk.Group,
			Kind:  gvk.Kind,
		},
		Name:      name,
		Namespace: ns,
	}

	if _, ok := r[ref]; ok {
		return fmt.Errorf("object already exists: %#v", ref)
	}

	r[ref] = obj
	return nil
}

type ref struct {
	schema.GroupKind
	Name      string
	Namespace string
}
