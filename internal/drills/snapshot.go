package drills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/util/retry"

	"github.com/chanupen/devops-lab/internal/state"
)

// volatile are annotations controllers change on their own; restore and
// compare leave them alone.
var volatile = []string{
	"deployment.kubernetes.io/revision", // the Deployment controller counts rollouts
	"argocd.argoproj.io/refresh",        // a refresh Argo CD has not picked up yet
}

func gvr(apiVersion, resource string) (schema.GroupVersionResource, error) {
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return schema.GroupVersionResource{}, err
	}
	return gv.WithResource(resource), nil
}

func client(dyn dynamic.Interface, apiVersion, resource, namespace string) (dynamic.ResourceInterface, error) {
	g, err := gvr(apiVersion, resource)
	if err != nil {
		return nil, err
	}
	if namespace == "" {
		return dyn.Resource(g), nil
	}
	return dyn.Resource(g).Namespace(namespace), nil
}

// Capture reads each object as it is now, for restore to put back. An
// object that does not exist is saved as Absent.
func Capture(ctx context.Context, dyn dynamic.Interface, refs []Ref) ([]state.SavedObject, error) {
	out := make([]state.SavedObject, 0, len(refs))
	for _, r := range refs {
		c, err := client(dyn, r.APIVersion, r.Resource, r.Namespace)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", r, err)
		}
		saved := state.SavedObject{APIVersion: r.APIVersion, Resource: r.Resource, Namespace: r.Namespace, Name: r.Name}
		u, err := c.Get(ctx, r.Name, metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
			saved.Absent = true
		case err != nil:
			return nil, fmt.Errorf("save %s: %w", r, err)
		default:
			if saved.Object, err = json.Marshal(strip(u).Object); err != nil {
				return nil, err
			}
		}
		out = append(out, saved)
	}
	return out, nil
}

// strip keeps what restore puts back: the content (spec, data, ...) and
// metadata's name, namespace, labels and annotations.
func strip(u *unstructured.Unstructured) *unstructured.Unstructured {
	o := map[string]any{}
	for k, v := range u.Object {
		if k != "status" && k != "metadata" {
			o[k] = v
		}
	}
	meta := map[string]any{"name": u.GetName()}
	if ns := u.GetNamespace(); ns != "" {
		meta["namespace"] = ns
	}
	if l := u.GetLabels(); len(l) > 0 {
		meta["labels"] = toAny(l)
	}
	if a := u.GetAnnotations(); len(a) > 0 {
		meta["annotations"] = toAny(a)
	}
	o["metadata"] = meta
	return &unstructured.Unstructured{Object: o}
}

