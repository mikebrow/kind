/*
Copyright 2019 The Kubernetes Authors.

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

// Package kubeadminit implements the kubeadm init action
package kubeadminit

import (
	"strings"

	"sigs.k8s.io/kind/pkg/errors"
	"sigs.k8s.io/kind/pkg/exec"

	"sigs.k8s.io/kind/pkg/cluster/nodeutils"

	"sigs.k8s.io/kind/pkg/cluster/internal/create/actions"
)

// kubeadmInitAction implements action for executing the kubeadm init
// and a set of default post init operations like e.g. install the
// CNI network plugin.
type action struct{}

// NewAction returns a new action for kubeadm init
func NewAction() actions.Action {
	return &action{}
}

// Execute runs the action
func (a *action) Execute(ctx *actions.ActionContext) error {
	ctx.Status.Start("Starting control-plane 🕹️")
	defer ctx.Status.End(false)

	allNodes, err := ctx.Nodes()
	if err != nil {
		return err
	}

	// get the target node for this task
	// TODO: eliminate the concept of bootstrapcontrolplane node entirely
	// outside this method
	node, err := nodeutils.BootstrapControlPlaneNode(allNodes)
	if err != nil {
		return err
	}

	// run kubeadm
	cmd := node.Command(
		// init because this is the control plane node
		"kubeadm", "init",
		// skip preflight checks, as these have undesirable side effects
		// and don't tell us much. requires kubeadm 1.13+
		"--skip-phases=preflight",
		// specify our generated config file
		"--config=/kind/kubeadm.conf",
		"--skip-token-print",
		// increase verbosity for debugging
		"--v=6",
	)
	lines, err := exec.CombinedOutputLines(cmd)
	ctx.Logger.V(3).Info(strings.Join(lines, "\n"))
	if err != nil {
		return errors.Wrap(err, "failed to init node with kubeadm")
	}

	// copy some files to the other control plane nodes
	otherControlPlanes, err := nodeutils.SecondaryControlPlaneNodes(allNodes)
	if err != nil {
		return err
	}
	for _, otherNode := range otherControlPlanes {
		for _, file := range []string{
			// copy over admin config so we can use any control plane to get it later
			"/etc/kubernetes/admin.conf",
			// copy over certs
			"/etc/kubernetes/pki/ca.crt", "/etc/kubernetes/pki/ca.key",
			"/etc/kubernetes/pki/front-proxy-ca.crt", "/etc/kubernetes/pki/front-proxy-ca.key",
			"/etc/kubernetes/pki/sa.pub", "/etc/kubernetes/pki/sa.key",
			// TODO: if we gain external etcd support these will be
			// handled differently
			"/etc/kubernetes/pki/etcd/ca.crt", "/etc/kubernetes/pki/etcd/ca.key",
		} {
			if err := nodeutils.CopyNodeToNode(node, otherNode, file); err != nil {
				return errors.Wrap(err, "failed to copy admin kubeconfig")
			}
		}
	}

	// if we are only provisioning one node, remove the master taint
	// https://kubernetes.io/docs/setup/independent/create-cluster-kubeadm/#master-isolation
	if len(allNodes) == 1 {
		if err := node.Command(
			"kubectl", "--kubeconfig=/etc/kubernetes/admin.conf",
			"taint", "nodes", "--all", "node-role.kubernetes.io/master-",
		).Run(); err != nil {
			return errors.Wrap(err, "failed to remove master taint")
		}
	}

	// mark success
	ctx.Status.End(true)
	return nil
}
