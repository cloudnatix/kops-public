/*
Copyright 2020 The Kubernetes Authors.

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

package azure

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2020-06-01/compute"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/kops/pkg/nodeidentity"
)

// InstanceGroupNameTag is the key of the tag used to identify an
// instance group that VM ScaleSet belongs.
const InstanceGroupNameTag = "kops.k8s.io_instancegroup"

type vmssGetter interface {
	getVMScaleSet(ctx context.Context, vmssName string) (compute.VirtualMachineScaleSet, error)
}

var _ vmssGetter = &client{}

// nodeIdentifier identifies a node from Azure VM.
type nodeIdentifier struct {
	vmssGetter vmssGetter
}

var _ nodeidentity.Identifier = &nodeIdentifier{}

// New creates and returns a a node identifier for Nodes running on Azure.
func New() (nodeidentity.Identifier, error) {
	client, err := newClient()
	if err != nil {
		return nil, err
	}

	return &nodeIdentifier{
		vmssGetter: client,
	}, nil
}

// IdentifyNode queries Azure for the node identity information.
func (i *nodeIdentifier) IdentifyNode(ctx context.Context, node *corev1.Node) (*nodeidentity.Info, error) {
	providerID := node.Spec.ProviderID
	if providerID == "" {
		return nil, fmt.Errorf("providerID was not set for node %s", node.Name)
	}
	vmssName, err := getVMSSNameFromProviderID(providerID)
	if err != nil {
		return nil, fmt.Errorf("error on extracting VM ScaleSet name: %s", err)
	}

	vmss, err := i.vmssGetter.getVMScaleSet(ctx, vmssName)
	if err != nil {
		return nil, fmt.Errorf("error on getting VM ScaleSet: %s", err)
	}

	var igName string
	for k, v := range vmss.Tags {
		if k == InstanceGroupNameTag {
			igName = *v
		}
	}
	if igName == "" {
		return nil, fmt.Errorf("%s tag not set on VM ScaleSet %s", InstanceGroupNameTag, *vmss.Name)
	}

	return &nodeidentity.Info{
		InstanceGroup: igName,
	}, nil
}

func getVMSSNameFromProviderID(providerID string) (string, error) {
	if !strings.HasPrefix(providerID, "azure://") {
		return "", fmt.Errorf("providerID %q not recognized", providerID)
	}

	l := strings.Split(strings.TrimPrefix(providerID, "azure://"), "/")
	if len(l) != 11 {
		return "", fmt.Errorf("unexpected format of providerID %q", providerID)
	}
	return l[len(l)-3], nil
}