func toAny(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Restore puts the saved objects back, last first: an object that was
// absent is deleted; one that is gone is created; any other gets its
// saved content, labels and annotations, whatever changed them. It is
// idempotent, so it can run again after a crash halfway through.
func Restore(ctx context.Context, dyn dynamic.Interface, saved []state.SavedObject) error {
	var errs []error
	for _, s := range slices.Backward(saved) {
		if err := restoreOne(ctx, dyn, s); err != nil {
			errs = append(errs, fmt.Errorf("restore %s/%s: %w", s.Resource, s.Name, err))
		}
	}
	return errors.Join(errs...)
}

func restoreOne(ctx context.Context, dyn dynamic.Interface, s state.SavedObject) error {
	c, err := client(dyn, s.APIVersion, s.Resource, s.Namespace)
	if err != nil {
		return err
	}
	if s.Absent {
		err := c.Delete(ctx, s.Name, metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	want, err := decode(s.Object)
	if err != nil {
		return err
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cur, err := c.Get(ctx, s.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = c.Create(ctx, want.DeepCopy(), metav1.CreateOptions{})
			return err
		}
		if err != nil {
			return err
		}
		apply(cur, want.DeepCopy())
		_, err = c.Update(ctx, cur, metav1.UpdateOptions{})
		return err
	})
}

// apply makes cur's content, labels and annotations those of want, keeping
// the annotations controllers own as they are now.
func apply(cur, want *unstructured.Unstructured) {
	for k := range cur.Object {
		if k != "metadata" && k != "status" && k != "apiVersion" && k != "kind" {
			if _, ok := want.Object[k]; !ok {
				delete(cur.Object, k)
			}
		}
	}
	for k, v := range want.Object {
		if k != "metadata" && k != "status" {
			cur.Object[k] = v
		}
	}
	cur.SetLabels(want.GetLabels())
	ann := want.GetAnnotations()
	for _, k := range volatile {
		if v, ok := cur.GetAnnotations()[k]; ok {
			if ann == nil {
				ann = map[string]string{}
			}
			ann[k] = v
		}
	}
	cur.SetAnnotations(ann)
}

// decode reads a saved object the way client-go does, so whole numbers
// stay int64.
func decode(b []byte) (*unstructured.Unstructured, error) {
	u := &unstructured.Unstructured{}
	if err := u.UnmarshalJSON(b); err != nil {
		return nil, err
	}
	return u, nil
}

// Diff compares the saved objects with the cluster and returns one line
// per object that is not back as it was; nil when all are.
func Diff(ctx context.Context, dyn dynamic.Interface, saved []state.SavedObject) ([]string, error) {
	var diffs []string
	for _, s := range saved {
		c, err := client(dyn, s.APIVersion, s.Resource, s.Namespace)
		if err != nil {
			return nil, err
		}
		cur, err := c.Get(ctx, s.Name, metav1.GetOptions{})
		name := s.Resource + "/" + s.Name
		switch {
		case apierrors.IsNotFound(err) && s.Absent:
			continue
		case apierrors.IsNotFound(err):
			diffs = append(diffs, name+": missing")
			continue
		case err != nil:
			return nil, err
		case s.Absent:
			diffs = append(diffs, name+": exists, but did not before the drill")
			continue
		}
		want, err := decode(s.Object)
		if err != nil {
			return nil, err
		}
		if d := compare(Normalize(want), Normalize(cur)); d != "" {
			diffs = append(diffs, name+": "+d)
		}
	}
	return diffs, nil
}

// Normalize is the part of an object a drill must leave as it found it:
// content, labels and annotations, without status, server fields and the
// annotations controllers change on their own.
func Normalize(u *unstructured.Unstructured) map[string]any {
	s := strip(u).Object
	meta := s["metadata"].(map[string]any)
	if a, ok := meta["annotations"].(map[string]any); ok {
		a = maps.Clone(a)
		for _, k := range volatile {
			delete(a, k)
		}
		if len(a) == 0 {
			delete(meta, "annotations")
		} else {
			meta["annotations"] = a
		}
	}
	// Argo CD writes the running operation into the Application; it is
	// not desired state.
	delete(s, "operation")
	return s
}

// compare names the top-level fields (or metadata parts) that differ.
func compare(want, got map[string]any) string {
	var diff []string
	keys := slices.Sorted(maps.Keys(want))
	for k := range got {
		if _, ok := want[k]; !ok {
			keys = append(keys, k)
		}
	}
	for _, k := range keys {
		if k == "metadata" {
			wm, _ := want[k].(map[string]any)
			gm, _ := got[k].(map[string]any)
			for _, f := range []string{"labels", "annotations"} {
				if !reflect.DeepEqual(roundTrip(wm[f]), roundTrip(gm[f])) {
					diff = append(diff, "metadata."+f)
				}
			}
			continue
		}
		if !reflect.DeepEqual(roundTrip(want[k]), roundTrip(got[k])) {
			diff = append(diff, k)
		}
	}
	if len(diff) == 0 {
		return ""
	}
	return strings.Join(diff, ", ") + " differ"
}

// roundTrip makes values from JSON and from the API comparable (int64
// against float64).
func roundTrip(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}
