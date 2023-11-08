// Copyright 2018 The Operator-SDK Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package test

import (
	"fmt"
	"os"

	"golang.org/x/net/context"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GetOperatorNamespace will return an Operator Namespace,
// if the flag --operator-namespace  not be used (TestOpeatorNamespaceEnv not set)
// then it will create a new namespace with randon name and return that namespace
func (ctx *Context) GetOperatorNamespace() (string, error) {
	var err error
	ctx.operatorNamespace, err = ctx.getNamespace(ctx.operatorNamespace)
	return ctx.operatorNamespace, err
}

func (ctx *Context) getNamespace(ns string) (string, error) {
	if ns != "" {
		return ns, nil
	}
	// create namespace
	ns = ctx.GetID()
	namespaceObj := &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}
	_, err := ctx.kubeclient.CoreV1().Namespaces().Create(context.TODO(), namespaceObj, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return "", fmt.Errorf("namespace %s already exists: %w", ns, err)
	} else if err != nil {
		return "", err
	}
	ctx.AddCleanupFn(func() error {
		gracePeriodSeconds := int64(0)
		opts := metav1.DeleteOptions{GracePeriodSeconds: &gracePeriodSeconds}
		return ctx.kubeclient.CoreV1().Namespaces().Delete(context.TODO(), ns, opts)
	})
	return ns, nil
}

// GetWatchNamespace will return the  namespaces to operator
// watch for changes, if the flag --watch-namespaced not be used
// then it will  return the Operator Namespace.
func (ctx *Context) GetWatchNamespace() (string, error) {
	// if ctx.watchNamespace is already set and not "";
	// then return ctx.watchnamespace
	if ctx.watchNamespace != "" {
		return ctx.watchNamespace, nil
	}
	// if ctx.watchNamespace == "";
	// ensure it was set explicitly using TestWatchNamespaceEnv
	if ns, ok := os.LookupEnv(TestWatchNamespaceEnv); ok {
		return ns, nil
	}
	// get ctx.operatorNamespace (use ctx.GetOperatorNamespace()
	// to make sure ctx.operatorNamespace is not "")
	operatorNamespace, err := ctx.GetOperatorNamespace()
	if err != nil {
		return "", nil
	}
	ctx.watchNamespace = operatorNamespace
	return ctx.watchNamespace, nil
}
