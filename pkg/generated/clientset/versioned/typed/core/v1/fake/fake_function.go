/*
Copyright The Fission Authors.

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

// Code generated by client-gen. DO NOT EDIT.

package fake

import (
	v1 "github.com/fission/fission/pkg/apis/core/v1"
	corev1 "github.com/fission/fission/pkg/generated/applyconfiguration/core/v1"
	typedcorev1 "github.com/fission/fission/pkg/generated/clientset/versioned/typed/core/v1"
	gentype "k8s.io/client-go/gentype"
)

// fakeFunctions implements FunctionInterface
type fakeFunctions struct {
	*gentype.FakeClientWithListAndApply[*v1.Function, *v1.FunctionList, *corev1.FunctionApplyConfiguration]
	Fake *FakeCoreV1
}

func newFakeFunctions(fake *FakeCoreV1, namespace string) typedcorev1.FunctionInterface {
	return &fakeFunctions{
		gentype.NewFakeClientWithListAndApply[*v1.Function, *v1.FunctionList, *corev1.FunctionApplyConfiguration](
			fake.Fake,
			namespace,
			v1.SchemeGroupVersion.WithResource("functions"),
			v1.SchemeGroupVersion.WithKind("Function"),
			func() *v1.Function { return &v1.Function{} },
			func() *v1.FunctionList { return &v1.FunctionList{} },
			func(dst, src *v1.FunctionList) { dst.ListMeta = src.ListMeta },
			func(list *v1.FunctionList) []*v1.Function { return gentype.ToPointerSlice(list.Items) },
			func(list *v1.FunctionList, items []*v1.Function) { list.Items = gentype.FromPointerSlice(items) },
		),
		fake,
	}
}
