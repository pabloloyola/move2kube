/*
Copyright IBM Corporation 2020

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package apiresource

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/konveyor/move2kube/internal/common"
	irtypes "github.com/konveyor/move2kube/internal/types"
	"github.com/konveyor/move2kube/types"
	collecttypes "github.com/konveyor/move2kube/types/collection"
	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
)

const (
	selector = types.GroupName + "/service"
)

// IAPIResource defines the interface to be defined for a new api resource
type IAPIResource interface {
	GetSupportedKinds() []string
	CreateNewResources(ir irtypes.EnhancedIR, supportedKinds []string) []runtime.Object
	// Return nil if not supported
	ConvertToClusterSupportedKinds(obj runtime.Object, supportedKinds []string, otherobjs []runtime.Object, enhancedIR irtypes.EnhancedIR) ([]runtime.Object, bool)
}

// APIResource defines functions that are reusable across the api resources
type APIResource struct {
	IAPIResource
	cluster    collecttypes.ClusterMetadataSpec
	Scheme     *runtime.Scheme
	cachedobjs []runtime.Object
}

// SetClusterContext sets the cluster context
func (o *APIResource) SetClusterContext(cluster collecttypes.ClusterMetadataSpec) {
	o.cluster = cluster
}

// LoadResources loads the resources
// Returns resources it could not handle
func (o *APIResource) LoadResources(objs []runtime.Object, ir irtypes.EnhancedIR) []runtime.Object {
	ignoredResources := []runtime.Object{}
	for _, obj := range objs {
		if obj == nil {
			continue
		}
		if !o.loadResource(obj, objs, ir) {
			ignoredResources = append(ignoredResources, obj)
		}
	}
	return ignoredResources
}

// GetUpdatedResources converts IR to a runtime object
func (o *APIResource) GetUpdatedResources(ir irtypes.EnhancedIR) []runtime.Object {
	objs := o.CreateNewResources(ir, o.getClusterSupportedKinds())
	for _, obj := range objs {
		if !o.loadResource(obj, objs, ir) {
			log.Errorf("Object created seems to be of an incompatible type : %+v", obj)
		}
	}
	return o.cachedobjs
}

func (o *APIResource) isSupportedKind(obj runtime.Object) bool {
	kind := obj.GetObjectKind().GroupVersionKind().Kind
	return common.IsStringPresent(o.GetSupportedKinds(), kind)
}

// loadResource returns false if it could not handle the resource.
func (o *APIResource) loadResource(obj runtime.Object, otherobjs []runtime.Object, ir irtypes.EnhancedIR) bool {
	if !o.isSupportedKind(obj) {
		return false
	}
	supportedobjs, ok := o.ConvertToClusterSupportedKinds(obj, o.getClusterSupportedKinds(), otherobjs, ir)
	if !ok {
		return false
	}
	if o.cachedobjs == nil {
		// TODO: might need to merge supportedobjs with itself here if they are not all unique.
		// Alternatively assume ConvertToClusterSupportedKinds always gives unique resources.
		o.cachedobjs = supportedobjs
		return true
	}

	for _, supportedobj := range supportedobjs {
		merged := false
		for i, cachedobj := range o.cachedobjs {
			if mergedobj, ok := o.merge(cachedobj, supportedobj); ok {
				o.cachedobjs[i] = mergedobj
				merged = true
				break
			}
		}
		if !merged {
			o.cachedobjs = append(o.cachedobjs, supportedobj)
		}
	}

	return true
}

// Could be different versions, but will still be marked as duplicate
func (o *APIResource) isSameResource(obj1 runtime.Object, obj2 runtime.Object) bool {
	return o.shareSameID(obj1, obj2) && obj1.GetObjectKind().GroupVersionKind().GroupKind() == obj2.GetObjectKind().GroupVersionKind().GroupKind()
}

func (o *APIResource) shareSameID(obj1 runtime.Object, obj2 runtime.Object) bool {
	obj1id := o.getObjectID(obj1)
	obj2id := o.getObjectID(obj2)
	if obj1id == "" || obj2id == "" || obj1id != obj2id {
		return false
	}
	return true
}

func getServiceLabels(name string) map[string]string {
	return map[string]string{selector: name}
}

// getAnnotations configures annotations
func getAnnotations(service irtypes.Service) map[string]string {
	annotations := map[string]string{}
	for key, value := range service.Annotations {
		annotations[key] = value
	}
	return annotations
}

func (o *APIResource) merge(obj1, obj2 runtime.Object) (runtime.Object, bool) {
	if !o.isSameResource(obj1, obj2) {
		return nil, false
	}
	obj3, err := o.deepMerge(obj1, obj2)
	if err != nil {
		return obj3, false
	}
	return obj3, true
}

func (o *APIResource) getObjectID(obj runtime.Object) string {
	k8sObjValue := reflect.ValueOf(obj).Elem()
	objMeta, ok := k8sObjValue.FieldByName("ObjectMeta").Interface().(metav1.ObjectMeta)
	if !ok {
		log.Errorf("Failed to retrieve object metadata")
	}
	return objMeta.GetNamespace() + objMeta.GetName()
}

func (o *APIResource) getClusterSupportedKinds() []string {
	kinds := o.IAPIResource.GetSupportedKinds()
	supportedKinds := []string{}
	for _, kind := range kinds {
		if o.cluster.GetSupportedVersions(kind) != nil {
			supportedKinds = append(supportedKinds, kind)
		}
	}
	return supportedKinds
}

func getPodLabels(name string, networks []string) map[string]string {
	labels := getServiceLabels(name)
	networklabels := getNetworkPolicyLabels(networks)
	return common.MergeStringMaps(labels, networklabels)
}

func (o *APIResource) deepMerge(x, y runtime.Object) (runtime.Object, error) {
	xJSON, err := json.Marshal(x)
	if err != nil {
		log.Errorf("Merge failed. Failed to marshal the first object %v to json. Error: %q", x, err)
		return nil, err
	}
	yJSON, err := json.Marshal(y)
	if err != nil {
		log.Errorf("Merge failed. Failed to marshal the second object %v to json. Error: %q", y, err)
		return nil, err
	}
	mergedJSON, err := strategicpatch.StrategicMergePatch(xJSON, yJSON, x) // need to provide in reverse for proper ordering
	if err != nil {
		log.Errorf("Failed to merge the objects \n%s\n and \n%s\n Error: %q", xJSON, yJSON, err)
		return nil, err
	}
	codecs := serializer.NewCodecFactory(o.Scheme)
	obj, newGVK, err := codecs.UniversalDeserializer().Decode(mergedJSON, nil, nil)
	oldGVK := common.GetGVK(x)
	if newGVK == nil || *newGVK != oldGVK {
		err := fmt.Errorf("The group version kind after merging is different from before merging. original: %v new: %v", oldGVK, newGVK)
		log.Error(err)
		return obj, err
	}
	return obj, err
}
