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

package apiresourceset

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	apps "k8s.io/kubernetes/pkg/apis/apps"
	networking "k8s.io/kubernetes/pkg/apis/networking"

	"github.com/konveyor/move2kube/internal/apiresource"
)

type fixFunc func(obj runtime.Object) (runtime.Object, error)

var (
	fixFuncs map[string]fixFunc
)

func init() {
	fixFuncs = map[string]fixFunc{
		apiresource.DeploymentKind: fixDeployment,
		apiresource.IngressKind:    fixIngress,
	}
}

func fixDeployment(obj runtime.Object) (runtime.Object, error) {
	d, ok := obj.(*apps.Deployment)
	if !ok {
		return obj, fmt.Errorf("Non Matching type. Expected Deployment : Got %T", obj)
	}
	if d.Spec.Selector == nil {
		d.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: d.Spec.Template.Labels,
		}
	}
	obj = d
	return obj, nil
}

func fixIngress(obj runtime.Object) (runtime.Object, error) {
	ptf := networking.PathTypePrefix
	i, ok := obj.(*networking.Ingress)
	if !ok {
		return obj, fmt.Errorf("Non Matching type. Expected Ingress : Got %T", obj)
	}
	for ri, r := range i.Spec.Rules {
		for pi, p := range r.HTTP.Paths {
			if p.PathType == nil {
				i.Spec.Rules[ri].HTTP.Paths[pi].PathType = &ptf
			}
		}
	}
	obj = i
	return obj, nil
}
