/*
Copyright 2025 The Local Storage Operator Authors.

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

package networkpolicy

import (
	"context"

	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/openshift/local-storage-operator/assets"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type NetworkPolicyReconciler struct {
	Client client.Client
	Scheme *runtime.Scheme
}

func (r *NetworkPolicyReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	klog.InfoS("Reconciling NetworkPolicies", "namespace", request.Namespace, "name", request.Name)

	manifests := [...]string{
		"network-policy/allow-operand-egress-to-api-server.yaml",
		"network-policy/allow-operand-egress-to-dns.yaml",
		"network-policy/allow-operand-ingress-to-metrics.yaml",
	}

	for _, manifest := range manifests {
		npBytes, err := assets.ReadFileAndReplace(
			manifest,
			[]string{
				"${OBJECT_NAMESPACE}", request.Namespace,
			},
		)
		if err != nil {
			return ctrl.Result{Requeue: true}, err
		}
		np := resourceread.ReadNetworkPolicyV1OrDie(npBytes)

		opResult, err := controllerutil.CreateOrUpdate(ctx, r.Client, np, nil)
		if err != nil {
			return ctrl.Result{Requeue: true}, err
		} else if opResult == controllerutil.OperationResultUpdated ||
			opResult == controllerutil.OperationResultCreated {
			klog.InfoS("NetworkPolicy changed", "Name", np.GetName(), "opResult", opResult)
		}
	}

	return ctrl.Result{}, nil
}

func (r *NetworkPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	enqueueOnlyNamespace := handler.EnqueueRequestsFromMapFunc(
		func(ctx context.Context, obj client.Object) []reconcile.Request {
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: obj.GetNamespace()},
			}
			return []reconcile.Request{req}
		})

	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.NetworkPolicy{}).
		Watches(&networkingv1.NetworkPolicy{}, enqueueOnlyNamespace).
		Complete(r)

	// TODO: enqueue at least one initial request

	// TODO: can we handle cleanup too? How will it get deleted?
}
